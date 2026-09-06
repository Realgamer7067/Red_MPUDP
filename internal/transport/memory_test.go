package transport_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/netip"
	"testing"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
)

var (
	addrA = netip.MustParseAddrPort("10.0.0.1:5000")
	addrB = netip.MustParseAddrPort("10.0.0.2:5000")
)

func pair(t *testing.T, aOpts, bOpts transport.MemoryOptions) (*transport.MemoryConn, *transport.MemoryConn) {
	t.Helper()
	a, b := transport.NewMemoryPair(addrA, addrB, aOpts, bOpts)
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

func mustWrite(t *testing.T, c *transport.MemoryConn, payload []byte, dst netip.AddrPort) {
	t.Helper()
	if err := c.WriteTo(context.Background(), payload, transport.Endpoint{AddrPort: dst}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
}

func mustRead(t *testing.T, c *transport.MemoryConn, dst []byte) (int, transport.ReceiveMeta) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	n, meta, err := c.ReadInto(ctx, dst)
	if err != nil {
		t.Fatalf("ReadInto: %v", err)
	}
	return n, meta
}

// UDP-04: the pair carries a datagram and reports source metadata.
func TestMemoryRoundTrip(t *testing.T) {
	a, b := pair(t, transport.MemoryOptions{}, transport.MemoryOptions{})

	mustWrite(t, a, []byte("hello"), addrB)
	buf := make([]byte, 64)
	n, meta := mustRead(t, b, buf)
	if got := string(buf[:n]); got != "hello" {
		t.Fatalf("payload = %q", got)
	}
	if meta.Source != addrA {
		t.Fatalf("source = %v, want %v", meta.Source, addrA)
	}
	if meta.Truncated {
		t.Fatal("complete datagram reported as truncated")
	}
	if s := b.Stats(); s.Delivered != 1 {
		t.Fatalf("stats %+v", s)
	}
}

// UDP-02: WriteTo must not retain the caller's buffer — the caller may reuse or
// release it the instant WriteTo returns.
func TestMemoryWriteDoesNotRetainCallerBuffer(t *testing.T) {
	a, b := pair(t, transport.MemoryOptions{}, transport.MemoryOptions{})

	pkt := []byte("original")
	mustWrite(t, a, pkt, addrB)
	copy(pkt, "OVERWRIT") // caller reuses its buffer immediately

	buf := make([]byte, 64)
	n, _ := mustRead(t, b, buf)
	if got := string(buf[:n]); got != "original" {
		t.Fatalf("payload = %q, want %q — WriteTo retained the caller's buffer", got, "original")
	}
}

// UDP-32 / UDP-33: an oversized datagram is dropped whole, flagged, and never
// returned in part.
func TestMemoryTruncationDropsDatagram(t *testing.T) {
	a, b := pair(t, transport.MemoryOptions{}, transport.MemoryOptions{})

	mustWrite(t, a, []byte("0123456789"), addrB)
	small := make([]byte, 4)
	for i := range small {
		small[i] = 0xAA
	}
	n, meta, err := b.ReadInto(context.Background(), small)
	if !errors.Is(err, transport.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0 — a truncated datagram must return no bytes", n)
	}
	if !meta.Truncated {
		t.Fatal("meta.Truncated not set on a truncated datagram")
	}
	for i, b := range small {
		if b != 0xAA {
			t.Fatalf("dst[%d] overwritten with %#x; a dropped datagram must not reach a parser", i, b)
		}
	}
}

func TestMemoryOversizeWriteRejected(t *testing.T) {
	a, _ := pair(t, transport.MemoryOptions{}, transport.MemoryOptions{MaxDatagram: 16})
	err := a.WriteTo(context.Background(), make([]byte, 4096), transport.Endpoint{AddrPort: addrB})
	if !errors.Is(err, transport.ErrOversize) {
		t.Fatalf("err = %v, want ErrOversize", err)
	}
}

// UDP-06: loss injection is deterministic for a given seed.
func TestMemoryLossInjection(t *testing.T) {
	const n = 200
	sender, receiver := transport.NewMemoryPair(addrA, addrB, transport.MemoryOptions{},
		transport.MemoryOptions{LossRate: 0.5, Capacity: n * 2, Rand: rand.New(rand.NewPCG(7, 9))})
	defer sender.Close()
	defer receiver.Close()

	for i := 0; i < n; i++ {
		mustWrite(t, sender, []byte{byte(i)}, addrB)
	}
	s := receiver.Stats()
	if s.Dropped == 0 || s.Dropped == n {
		t.Fatalf("Dropped = %d of %d; expected a mix at LossRate 0.5", s.Dropped, n)
	}
	if s.Dropped+s.Overflowed+uint64(len(dstDrain(t, receiver))) != n {
		t.Fatalf("datagrams unaccounted for: %+v", s)
	}
}

// dstDrain reads everything currently queued.
func dstDrain(t *testing.T, c *transport.MemoryConn) [][]byte {
	t.Helper()
	var out [][]byte
	buf := make([]byte, 64)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		n, _, err := c.ReadInto(ctx, buf)
		cancel()
		if err != nil {
			return out
		}
		out = append(out, append([]byte(nil), buf[:n]...))
	}
}

// UDP-07: duplication injection delivers a second copy.
func TestMemoryDuplicationInjection(t *testing.T) {
	sender, receiver := transport.NewMemoryPair(addrA, addrB, transport.MemoryOptions{},
		transport.MemoryOptions{DuplicateRate: 1.0})
	defer sender.Close()
	defer receiver.Close()

	mustWrite(t, sender, []byte("dup"), addrB)
	buf := make([]byte, 16)
	for i := 0; i < 2; i++ {
		n, _ := mustRead(t, receiver, buf)
		if string(buf[:n]) != "dup" {
			t.Fatalf("copy %d = %q", i, buf[:n])
		}
	}
	if s := receiver.Stats(); s.Duplicated != 1 {
		t.Fatalf("stats %+v, want Duplicated=1", s)
	}
}

// UDP-05: reorder injection makes a later datagram arrive first, and loses
// nothing.
func TestMemoryReorderInjection(t *testing.T) {
	sender, receiver := transport.NewMemoryPair(addrA, addrB, transport.MemoryOptions{},
		transport.MemoryOptions{ReorderRate: 1.0})
	defer sender.Close()
	defer receiver.Close()

	mustWrite(t, sender, []byte{1}, addrB) // held back
	mustWrite(t, sender, []byte{2}, addrB) // overtakes, then releases the held one

	buf := make([]byte, 8)
	var order []byte
	for i := 0; i < 2; i++ {
		n, _ := mustRead(t, receiver, buf)
		order = append(order, buf[:n]...)
	}
	if len(order) != 2 || order[0] != 2 || order[1] != 1 {
		t.Fatalf("delivery order = %v, want [2 1]", order)
	}
	if s := receiver.Stats(); s.Reordered != 1 {
		t.Fatalf("stats %+v, want Reordered=1", s)
	}
}

// UDP-08: the receive queue is bounded; overflow is dropped and counted, never
// buffered without limit.
func TestMemoryBoundedCapacity(t *testing.T) {
	const cap = 4
	sender, receiver := transport.NewMemoryPair(addrA, addrB, transport.MemoryOptions{},
		transport.MemoryOptions{Capacity: cap})
	defer sender.Close()
	defer receiver.Close()

	for i := 0; i < cap*4; i++ {
		mustWrite(t, sender, []byte{byte(i)}, addrB)
	}
	s := receiver.Stats()
	if s.Overflowed != uint64(cap*4-cap) {
		t.Fatalf("Overflowed = %d, want %d", s.Overflowed, cap*4-cap)
	}
	// Exactly cap datagrams are readable, then the queue is empty.
	buf := make([]byte, 8)
	for i := 0; i < cap; i++ {
		mustRead(t, receiver, buf)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := receiver.ReadInto(ctx, buf); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded — queue held more than %d", err, cap)
	}
}

func TestMemoryPathError(t *testing.T) {
	a, _ := pair(t, transport.MemoryOptions{}, transport.MemoryOptions{})
	want := transport.PathError{Peer: addrB, MTU: 1300, Local: true}
	a.InjectPathError(want)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := a.ReadPathError(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("PathError = %+v, want %+v", got, want)
	}
}

// UDP-39 / UDP-40: Close unblocks every parked caller and is idempotent.
func TestMemoryCloseUnblocksAndIsIdempotent(t *testing.T) {
	a, _ := transport.NewMemoryPair(addrA, addrB, transport.MemoryOptions{}, transport.MemoryOptions{})

	readErr := make(chan error, 1)
	peErr := make(chan error, 1)
	go func() { _, _, err := a.ReadInto(context.Background(), make([]byte, 64)); readErr <- err }()
	go func() { _, err := a.ReadPathError(context.Background()); peErr <- err }()
	time.Sleep(20 * time.Millisecond)

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	for name, ch := range map[string]chan error{"ReadInto": readErr, "ReadPathError": peErr} {
		select {
		case err := <-ch:
			if !errors.Is(err, transport.ErrClosed) {
				t.Fatalf("%s returned %v, want ErrClosed", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Close left %s blocked", name)
		}
	}
}

func TestMemoryContextCancelUnblocksRead(t *testing.T) {
	a, _ := pair(t, transport.MemoryOptions{}, transport.MemoryOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := a.ReadInto(ctx, make([]byte, 64)); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not unblock ReadInto")
	}
}

// The injected path-error queue is bounded and never blocks: at capacity the
// oldest event is dropped and counted, mirroring the real sockets.
func TestMemoryInjectedPathErrorsAreBounded(t *testing.T) {
	a, _ := pair(t, transport.MemoryOptions{}, transport.MemoryOptions{})

	const over = transport.MaxInjectedPathErrors + 8
	for i := 0; i < over; i++ {
		a.InjectPathError(transport.PathError{MTU: 1000 + i})
	}
	if s := a.Stats(); s.PathErrDropped != over-transport.MaxInjectedPathErrors {
		t.Fatalf("PathErrDropped = %d, want %d", s.PathErrDropped, over-transport.MaxInjectedPathErrors)
	}
	// The queue kept the newest entries, so the first one read is not the
	// first one injected.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pe, err := a.ReadPathError(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pe.MTU != 1000+(over-transport.MaxInjectedPathErrors) {
		t.Fatalf("oldest surviving event MTU = %d, want %d",
			pe.MTU, 1000+(over-transport.MaxInjectedPathErrors))
	}
}

// After Close an injected event is discarded rather than queued, and every
// method reports ErrClosed ahead of a cancelled context or an oversized write.
func TestMemoryAfterCloseIsErrClosed(t *testing.T) {
	a, _ := transport.NewMemoryPair(addrA, addrB,
		transport.MemoryOptions{MaxDatagram: 32}, transport.MemoryOptions{})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a.InjectPathError(transport.PathError{MTU: 1200}) // post-close: discarded

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	checks := map[string]func() error{
		"ReadInto":           func() error { _, _, err := a.ReadInto(context.Background(), make([]byte, 8)); return err },
		"ReadInto cancelled": func() error { _, _, err := a.ReadInto(cancelled, make([]byte, 8)); return err },
		"WriteTo":            func() error { return a.WriteTo(context.Background(), []byte("x"), transport.Endpoint{AddrPort: addrB}) },
		"WriteTo oversized": func() error {
			return a.WriteTo(context.Background(), make([]byte, 4096), transport.Endpoint{AddrPort: addrB})
		},
		"WriteTo cancelled":       func() error { return a.WriteTo(cancelled, []byte("x"), transport.Endpoint{AddrPort: addrB}) },
		"ReadPathError":           func() error { _, err := a.ReadPathError(context.Background()); return err },
		"ReadPathError cancelled": func() error { _, err := a.ReadPathError(cancelled); return err },
	}
	for name, fn := range checks {
		if err := fn(); !errors.Is(err, transport.ErrClosed) {
			t.Errorf("%s after Close = %v, want ErrClosed", name, err)
		}
	}
}

// Companion to TestMemoryAfterCloseIsErrClosed: that test injects only after
// Close, so it never exercises a non-empty queue. Queue an event first, confirm
// it is really there, then close and require ErrClosed rather than the pending
// event.
func TestMemoryErrClosedOutranksAQueuedPathError(t *testing.T) {
	a, _ := transport.NewMemoryPair(addrA, addrB, transport.MemoryOptions{}, transport.MemoryOptions{})

	// The event is genuinely queued: while open, it reads back.
	a.InjectPathError(transport.PathError{Peer: addrB, MTU: 1300})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	pe, err := a.ReadPathError(ctx)
	cancel()
	if err != nil || pe.MTU != 1300 {
		t.Fatalf("open conn ReadPathError = (%+v, %v)", pe, err)
	}

	// Re-queue, then close with it still pending.
	a.InjectPathError(transport.PathError{Peer: addrB, MTU: 1300})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadPathError(context.Background()); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("ReadPathError with an event still queued = %v, want ErrClosed", err)
	}
}
