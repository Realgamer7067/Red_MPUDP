// Package testclock provides a deterministic [clock.Clock] for tests.
//
// Time never moves on its own. It advances only when a test calls
// [Clock.Advance] or [Clock.Set]. When time advances, every timer whose
// deadline is now in the past fires, in strict deadline order, with ties broken
// by creation order. The clock's Now is stepped to each timer's deadline before
// that timer fires, so a callback that reads the clock sees its own deadline,
// not the post-Advance instant.
//
// The clock's internal lock is never held while a timer callback runs or while
// a value is delivered on a timer channel, so callbacks may freely call back
// into the clock (Now, NewTimer, Reset, Stop). Callbacks must not call Advance
// or Set re-entrantly; doing so deadlocks.
package testclock

import (
	"sort"
	"sync"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/clock"
)

// Clock is a manually advanced [clock.Clock].
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	nextID uint64
	timers map[uint64]*entry
}

type entry struct {
	id       uint64
	deadline time.Time
	period   time.Duration // 0 for a one-shot timer
	seq      uint64        // creation order, for deterministic tie-breaking
	active   bool
	ticks    int // times this ticker has fired (period > 0 only)

	ch chan time.Time // non-nil for NewTimer / NewTicker
	fn func()         // non-nil for AfterFunc
}

// New returns a Clock whose current time is start.
func New(start time.Time) *Clock {
	return &Clock{now: start, timers: make(map[uint64]*entry)}
}

var _ clock.Clock = (*Clock)(nil)

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Since returns c.Now().Sub(t).
func (c *Clock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// NewTimer returns a timer that fires once, d after the current fake time.
func (c *Clock) NewTimer(d time.Duration) clock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.add(d, 0)
	e.ch = make(chan time.Time, 1)
	return &timer{c: c, id: e.id, ch: e.ch}
}

// AfterFunc schedules f to run d after the current fake time. f runs during a
// later Advance or Set call, with the clock lock released.
func (c *Clock) AfterFunc(d time.Duration, f func()) clock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.add(d, 0)
	e.fn = f
	return &timer{c: c, id: e.id}
}

// NewTicker returns a ticker firing every d. It panics if d <= 0.
func (c *Clock) NewTicker(d time.Duration) clock.Ticker {
	if d <= 0 {
		panic("testclock: non-positive interval for NewTicker")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.add(d, d)
	e.ch = make(chan time.Time, 1)
	return &ticker{c: c, id: e.id, ch: e.ch}
}

// add creates an entry and registers it. Caller holds c.mu.
func (c *Clock) add(d, period time.Duration) *entry {
	c.nextID++
	e := &entry{
		id:       c.nextID,
		deadline: c.now.Add(d),
		period:   period,
		seq:      c.nextID,
		active:   true,
	}
	c.timers[e.id] = e
	return e
}

// Advance moves the clock forward by d, firing every timer that becomes due.
// d must not be negative.
func (c *Clock) Advance(d time.Duration) {
	if d < 0 {
		panic("testclock: negative duration for Advance")
	}
	c.Set(c.Now().Add(d))
}

// Set moves the clock to t, firing every timer that becomes due. t must not be
// before the current time.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	if t.Before(c.now) {
		c.mu.Unlock()
		panic("testclock: Set to a time before the current time")
	}

	for {
		e := c.earliestDue(t)
		if e == nil {
			break
		}
		fireTime := e.deadline
		c.now = fireTime
		if e.period > 0 {
			e.deadline = e.deadline.Add(e.period)
			e.ticks++
		} else {
			e.active = false
			delete(c.timers, e.id)
		}
		ch, fn := e.ch, e.fn

		c.mu.Unlock()
		if fn != nil {
			fn()
		} else if ch != nil {
			select {
			case ch <- fireTime:
			default: // coalesce, exactly like time.Ticker / time.Timer
			}
		}
		c.mu.Lock()
	}

	c.now = t
	c.mu.Unlock()
}

// earliestDue returns the active timer with the smallest (deadline, seq) whose
// deadline is <= target, or nil. Caller holds c.mu.
func (c *Clock) earliestDue(target time.Time) *entry {
	var due []*entry
	for _, e := range c.timers {
		if e.active && !e.deadline.After(target) {
			due = append(due, e)
		}
	}
	if len(due) == 0 {
		return nil
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].deadline.Equal(due[j].deadline) {
			return due[i].deadline.Before(due[j].deadline)
		}
		return due[i].seq < due[j].seq
	})
	return due[0]
}

// Len returns the number of active timers and tickers. Useful for leak checks.
func (c *Clock) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.timers {
		if e.active {
			n++
		}
	}
	return n
}

// TickCount returns the number of times tk has fired since creation, counting
// every interval crossed by Advance/Set even when channel delivery coalesced.
// tk must have come from this clock's NewTicker.
func (c *Clock) TickCount(tk clock.Ticker) int {
	t, ok := tk.(*ticker)
	if !ok || t.c != c {
		panic("testclock: TickCount on a ticker from a different clock")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.timers[t.id]; ok {
		return e.ticks
	}
	return 0
}

// timer implements clock.Timer against a parent Clock entry.
type timer struct {
	c  *Clock
	id uint64
	ch chan time.Time // stable handle; nil for AfterFunc
}

func (t *timer) C() <-chan time.Time { return t.ch }

func (t *timer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	e, ok := t.c.timers[t.id]
	if !ok || !e.active {
		return false
	}
	e.active = false
	delete(t.c.timers, t.id)
	return true
}

func (t *timer) Reset(d time.Duration) bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	e, ok := t.c.timers[t.id]
	if !ok {
		// Timer already fired (one-shot) and was removed; recreate it so Reset
		// behaves like time.Timer.Reset on a fired timer. Reuse the same
		// channel so a caller holding t.C() still receives.
		t.c.nextID++
		ne := &entry{id: t.id, deadline: t.c.now.Add(d), seq: t.c.nextID, active: true, ch: t.ch}
		t.c.timers[t.id] = ne
		return false
	}
	wasActive := e.active
	e.deadline = t.c.now.Add(d)
	e.active = true
	return wasActive
}

// ticker implements clock.Ticker against a parent Clock entry.
type ticker struct {
	c  *Clock
	id uint64
	ch chan time.Time
}

func (t *ticker) C() <-chan time.Time { return t.ch }

func (t *ticker) Stop() {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	if e, ok := t.c.timers[t.id]; ok {
		e.active = false
	}
}

func (t *ticker) Reset(d time.Duration) {
	if d <= 0 {
		panic("testclock: non-positive interval for Ticker.Reset")
	}
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	if e, ok := t.c.timers[t.id]; ok {
		e.period = d
		e.deadline = t.c.now.Add(d)
		e.active = true
	}
}
