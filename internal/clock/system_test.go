package clock_test

import (
	"testing"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/clock"
)

func TestSystemNowIsMonotonicAndAdvances(t *testing.T) {
	var c clock.Clock = clock.System{}
	a := c.Now()
	time.Sleep(2 * time.Millisecond)
	b := c.Now()
	if !b.After(a) {
		t.Fatalf("System.Now did not advance: %v then %v", a, b)
	}
	if c.Since(a) <= 0 {
		t.Fatalf("System.Since returned non-positive: %v", c.Since(a))
	}
}

func TestSystemTimerFires(t *testing.T) {
	c := clock.System{}
	tm := c.NewTimer(5 * time.Millisecond)
	select {
	case <-tm.C():
	case <-time.After(time.Second):
		t.Fatal("System timer never fired")
	}
	if tm.Stop() {
		t.Fatal("Stop on a fired timer returned true")
	}
}

func TestSystemAfterFuncAndStop(t *testing.T) {
	c := clock.System{}
	fired := make(chan struct{}, 1)
	tm := c.AfterFunc(time.Hour, func() { fired <- struct{}{} })
	if !tm.Stop() {
		t.Fatal("Stop on a pending AfterFunc returned false")
	}
	if tm.C() != nil {
		t.Fatal("AfterFunc timer should have a nil channel")
	}
	select {
	case <-fired:
		t.Fatal("stopped AfterFunc still ran")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestSystemTicker(t *testing.T) {
	c := clock.System{}
	tk := c.NewTicker(2 * time.Millisecond)
	defer tk.Stop()
	for i := 0; i < 3; i++ {
		select {
		case <-tk.C():
		case <-time.After(time.Second):
			t.Fatalf("System ticker missed tick %d", i)
		}
	}
}
