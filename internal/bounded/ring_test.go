package bounded_test

import (
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/bounded"
)

// BASE-09: empty behaviour.
func TestRingEmpty(t *testing.T) {
	r := bounded.NewRing[int](4)
	if !r.Empty() || r.Full() || r.Len() != 0 || r.Cap() != 4 {
		t.Fatalf("fresh ring state wrong: empty=%v full=%v len=%d cap=%d", r.Empty(), r.Full(), r.Len(), r.Cap())
	}
	if _, ok := r.Pop(); ok {
		t.Fatal("Pop on empty ring returned ok")
	}
	if _, ok := r.Peek(); ok {
		t.Fatal("Peek on empty ring returned ok")
	}
}

// BASE-10: full behaviour, Push rejects without corrupting.
func TestRingFull(t *testing.T) {
	r := bounded.NewRing[int](3)
	for i := 1; i <= 3; i++ {
		if !r.Push(i) {
			t.Fatalf("Push(%d) rejected before full", i)
		}
	}
	if !r.Full() {
		t.Fatal("ring not reporting full at capacity")
	}
	if r.Push(99) {
		t.Fatal("Push on full ring returned true")
	}
	if r.Len() != 3 {
		t.Fatalf("Len changed after rejected Push: %d", r.Len())
	}
	// FIFO order preserved.
	for i := 1; i <= 3; i++ {
		v, ok := r.Pop()
		if !ok || v != i {
			t.Fatalf("Pop = (%d,%v), want (%d,true)", v, ok, i)
		}
	}
}

// BASE-11: wraparound - repeated push/pop cycles across the buffer boundary.
func TestRingWraparound(t *testing.T) {
	r := bounded.NewRing[int](3)
	next := 0
	popped := 0
	for cycle := 0; cycle < 100; cycle++ {
		for !r.Full() {
			r.Push(next)
			next++
		}
		// drain two, forcing head to wrap
		for k := 0; k < 2; k++ {
			v, ok := r.Pop()
			if !ok || v != popped {
				t.Fatalf("cycle %d: Pop = (%d,%v), want (%d,true)", cycle, v, ok, popped)
			}
			popped++
		}
	}
}

// BASE-12: PushEvict drops the oldest when full and reports it.
func TestRingPushEvict(t *testing.T) {
	r := bounded.NewRing[int](3)
	r.Push(1)
	r.Push(2)
	r.Push(3)
	dropped, evicted := r.PushEvict(4)
	if !evicted || dropped != 1 {
		t.Fatalf("PushEvict on full = (%d,%v), want (1,true)", dropped, evicted)
	}
	if r.Len() != 3 {
		t.Fatalf("Len after PushEvict = %d, want 3", r.Len())
	}
	got := []int{}
	for {
		v, ok := r.Pop()
		if !ok {
			break
		}
		got = append(got, v)
	}
	if len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("contents after PushEvict = %v, want [2 3 4]", got)
	}

	// PushEvict when not full behaves like Push.
	r2 := bounded.NewRing[int](3)
	if _, evicted := r2.PushEvict(7); evicted {
		t.Fatal("PushEvict on non-full ring reported an eviction")
	}
}

func TestRingZeroCapacityPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewRing(0) did not panic")
		}
	}()
	bounded.NewRing[int](0)
}

// Pop must clear the slot so the ring does not pin references (matters for
// *packetbuf.Buffer and other pointer payloads).
func TestRingPopReleasesReference(t *testing.T) {
	r := bounded.NewRing[*int](2)
	x := new(int)
	r.Push(x)
	if _, ok := r.Pop(); !ok {
		t.Fatal("Pop failed")
	}
	// Not directly observable without unsafe; this at least exercises the path
	// under -race and the zeroing code in Pop/Reset.
	r.Reset()
	if !r.Empty() {
		t.Fatal("Reset left ring non-empty")
	}
}
