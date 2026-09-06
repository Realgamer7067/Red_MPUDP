package bounded_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/bounded"
	"github.com/Realgamer7067/Red_MPUDP/internal/testclock"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newQueue(t *testing.T, maxPkts, maxBytes int) (*bounded.ByteQueue[int], *testclock.Clock) {
	t.Helper()
	c := testclock.New(epoch)
	return bounded.NewByteQueue[int](c, maxPkts, maxBytes), c
}

// BASE-13: reject over the byte limit, leave the queue unchanged.
func TestByteQueueRejectsOverByteLimit(t *testing.T) {
	q, _ := newQueue(t, 100, 1000)
	if err := q.Enqueue(1, 600); err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	err := q.Enqueue(2, 500) // 600+500 > 1000
	if !errors.Is(err, bounded.ErrQueueFullBytes) {
		t.Fatalf("over-byte enqueue err = %v, want ErrQueueFullBytes", err)
	}
	if q.Len() != 1 || q.Bytes() != 600 {
		t.Fatalf("queue mutated by rejected enqueue: len=%d bytes=%d", q.Len(), q.Bytes())
	}
	// An item that exactly fits is accepted.
	if err := q.Enqueue(3, 400); err != nil {
		t.Fatalf("exact-fit enqueue rejected: %v", err)
	}
	if q.Bytes() != 1000 {
		t.Fatalf("bytes = %d, want 1000", q.Bytes())
	}
}

// BASE-14: reject over the packet limit.
func TestByteQueueRejectsOverPacketLimit(t *testing.T) {
	q, _ := newQueue(t, 2, 100000)
	_ = q.Enqueue(1, 10)
	_ = q.Enqueue(2, 10)
	err := q.Enqueue(3, 10)
	if !errors.Is(err, bounded.ErrQueueFullPackets) {
		t.Fatalf("over-packet enqueue err = %v, want ErrQueueFullPackets", err)
	}
	if q.Len() != 2 {
		t.Fatalf("len = %d, want 2", q.Len())
	}
}

func TestByteQueueItemLargerThanBudget(t *testing.T) {
	q, _ := newQueue(t, 10, 500)
	if err := q.Enqueue(1, 501); !errors.Is(err, bounded.ErrItemTooLarge) {
		t.Fatalf("err = %v, want ErrItemTooLarge", err)
	}
}

// BASE-16: insertion time is recorded from the injected clock.
func TestByteQueueRecordsInsertionTime(t *testing.T) {
	q, c := newQueue(t, 10, 100000)
	_ = q.Enqueue(1, 10)
	c.Advance(30 * time.Millisecond)
	_ = q.Enqueue(2, 10)

	i1, _ := q.Dequeue()
	if !i1.Enqueued.Equal(epoch) {
		t.Fatalf("item 1 Enqueued = %v, want epoch", i1.Enqueued)
	}
	i2, _ := q.Dequeue()
	if !i2.Enqueued.Equal(epoch.Add(30 * time.Millisecond)) {
		t.Fatalf("item 2 Enqueued = %v, want epoch+30ms", i2.Enqueued)
	}
}

// BASE-17: exact byte accounting across enqueue, dequeue and drop.
func TestByteQueueExactByteAccounting(t *testing.T) {
	q, c := newQueue(t, 10, 100000)
	sizes := []int{100, 250, 75, 900, 12}
	total := 0
	for i, s := range sizes {
		if err := q.Enqueue(i, s); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		total += s
		if q.Bytes() != total {
			t.Fatalf("after enqueue %d: Bytes=%d want %d", i, q.Bytes(), total)
		}
		c.Advance(time.Millisecond)
	}
	// Dequeue two.
	for k := 0; k < 2; k++ {
		item, ok := q.Dequeue()
		if !ok {
			t.Fatal("unexpected empty")
		}
		total -= item.Bytes
		if q.Bytes() != total {
			t.Fatalf("after dequeue: Bytes=%d want %d", q.Bytes(), total)
		}
	}
	// DropExpired the rest up to now.
	dropped := q.DropExpired(c.Now())
	for _, d := range dropped {
		total -= d.Bytes
	}
	if total != 0 || q.Bytes() != 0 || q.Len() != 0 {
		t.Fatalf("after DropExpired: total=%d Bytes=%d Len=%d, want all 0", total, q.Bytes(), q.Len())
	}
}

func TestByteQueueDropExpiredPartial(t *testing.T) {
	q, c := newQueue(t, 10, 100000)
	_ = q.Enqueue(1, 10) // t=epoch
	c.Advance(10 * time.Millisecond)
	_ = q.Enqueue(2, 10) // t=epoch+10ms
	c.Advance(10 * time.Millisecond)
	_ = q.Enqueue(3, 10) // t=epoch+20ms

	dropped := q.DropExpired(epoch.Add(10 * time.Millisecond))
	if len(dropped) != 2 {
		t.Fatalf("dropped %d, want 2 (items at +0 and +10ms)", len(dropped))
	}
	if q.Len() != 1 || q.Bytes() != 10 {
		t.Fatalf("remaining len=%d bytes=%d, want 1/10", q.Len(), q.Bytes())
	}
	head, _ := q.Peek()
	if head.Value != 3 {
		t.Fatalf("head = %d, want 3", head.Value)
	}
}

func TestByteQueueBadConstruction(t *testing.T) {
	c := testclock.New(epoch)
	for _, tc := range []struct{ pkts, bytes int }{{0, 10}, {10, 0}, {-1, -1}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("NewByteQueue(%d,%d) did not panic", tc.pkts, tc.bytes)
				}
			}()
			bounded.NewByteQueue[int](c, tc.pkts, tc.bytes)
		}()
	}
}
