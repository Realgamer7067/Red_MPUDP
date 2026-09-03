package phase0sim

import (
	"math"
	"testing"
)

// design §14 defaults, in the units this sim uses.
const (
	mtuBytes   = 1268 // inner 1180 + 88 outer overhead
	initBps    = 1_000_000 / 8
	minBps     = 128_000 / 8
	maxBps     = 100_000_000 / 8
	queueTgtNs = 15 * ms
	staleNs    = 250 * ms
)

func newController() *Controller {
	return &Controller{
		MTUBytes:        mtuBytes,
		RateBps:         initBps,
		MinBps:          minBps,
		MaxBps:          maxBps,
		QueueTargetNs:   queueTgtNs,
		FeedbackStaleNs: staleNs,
	}
}

// SPIKE-42: one-MTU-per-SRTT additive increase.
func TestAdditiveIncrease(t *testing.T) {
	c := newController()
	var clk Clock
	srtt := int64(50 * ms)
	c.feedback(clk.Now(), srtt) // sets srtt
	c.nextEval = clk.Now() + c.evalPeriod()

	start := c.RateBps
	var evals int
	for clk.Now() < 1*s {
		clk.Advance(ms)
		c.feedback(clk.Now(), srtt)
		before := c.RateBps
		c.tick(clk.Now(), 0) // queue delay 0 -> under target
		if c.RateBps != before {
			evals++
			inc := c.RateBps - before
			want := c.MTUBytes * int64(s) / c.srttNs
			if inc != want {
				t.Fatalf("increase = %d B/s, want %d B/s (one MTU per srtt)", inc, want)
			}
		}
	}
	if evals < 15 || evals > 25 {
		t.Fatalf("got %d increase events in 1s at srtt=50ms, want ~20", evals)
	}
	if c.RateBps <= start {
		t.Fatalf("rate did not increase: %d -> %d", start, c.RateBps)
	}
}

// SPIKE-43: multiplicative decrease, at most once per RTT.
func TestMultiplicativeDecreaseOncePerRTT(t *testing.T) {
	c := newController()
	c.RateBps = maxBps
	var clk Clock
	srtt := int64(40 * ms)
	c.feedback(clk.Now(), srtt)
	c.nextEval = clk.Now() + c.evalPeriod()

	// advance to first eval boundary
	clk.Advance(srtt)
	c.feedback(clk.Now(), srtt)
	c.observeLoss()
	c.observeLoss() // two losses, same RTT
	c.tick(clk.Now(), 0)
	if c.RateBps != maxBps/2 {
		t.Fatalf("after loss: rate = %d, want %d (single halving)", c.RateBps, maxBps/2)
	}

	// same RTT, another loss, tick before nextEval -> no change
	c.observeLoss()
	c.tick(clk.Now()+ms, 0)
	if c.RateBps != maxBps/2 {
		t.Fatalf("second halving inside one RTT: rate = %d", c.RateBps)
	}

	// next RTT -> halves again
	clk.Advance(srtt)
	c.feedback(clk.Now(), srtt)
	c.observeLoss()
	c.tick(clk.Now(), 0)
	if c.RateBps != maxBps/4 {
		t.Fatalf("next-RTT loss: rate = %d, want %d", c.RateBps, maxBps/4)
	}
}

// SPIKE-44: stale feedback drops the rate to the minimum.
func TestStaleFeedbackDropsToMinimum(t *testing.T) {
	c := newController()
	c.RateBps = maxBps
	var clk Clock
	srtt := int64(40 * ms)
	c.feedback(clk.Now(), srtt)
	c.nextEval = clk.Now() + c.evalPeriod()

	clk.Advance(c.FeedbackStaleNs + ms) // no feedback in this whole span
	c.tick(clk.Now(), 0)
	if c.RateBps != c.MinBps {
		t.Fatalf("stale feedback: rate = %d, want MinBps %d", c.RateBps, c.MinBps)
	}
}

// SPIKE-45: two consecutive high-queue-delay rounds halve the rate.
func TestQueueDelayReduction(t *testing.T) {
	c := newController()
	c.RateBps = maxBps
	var clk Clock
	srtt := int64(40 * ms)
	c.feedback(clk.Now(), srtt)
	c.nextEval = clk.Now() + c.evalPeriod()

	high := c.QueueTargetNs + 5*ms

	clk.Advance(srtt)
	c.feedback(clk.Now(), srtt)
	c.tick(clk.Now(), high) // round 1: no cut yet
	if c.RateBps != maxBps {
		t.Fatalf("round 1 high delay should not cut: rate = %d", c.RateBps)
	}
	clk.Advance(c.evalPeriod())
	c.feedback(clk.Now(), srtt)
	c.tick(clk.Now(), high) // round 2: cut
	if c.RateBps != maxBps/2 {
		t.Fatalf("round 2 high delay: rate = %d, want %d", c.RateBps, maxBps/2)
	}
}

func runFairness(bufferSeconds float64) (ctrlBps, tcpBps float64) {
	cap := 20_000_000.0 / 8 // 20 Mbit/s
	sm := &Sim{
		Link: Link{
			CapacityBps: cap,
			BufferBytes: cap * bufferSeconds,
			BaseRTTns:   40 * ms,
		},
		Controller: newController(),
		TCP:        &TCPFlow{MSS: 1448, RateBps: initBps, MinBps: minBps},
		StepNs:     ms,
		WarmupNs:   15 * s,
		MeasureNs:  30 * s,
	}
	return sm.Run()
}

// targetDelayMs is the controller's queue_delay_target in ms (design §14 default).
const targetDelayMs = 15

// SPIKE-46, SPIKE-47, SPIKE-48: greedy controller flow vs one long-lived TCP
// flow on a shared drop-tail bottleneck, swept across buffer depths. Asserts
// the design §9.5.1 fairness acceptance criterion in full:
//
//  1. anti-flood floor  - native TCP >= 35% at EVERY depth
//  2. equality          - Jain >= 0.90 for depths <= queue_delay_target
//  3. graceful yield     - above the target, deeper buffer => controller
//     throughput non-increasing AND native TCP share non-decreasing
func TestFairnessCriterion(t *testing.T) {
	depthsMs := []float64{5, 10, 15, 20, 30, 45, 60, 100}

	type row struct {
		bufMs            float64
		ctrl, tcp, share float64
		jain             float64
	}
	rows := make([]row, 0, len(depthsMs))
	for _, bufMs := range depthsMs {
		c, tc := runFairness(bufMs / 1000)
		total := c + tc
		if total == 0 {
			t.Fatalf("buffer %.0fms: no throughput", bufMs)
		}
		r := row{bufMs: bufMs, ctrl: c, tcp: tc, share: tc / total, jain: Jain(c, tc)}
		rows = append(rows, r)
		t.Logf("buffer %3.0f ms | controller %5.2f | TCP %5.2f Mbit/s | TCP %4.1f%% | Jain %.3f",
			bufMs, mbps(c)*8, mbps(tc)*8, r.share*100, r.jain)
	}

	const eps = 0.005 // fluid-model slack

	// Clause 1: anti-flood floor at every depth.
	for _, r := range rows {
		if r.share < 0.35 {
			t.Errorf("clause 1 (anti-flood): buffer %.0fms TCP share %.1f%% < 35%% — RED_MPUDP is starving TCP",
				r.bufMs, r.share*100)
		}
	}

	// Clause 2: Jain >= 0.90 at or below queue_delay_target.
	for _, r := range rows {
		if r.bufMs <= targetDelayMs && r.jain < 0.90 {
			t.Errorf("clause 2 (equality): buffer %.0fms (<= %dms target) Jain %.3f < 0.90",
				r.bufMs, targetDelayMs, r.jain)
		}
	}

	// Clause 3: monotonic yield above the target.
	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		if cur.bufMs <= targetDelayMs {
			continue
		}
		if cur.ctrl > prev.ctrl*(1+eps) {
			t.Errorf("clause 3 (yield): controller throughput rose from %.2f to %.2f Mbit/s as buffer grew %.0f->%.0fms",
				mbps(prev.ctrl)*8, mbps(cur.ctrl)*8, prev.bufMs, cur.bufMs)
		}
		if cur.share < prev.share-eps {
			t.Errorf("clause 3 (yield): TCP share fell from %.1f%% to %.1f%% as buffer grew %.0f->%.0fms",
				prev.share*100, cur.share*100, prev.bufMs, cur.bufMs)
		}
	}
}

func TestJain(t *testing.T) {
	if got := Jain(10, 10); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("Jain(10,10) = %v, want 1", got)
	}
	if got := Jain(10, 0); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("Jain(10,0) = %v, want 0.5", got)
	}
	if got := Jain(0, 0); got != 1 {
		t.Fatalf("Jain(0,0) = %v, want 1", got)
	}
}
