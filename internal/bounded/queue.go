package bounded

import (
	"errors"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/clock"
)

// Errors returned by ByteQueue.Enqueue. Callers compare with errors.Is.
var (
	// ErrQueueFullPackets means the queue already holds its maximum packet
	// count.
	ErrQueueFullPackets = errors.New("bounded: queue at packet limit")
	// ErrQueueFullBytes means enqueuing the item would exceed the byte budget.
	ErrQueueFullBytes = errors.New("bounded: queue at byte limit")
	// ErrItemTooLarge means a single item is larger than the whole byte budget
	// and could never be enqueued.
	ErrItemTooLarge = errors.New("bounded: item larger than queue byte limit")
)

// QueueItem pairs a payload with the instant it was enqueued. Consumers use
// Enqueued to enforce per-replica deadlines (design §9.4) and to measure queue
// sojourn time for the pacer.
type QueueItem[T any] struct {
	Value    T
	Bytes    int
	Enqueued time.Time
}

// ByteQueue is a FIFO bounded by both a packet count and a total byte budget.
// Insertion time is recorded from an injected clock. The zero value is not
// usable; construct one with NewByteQueue. ByteQueue is not safe for concurrent
// use.
type ByteQueue[T any] struct {
	items    *Ring[QueueItem[T]]
	clk      clock.Clock
	maxBytes int
	curBytes int
}

// NewByteQueue returns a queue holding at most maxPackets items and maxBytes
// bytes total. Both limits must be positive. clk supplies enqueue timestamps.
func NewByteQueue[T any](clk clock.Clock, maxPackets, maxBytes int) *ByteQueue[T] {
	if maxPackets <= 0 || maxBytes <= 0 {
		panic("bounded: NewByteQueue limits must be > 0")
	}
	if clk == nil {
		panic("bounded: NewByteQueue clock must not be nil")
	}
	return &ByteQueue[T]{
		items:    NewRing[QueueItem[T]](maxPackets),
		clk:      clk,
		maxBytes: maxBytes,
	}
}

// Enqueue appends value, whose serialized size is sizeBytes. It returns a
// typed error and leaves the queue unchanged if either limit would be
// exceeded. sizeBytes must be > 0.
func (q *ByteQueue[T]) Enqueue(value T, sizeBytes int) error {
	if sizeBytes <= 0 {
		panic("bounded: Enqueue sizeBytes must be > 0")
	}
	if sizeBytes > q.maxBytes {
		return ErrItemTooLarge
	}
	if q.items.Full() {
		return ErrQueueFullPackets
	}
	if q.curBytes+sizeBytes > q.maxBytes {
		return ErrQueueFullBytes
	}
	q.items.Push(QueueItem[T]{Value: value, Bytes: sizeBytes, Enqueued: q.clk.Now()})
	q.curBytes += sizeBytes
	return nil
}

// Dequeue removes and returns the oldest item. ok is false if the queue is
// empty. The queue's byte count drops by exactly the item's recorded size.
func (q *ByteQueue[T]) Dequeue() (item QueueItem[T], ok bool) {
	item, ok = q.items.Pop()
	if ok {
		q.curBytes -= item.Bytes
	}
	return item, ok
}

// Peek returns the oldest item without removing it.
func (q *ByteQueue[T]) Peek() (item QueueItem[T], ok bool) {
	return q.items.Peek()
}

// DropOldest removes the oldest item and returns it, for head-drop policies.
// ok is false if the queue is empty.
func (q *ByteQueue[T]) DropOldest() (item QueueItem[T], ok bool) {
	return q.Dequeue()
}

// DropExpired removes and returns every item at the head whose enqueue time is
// at or before cutoff. Byte accounting is updated for each removed item.
func (q *ByteQueue[T]) DropExpired(cutoff time.Time) []QueueItem[T] {
	var dropped []QueueItem[T]
	for {
		head, ok := q.items.Peek()
		if !ok || head.Enqueued.After(cutoff) {
			return dropped
		}
		item, _ := q.items.Pop()
		q.curBytes -= item.Bytes
		dropped = append(dropped, item)
	}
}

// Len returns the number of queued items.
func (q *ByteQueue[T]) Len() int { return q.items.Len() }

// Bytes returns the exact total serialized size of queued items.
func (q *ByteQueue[T]) Bytes() int { return q.curBytes }

// MaxBytes returns the byte budget.
func (q *ByteQueue[T]) MaxBytes() int { return q.maxBytes }

// MaxPackets returns the packet-count limit.
func (q *ByteQueue[T]) MaxPackets() int { return q.items.Cap() }
