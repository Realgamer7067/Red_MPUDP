package clock

import "time"

// System is the production Clock. It delegates to the time package, so all
// times carry a monotonic reading and timers use the runtime timer wheel.
type System struct{}

var _ Clock = System{}

// Now returns time.Now().
func (System) Now() time.Time { return time.Now() }

// Since returns time.Since(t).
func (System) Since(t time.Time) time.Duration { return time.Since(t) }

// NewTimer wraps time.NewTimer.
func (System) NewTimer(d time.Duration) Timer {
	return sysTimer{time.NewTimer(d)}
}

// AfterFunc wraps time.AfterFunc. The returned Timer has a nil channel.
func (System) AfterFunc(d time.Duration, f func()) Timer {
	return sysTimer{time.AfterFunc(d, f)}
}

// NewTicker wraps time.NewTicker. It panics if d <= 0, matching time.NewTicker.
func (System) NewTicker(d time.Duration) Ticker {
	return sysTicker{time.NewTicker(d)}
}

type sysTimer struct{ t *time.Timer }

func (s sysTimer) C() <-chan time.Time        { return s.t.C }
func (s sysTimer) Stop() bool                 { return s.t.Stop() }
func (s sysTimer) Reset(d time.Duration) bool { return s.t.Reset(d) }

type sysTicker struct{ t *time.Ticker }

func (s sysTicker) C() <-chan time.Time   { return s.t.C }
func (s sysTicker) Stop()                 { s.t.Stop() }
func (s sysTicker) Reset(d time.Duration) { s.t.Reset(d) }
