package packetbuf_test

import (
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/packetbuf"
)

// BASE-18: size classes cover the largest datagram the design permits.
func TestSizeClassesCoverCeiling(t *testing.T) {
	p := packetbuf.NewPool()
	b := p.Get(packetbuf.MaxOuterDatagram)
	defer b.Release()
	if len(b.Bytes()) != packetbuf.MaxOuterDatagram {
		t.Fatalf("len = %d, want %d", len(b.Bytes()), packetbuf.MaxOuterDatagram)
	}
	if b.Cap() < packetbuf.MaxOuterDatagram {
		t.Fatalf("class cap %d < MaxOuterDatagram %d", b.Cap(), packetbuf.MaxOuterDatagram)
	}
	if packetbuf.MaxDataDatagram > packetbuf.MaxOuterDatagram ||
		packetbuf.MaxHandshakeDatagram > packetbuf.MaxOuterDatagram {
		t.Fatal("a sub-ceiling constant exceeds MaxOuterDatagram")
	}
}

func TestGetPicksSmallestSufficientClass(t *testing.T) {
	p := packetbuf.NewPool()
	cases := []struct {
		n       int
		wantCap int
	}{
		{1, 128},
		{128, 128},
		{129, packetbuf.MaxHandshakeDatagram},
		{packetbuf.MaxHandshakeDatagram, packetbuf.MaxHandshakeDatagram},
		{packetbuf.MaxHandshakeDatagram + 1, packetbuf.MaxOuterDatagram},
		{packetbuf.MaxOuterDatagram, packetbuf.MaxOuterDatagram},
	}
	for _, tc := range cases {
		b := p.Get(tc.n)
		if b.Cap() != tc.wantCap {
			t.Errorf("Get(%d) cap = %d, want %d", tc.n, b.Cap(), tc.wantCap)
		}
		if len(b.Bytes()) != tc.n {
			t.Errorf("Get(%d) len = %d, want %d", tc.n, len(b.Bytes()), tc.n)
		}
		b.Release()
	}
}

func TestGetOversizePanics(t *testing.T) {
	p := packetbuf.NewPool()
	defer func() {
		if recover() == nil {
			t.Fatal("Get past the largest class did not panic")
		}
	}()
	p.Get(packetbuf.MaxOuterDatagram + 1)
}

// BASE-21: a buffer is reusable after Release (round-trips through the pool).
func TestReleaseReturnsBufferToPool(t *testing.T) {
	p := packetbuf.NewPool()
	b1 := p.Get(500)
	data := b1.Bytes()
	for i := range data {
		data[i] = 0xAA
	}
	b1.Release()

	b2 := p.Get(500)
	defer b2.Release()
	// Same size class; length reset to the new request.
	if len(b2.Bytes()) != 500 {
		t.Fatalf("recycled buffer len = %d, want 500", len(b2.Bytes()))
	}
}

func TestResize(t *testing.T) {
	p := packetbuf.NewPool()
	b := p.Get(10)
	defer b.Release()
	b.Resize(b.Cap())
	if len(b.Bytes()) != b.Cap() {
		t.Fatalf("after Resize to cap, len = %d, want %d", len(b.Bytes()), b.Cap())
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Resize past cap did not panic")
		}
	}()
	b.Resize(b.Cap() + 1)
}

// BASE-19: concurrent Get/Release from many goroutines is race-free and every
// buffer is independently owned.
func TestConcurrentGetReleaseNoAliasing(t *testing.T) {
	p := packetbuf.NewPool()
	const workers, iters = 16, 400
	done := make(chan struct{}, workers)
	for w := 0; w < workers; w++ {
		go func(id byte) {
			for i := 0; i < iters; i++ {
				b := p.Get(packetbuf.MaxDataDatagram)
				buf := b.Bytes()
				for j := range buf {
					buf[j] = id
				}
				for j := range buf {
					if buf[j] != id {
						t.Errorf("worker %d: buffer aliased, saw %d", id, buf[j])
						break
					}
				}
				b.Release()
			}
			done <- struct{}{}
		}(byte(w + 1))
	}
	for w := 0; w < workers; w++ {
		<-done
	}
}
