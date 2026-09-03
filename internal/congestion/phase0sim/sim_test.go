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

// SPIKE-46, SPIKE-47: greedy controller flow vs one long-lived TCP flow on a
// shared bottleneck. The hard assertion is the anti-flood floor from design
// §17.4 (native TCP keeps >= 35%): the custom UDP data plane must never starve
// TCP. The Jain index is reported for the transport decision (SPIKE-48 / D-P0-4)
// rather than asserted here, because the design's delay-gated controller
// deliberately yields to bufferbloating loss-based TCP and the "reject vs
// reopen transport" call is made in the decision record with the buffer sweep
// below in hand.
func TestFairnessAgainstTCP(t *testing.T) {
	ctrlBps, tcpBps := runFairness(0.06) // ~60 ms drop-tail buffer
	total := ctrlBps + tcpBps
	if total == 0 {
		t.Fatal("no throughput")
	}
	tcpShare := tcpBps / total
	jain := Jain(ctrlBps, tcpBps)

	t.Logf("controller = %.2f Mbit/s, TCP = %.2f Mbit/s (link 20, buffer ~60ms)",
		mbps(ctrlBps)*8, mbps(tcpBps)*8)
	t.Logf("TCP share = %.1f%%  Jain = %.3f  utilisation = %.1f%%",
		tcpShare*100, jain, total/(20_000_000.0/8)*100)

	if tcpShare < 0.35 {
		t.Errorf("SPIKE-48 floor: TCP kept only %.1f%% of the bottleneck (< 35%%) — the UDP flow is starving TCP", tcpShare*100)
	}
	if jain < 0.90 {
		t.Logf("NOTE (D-P0-4): Jain %.3f < 0.90 at this buffer depth — controller yields to TCP; carried to the transport decision", jain)
	}
}

// SPIKE-48: buffer-depth sweep. Fairness of the delay-gated controller against
// loss-based TCP degrades as the drop-tail buffer grows past the 15 ms target.
func TestFairnessBufferSweep(t *testing.T) {
	for _, bufMs := range []float64{5, 10, 15, 20, 30, 45, 60, 100} {
		ctrlBps, tcpBps := runFairness(bufMs / 1000)
		total := ctrlBps + tcpBps
		jain := Jain(ctrlBps, tcpBps)
		t.Logf("buffer %3.0f ms | controller %5.2f | TCP %5.2f Mbit/s | TCP %4.1f%% | Jain %.3f",
			bufMs, mbps(ctrlBps)*8, mbps(tcpBps)*8, tcpBps/total*100, jain)
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
