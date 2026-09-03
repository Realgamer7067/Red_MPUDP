// Package phase0sim is an M02 Phase 0 spike: a deterministic, fluid-model
// simulator used to check that the design §9.5 per-direction AIMD controller can
// share a bottleneck fairly with a standard TCP-style flow (SPIKE-39..48).
//
// It is NOT the production controller. Milestone M17 builds that in
// internal/congestion/{controller,pacer}.go; this package is removed once the
// transport decision (SPIKE-66) is recorded.
package phase0sim

import "math"

// Clock is a fake monotonic clock advanced explicitly by the simulator
// (SPIKE-41). Time is whole nanoseconds.
type Clock struct{ ns int64 }

func (c *Clock) Now() int64      { return c.ns }
func (c *Clock) Advance(d int64) { c.ns += d }

const (
	ms = int64(1_000_000)
	s  = 1000 * ms
)

// Link is a single FIFO bottleneck: constant capacity, finite buffer, tail
// drop. Rates and the queue are in bytes and bytes/second.
type Link struct {
	CapacityBps float64
	BufferBytes float64
	BaseRTTns   int64

	queue float64
}

// step offers each flow's bytes for the interval dt (seconds), drains the queue
// at capacity, and returns whether the buffer overflowed (a loss signal shared
// by every flow that fed it this step) plus the post-step one-way queue delay.
func (l *Link) step(offered float64, dt float64) (lost bool, queueDelayNs int64) {
	l.queue += offered
	drain := l.CapacityBps * dt
	if l.queue < drain {
		l.queue = 0
	} else {
		l.queue -= drain
	}
	if l.queue > l.BufferBytes {
		l.queue = l.BufferBytes
		lost = true
	}
	queueDelayNs = int64(l.queue / l.CapacityBps * float64(s))
	return lost, queueDelayNs
}

func (l *Link) rttNs(queueDelayNs int64) int64 { return l.BaseRTTns + queueDelayNs }

// TCPFlow is the reference: one-packet-per-RTT additive increase, halve on loss,
// evaluated once per RTT.
type TCPFlow struct {
	MSS       float64
	RateBps   float64
	MinBps    float64
	nextEval  int64
	lostSince bool
}

func (f *TCPFlow) rate() float64 { return f.RateBps }

func (f *TCPFlow) observeLoss() { f.lostSince = true }

func (f *TCPFlow) tick(now, rttNs int64) {
	if now < f.nextEval {
		return
	}
	if f.lostSince {
		f.RateBps *= 0.5
	} else {
		f.RateBps += f.MSS / (float64(rttNs) / float64(s))
	}
	if f.RateBps < f.MinBps {
		f.RateBps = f.MinBps
	}
	f.lostSince = false
	f.nextEval = now + rttNs
}

// Controller is the design §9.5 model as an integer-bytes-per-second AIMD
// controller (SPIKE-40): once-per-RTT additive increase of one confirmed outer
// MTU per SRTT when there is fresh feedback, no new loss, and queue delay under
// target; halve on newly finalized loss (at most once per RTT); halve after two
// consecutive high-queue-delay rounds; drop to the minimum when feedback is
// stale.
type Controller struct {
	MTUBytes        int64
	RateBps         int64
	MinBps          int64
	MaxBps          int64
	QueueTargetNs   int64
	FeedbackStaleNs int64

	srttNs        int64
	nextEval      int64
	lossSince     bool
	highDelayRuns int
	lastFeedback  int64
}

func (c *Controller) rate() float64 { return float64(c.RateBps) }

func (c *Controller) observeLoss() { c.lossSince = true }

// feedback marks that a fresh authenticated report arrived at time now with the
// given measured one-way queue delay; srtt is updated RFC6298-style.
func (c *Controller) feedback(now, rttNs int64) {
	c.lastFeedback = now
	if c.srttNs == 0 {
		c.srttNs = rttNs
		return
	}
	// srtt = 7/8 srtt + 1/8 sample
	c.srttNs = (7*c.srttNs + rttNs) / 8
}

func (c *Controller) tick(now, queueDelayNs int64) {
	if now < c.nextEval {
		return
	}
	defer func() { c.nextEval = now + c.evalPeriod() }()

	if now-c.lastFeedback > c.FeedbackStaleNs {
		c.RateBps = c.MinBps
		c.highDelayRuns = 0
		c.lossSince = false
		return
	}

	switch {
	case c.lossSince:
		c.RateBps /= 2
		c.highDelayRuns = 0
	case queueDelayNs > c.QueueTargetNs:
		c.highDelayRuns++
		if c.highDelayRuns >= 2 {
			c.RateBps /= 2
			c.highDelayRuns = 0
		}
	default:
		c.highDelayRuns = 0
		if c.srttNs > 0 {
			inc := c.MTUBytes * int64(s) / c.srttNs
			c.RateBps += inc
		}
	}
	c.lossSince = false
	c.clamp()
}

func (c *Controller) clamp() {
	if c.RateBps < c.MinBps {
		c.RateBps = c.MinBps
	}
	if c.RateBps > c.MaxBps {
		c.RateBps = c.MaxBps
	}
}

func (c *Controller) evalPeriod() int64 {
	if c.srttNs > 0 {
		return c.srttNs
	}
	return c.FeedbackStaleNs
}

// flow is anything the simulator paces on the shared link.
type flow interface {
	rate() float64
	observeLoss()
}

// Sim runs the shared-bottleneck experiment.
type Sim struct {
	Clock      Clock
	Link       Link
	Controller *Controller
	TCP        *TCPFlow

	StepNs    int64
	WarmupNs  int64
	MeasureNs int64
	StaleFeed bool // if true, stop delivering feedback to the controller after warmup

	ctrlBytes float64
	tcpBytes  float64
}

// Run advances the simulation and returns delivered throughput (bytes/s) for
// the controller and the TCP flow over the post-warmup measurement window.
func (sm *Sim) Run() (ctrlBps, tcpBps float64) {
	dt := float64(sm.StepNs) / float64(s)
	end := sm.WarmupNs + sm.MeasureNs

	// prime feedback
	sm.Controller.feedback(sm.Clock.Now(), sm.Link.rttNs(0))
	sm.TCP.nextEval = sm.Clock.Now() + sm.Link.rttNs(0)
	sm.Controller.nextEval = sm.Clock.Now() + sm.Controller.evalPeriod()

	var lastQDelay int64
	for sm.Clock.Now() < end {
		now := sm.Clock.Now()
		measuring := now >= sm.WarmupNs

		cOffer := sm.Controller.rate() * dt
		tOffer := sm.TCP.rate() * dt
		lost, qDelay := sm.Link.step(cOffer+tOffer, dt)
		lastQDelay = qDelay
		rtt := sm.Link.rttNs(qDelay)

		if lost {
			sm.Controller.observeLoss()
			sm.TCP.observeLoss()
		}

		// Delivered bytes this step = min(offered, share of drain). Approximate
		// each flow's delivered as its offer scaled by the served fraction.
		served := (sm.Link.CapacityBps * dt)
		total := cOffer + tOffer
		frac := 1.0
		if total > served && total > 0 {
			frac = served / total
		}
		if measuring {
			sm.ctrlBytes += cOffer * frac
			sm.tcpBytes += tOffer * frac
		}

		if !(sm.StaleFeed && measuring) {
			sm.Controller.feedback(now, rtt)
		}
		sm.Controller.tick(now, qDelay)
		sm.TCP.tick(now, rtt)

		sm.Clock.Advance(sm.StepNs)
	}
	_ = lastQDelay

	win := float64(sm.MeasureNs) / float64(s)
	return sm.ctrlBytes / win, sm.tcpBytes / win
}

// Jain returns the Jain fairness index of the given rates (1.0 == perfectly
// equal). SPIKE-47.
func Jain(rates ...float64) float64 {
	var sum, sumsq float64
	for _, r := range rates {
		sum += r
		sumsq += r * r
	}
	if sumsq == 0 {
		return 1
	}
	return (sum * sum) / (float64(len(rates)) * sumsq)
}

// Round-trip helper kept for tests that want a clean number.
func mbps(x float64) float64 { return math.Round(x/1e6*100) / 100 }
