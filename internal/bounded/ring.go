// Package bounded provides fixed-capacity data structures for RED_MPUDP data
// paths. Every structure has a hard upper bound fixed at construction; none
// ever grows. This is a deliberate anti-amplification and anti-bufferbloat
// property: an attacker who floods a path cannot make the receiver allocate
// without limit, and a slow path cannot accumulate unbounded latency.
package bounded

// Ring is a fixed-capacity circular buffer (FIFO). The zero value is not
// usable; construct one with NewRing. Ring is not safe for concurrent use.
type Ring[T any] struct {
	buf  []T
	head int // index of the oldest element
	n    int // number of elements currently stored
}

// NewRing returns a Ring that holds at most capacity elements. capacity must be
// greater than zero.
func NewRing[T any](capacity int) *Ring[T] {
	if capacity <= 0 {
		panic("bounded: NewRing capacity must be > 0")
	}
	return &Ring[T]{buf: make([]T, capacity)}
}

// Cap returns the fixed capacity.
func (r *Ring[T]) Cap() int { return len(r.buf) }

// Len returns the number of elements currently stored.
func (r *Ring[T]) Len() int { return r.n }

// Empty reports whether the ring holds no elements.
func (r *Ring[T]) Empty() bool { return r.n == 0 }

// Full reports whether the ring is at capacity.
func (r *Ring[T]) Full() bool { return r.n == len(r.buf) }

// Push appends v. It returns false without modifying the ring if it is full.
func (r *Ring[T]) Push(v T) bool {
	if r.n == len(r.buf) {
		return false
	}
	r.buf[(r.head+r.n)%len(r.buf)] = v
	r.n++
	return true
}

// PushEvict appends v. If the ring is full it first drops the oldest element
// and returns it with evicted=true.
func (r *Ring[T]) PushEvict(v T) (dropped T, evicted bool) {
	if r.n == len(r.buf) {
		dropped = r.buf[r.head]
		var zero T
		r.buf[r.head] = zero
		r.head = (r.head + 1) % len(r.buf)
		r.n--
		evicted = true
	}
	r.buf[(r.head+r.n)%len(r.buf)] = v
	r.n++
	return dropped, evicted
}

// Pop removes and returns the oldest element. ok is false if the ring is empty.
func (r *Ring[T]) Pop() (v T, ok bool) {
	if r.n == 0 {
		return v, false
	}
	v = r.buf[r.head]
	var zero T
	r.buf[r.head] = zero // release reference so GC can reclaim
	r.head = (r.head + 1) % len(r.buf)
	r.n--
	return v, true
}

// Peek returns the oldest element without removing it. ok is false if empty.
func (r *Ring[T]) Peek() (v T, ok bool) {
	if r.n == 0 {
		return v, false
	}
	return r.buf[r.head], true
}

// Reset drops all elements, zeroing storage so references are released.
func (r *Ring[T]) Reset() {
	var zero T
	for i := range r.buf {
		r.buf[i] = zero
	}
	r.head = 0
	r.n = 0
}
