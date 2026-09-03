package testclock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/clock"
	"github.com/Realgamer7067/Red_MPUDP/internal/testclock"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestClockImplementsInterface(t *testing.T) {
	var _ clock.Clock = testclock.New(epoch)
}

func TestTimeDoesNotMoveOnItsOwn(t *testing.T) {
	c := testclock.New(epoch)
	first := c.Now()
	for i := 0; i < 1000; i++ {
		_ = c.Now()
	}
	if !c.Now().Equal(first) {
		t.Fatalf("time advanced without Advance: %v != %v", c.Now(), first)
	}
}

// BASE-02 / BASE-03: deterministic timer advancement.
func TestTimerFiresExactlyAtDeadline(t *testing.T) {
	c := testclock.New(epoch)
	tm := c.NewTimer(100 * time.Millisecond)

	c.Advance(99 * time.Millisecond)
	select {
	case <-tm.C():
		t.Fatal("timer fired early")
	default:
	}

	c.Advance(1 * time.Millisecond)
	select {
	case got := <-tm.C():
		if want := epoch.Add(100 * time.Millisecond); !got.Equal(want) {
			t.Fatalf("delivered time = %v, want %v", got, want)
		}
	default:
		t.Fatal("timer did not fire at its deadline")
	}
}

// BASE-05: callbacks observe their own deadline, not the post-Advance instant,
// and same-instant timers fire in creation order.
func TestCallbacksObserveOwnDeadlineAndOrdering(t *testing.T) {
	c := testclock.New(epoch)
	var mu sync.Mutex
	var order []string
	var seenAt []time.Time

	record := func(name string) func() {
		return func() {
			mu.Lock()
			order = append(order, name)
			seenAt = append(seenAt, c.Now())
			mu.Unlock()
		}
	}

	c.AfterFunc(20*time.Millisecond, record("b"))
	c.AfterFunc(10*time.Millisecond, record("a1"))
	c.AfterFunc(10*time.Millisecond, record("a2")) // same instant as a1, created later

	c.Advance(30 * time.Millisecond)

	if want := []string{"a1", "a2", "b"}; !equalStrs(order, want) {
		t.Fatalf("fire order = %v, want %v", order, want)
	}
	wantAt := []time.Time{
		epoch.Add(10 * time.Millisecond),
		epoch.Add(10 * time.Millisecond),
		epoch.Add(20 * time.Millisecond),
	}
	for i := range wantAt {
		if !seenAt[i].Equal(wantAt[i]) {
			t.Fatalf("callback %d saw Now()=%v, want %v", i, seenAt[i], wantAt[i])
		}
	}
	if !c.Now().Equal(epoch.Add(30 * time.Millisecond)) {
		t.Fatalf("final Now()=%v, want epoch+30ms", c.Now())
	}
}

// BASE-06: timer cancellation.
func TestTimerStop(t *testing.T) {
	c := testclock.New(epoch)
	tm := c.NewTimer(10 * time.Millisecond)
	if !tm.Stop() {
		t.Fatal("Stop on active timer returned false")
	}
	if tm.Stop() {
		t.Fatal("second Stop returned true")
	}
	c.Advance(time.Second)
	select {
	case <-tm.C():
		t.Fatal("stopped timer fired")
	default:
	}
	if c.Len() != 0 {
		t.Fatalf("stopped timer still registered: Len=%d", c.Len())
	}
}

func TestTimerReset(t *testing.T) {
	c := testclock.New(epoch)
	tm := c.NewTimer(10 * time.Millisecond)
	tm.Reset(50 * time.Millisecond)

	c.Advance(10 * time.Millisecond)
	select {
	case <-tm.C():
		t.Fatal("timer fired at the original deadline after Reset")
	default:
	}
	c.Advance(40 * time.Millisecond)
	select {
	case <-tm.C():
	default:
		t.Fatal("timer did not fire at the reset deadline")
	}
}

// BASE-07: ticker stop.
func TestTickerStop(t *testing.T) {
	c := testclock.New(epoch)
	tk := c.NewTicker(10 * time.Millisecond)

	c.Advance(25 * time.Millisecond)
	if got := c.TickCount(tk); got != 2 {
		t.Fatalf("TickCount after 25ms = %d, want 2", got)
	}
	tk.Stop()
	c.Advance(100 * time.Millisecond)
	if got := c.TickCount(tk); got != 2 {
		t.Fatalf("TickCount after Stop = %d, want 2 (no further ticks)", got)
	}
}

// BASE-04 + advisor point 2: channel delivery coalesces but TickCount reports
// every interval crossed.
func TestTickerCoalescesChannelButCountsAllIntervals(t *testing.T) {
	c := testclock.New(epoch)
	tk := c.NewTicker(10 * time.Millisecond)

	c.Advance(55 * time.Millisecond) // 5 intervals, nobody draining the channel

	if got := c.TickCount(tk); got != 5 {
		t.Fatalf("TickCount = %d, want 5", got)
	}
	// Only one tick is buffered on the channel (coalesced), like time.Ticker.
	n := 0
	for {
		select {
		case <-tk.C():
			n++
			continue
		default:
		}
		break
	}
	if n != 1 {
		t.Fatalf("drained %d ticks from channel, want 1 (coalesced)", n)
	}
}

// BASE-08: the fake-clock lock is not held during callbacks, so a callback may
// re-enter the clock (Now, NewTimer, Stop) without deadlocking.
func TestCallbackCanReenterClock(t *testing.T) {
	c := testclock.New(epoch)
	done := make(chan struct{})
	c.AfterFunc(10*time.Millisecond, func() {
		_ = c.Now()
		inner := c.NewTimer(5 * time.Millisecond)
		inner.Stop()
		close(done)
	})

	fin := make(chan struct{})
	go func() { c.Advance(20 * time.Millisecond); close(fin) }()

	select {
	case <-fin:
	case <-time.After(2 * time.Second):
		t.Fatal("Advance deadlocked: callback could not re-enter the clock")
	}
	<-done
}

func TestConcurrentNowIsRaceFree(t *testing.T) {
	c := testclock.New(epoch)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = c.Now()
				_ = c.Since(epoch)
			}
		}()
	}
	for i := 0; i < 50; i++ {
		c.Advance(time.Millisecond)
	}
	wg.Wait()
}

func TestSetBackwardsPanics(t *testing.T) {
	c := testclock.New(epoch)
	c.Advance(time.Second)
	defer func() {
		if recover() == nil {
			t.Fatal("Set to an earlier time did not panic")
		}
	}()
	c.Set(epoch)
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
