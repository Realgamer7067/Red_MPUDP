// Package clock abstracts time so RED_MPUDP state machines (session, path
// actor, health, congestion, scheduler) can be driven deterministically in
// tests without real delays or wall-clock sleeps.
//
// Production code uses [System]. Tests use
// github.com/Realgamer7067/Red_MPUDP/internal/testclock, whose Clock advances
// only when Advance is called and fires timers in deadline order.
package clock

import "time"

// Clock is the injection point. Every component that needs the current time or
// a timer takes a Clock rather than calling the time package directly.
type Clock interface {
	// Now returns the current time. For System this is monotonic.
	Now() time.Time

	// Since is shorthand for c.Now().Sub(t).
	Since(t time.Time) time.Duration

	// NewTimer returns a Timer that delivers one value on its channel after d.
	NewTimer(d time.Duration) Timer

	// AfterFunc schedules f to run after d and returns a Timer whose Stop
	// cancels it. For System, f runs in its own goroutine. For the test clock,
	// f runs during Advance with the clock's lock released.
	AfterFunc(d time.Duration, f func()) Timer

	// NewTicker returns a Ticker that delivers a value on its channel every d.
	// d must be greater than zero. Channel delivery coalesces (buffer of one,
	// non-blocking send) exactly like time.Ticker; the test clock exposes the
	// true tick count separately via its TickCount method.
	NewTicker(d time.Duration) Ticker
}

// Timer is a one-shot timer. Semantics match time.Timer. A Timer is not safe
// for concurrent use by multiple goroutines.
type Timer interface {
	// C is the channel on which the time is delivered. It is nil for a Timer
	// created by AfterFunc.
	C() <-chan time.Time
	// Stop prevents the Timer from firing, returning true if it was still
	// active. Matches time.Timer.Stop.
	Stop() bool
	// Reset changes the timer to expire after d, returning true if it was
	// active. Matches time.Timer.Reset.
	Reset(d time.Duration) bool
}

// Ticker delivers ticks on a channel at intervals. Semantics match time.Ticker.
type Ticker interface {
	// C is the channel on which ticks are delivered.
	C() <-chan time.Time
	// Stop turns off the ticker. It does not close the channel.
	Stop()
	// Reset changes the ticker interval to d, which must be greater than zero.
	Reset(d time.Duration)
}
