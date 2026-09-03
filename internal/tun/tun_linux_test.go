//go:build linux

package tun

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeRW stands in for the *os.File wrapping /dev/net/tun.
type fakeRW struct {
	mu       sync.Mutex
	queue    [][]byte
	readErr  error
	writeErr error
	shortN   int
	written  [][]byte
	closeN   int
	closeErr error
	dlCh     chan struct{}
}

func newFakeRW() *fakeRW { return &fakeRW{dlCh: make(chan struct{})} }

func (f *fakeRW) enqueue(p []byte) {
	f.mu.Lock()
	f.queue = append(f.queue, append([]byte(nil), p...))
	f.mu.Unlock()
}

func (f *fakeRW) Read(p []byte) (int, error) {
	f.mu.Lock()
	if f.readErr != nil {
		e := f.readErr
		f.mu.Unlock()
		return 0, e
	}
	if len(f.queue) > 0 {
		q := f.queue[0]
		f.queue = f.queue[1:]
		f.mu.Unlock()
		return copy(p, q), nil
	}
	ch := f.dlCh
	f.mu.Unlock()
	<-ch
	return 0, os.ErrDeadlineExceeded
}

func (f *fakeRW) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	if f.shortN > 0 {
		f.written = append(f.written, append([]byte(nil), p[:f.shortN]...))
		return f.shortN, nil
	}
	f.written = append(f.written, append([]byte(nil), p...))
	return len(p), nil
}

func (f *fakeRW) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeN++
	return f.closeErr
}

func (f *fakeRW) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !t.IsZero() && t.Before(time.Now()) {
		select {
		case <-f.dlCh:
		default:
			close(f.dlCh)
		}
	}
	return nil
}

type fakeLink struct {
	mtuSet   []int
	setErr   error
	closeN   int
	closeErr error
}

func (l *fakeLink) setMTU(mtu int) error {
	if l.setErr != nil {
		return l.setErr
	}
	l.mtuSet = append(l.mtuSet, mtu)
	return nil
}
func (l *fakeLink) close() error { l.closeN++; return l.closeErr }

func newDevice(rw *fakeRW, link *fakeLink, mtu, maxPacket int) *linuxDevice {
	d := &linuxDevice{
		rw:        rw,
		name:      "red0",
		index:     5,
		addr:      netip.MustParsePrefix("10.9.0.2/24"),
		maxPacket: maxPacket,
		link:      link,
	}
	d.mtu.Store(int64(mtu))
	return d
}

func TestLinuxReadPacket(t *testing.T) {
	rw := newFakeRW()
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)
	rw.enqueue(make([]byte, 100))

	buf := make([]byte, DefaultMTU+64)
	n, err := d.ReadPacket(context.Background(), buf)
	if err != nil || n != 100 {
		t.Fatalf("ReadPacket = (%d, %v)", n, err)
	}
	if s := d.Stats(); s.RxPackets != 1 || s.RxBytes != 100 {
		t.Fatalf("stats %+v", s)
	}
}

// TUN-20: an oversize packet is dropped and counted, never returned.
func TestLinuxReadOversize(t *testing.T) {
	rw := newFakeRW()
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)
	rw.enqueue(make([]byte, DefaultMTU+50))

	buf := make([]byte, DefaultMTU+200)
	if _, err := d.ReadPacket(context.Background(), buf); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("err = %v, want ErrPacketTooLarge", err)
	}
	s := d.Stats()
	if s.RxOversize != 1 || s.RxPackets != 0 {
		t.Fatalf("stats %+v", s)
	}
}

func TestLinuxReadBufferTooSmall(t *testing.T) {
	d := newDevice(newFakeRW(), &fakeLink{}, DefaultMTU, DefaultMTU)
	if _, err := d.ReadPacket(context.Background(), make([]byte, DefaultMTU)); !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("err = %v, want ErrBufferTooSmall", err)
	}
}

// TUN-19: a cancelled context unblocks the read with ctx.Err(), and it is not
// counted as a descriptor error.
func TestLinuxReadContextCancel(t *testing.T) {
	rw := newFakeRW()
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.ReadPacket(ctx, make([]byte, DefaultMTU+64))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadPacket did not unblock on cancel")
	}
	if d.Stats().RxErrors != 0 {
		t.Fatalf("cancellation counted as a descriptor error")
	}
}

func TestLinuxReadPermanentError(t *testing.T) {
	rw := newFakeRW()
	rw.readErr = errors.New("EIO")
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)

	if _, err := d.ReadPacket(context.Background(), make([]byte, DefaultMTU+64)); err == nil {
		t.Fatal("expected an error")
	}
	if d.Stats().RxErrors != 1 {
		t.Fatalf("RxErrors = %d", d.Stats().RxErrors)
	}
}

func TestLinuxReadWriteAfterClose(t *testing.T) {
	d := newDevice(newFakeRW(), &fakeLink{}, DefaultMTU, DefaultMTU)
	_ = d.Close()
	if _, err := d.ReadPacket(context.Background(), make([]byte, DefaultMTU+64)); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
	if _, err := d.WritePacket([]byte{1, 2, 3}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
}

func TestLinuxWritePacket(t *testing.T) {
	rw := newFakeRW()
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)
	pkt := []byte{0x45, 0, 0, 40}
	n, err := d.WritePacket(pkt)
	if err != nil || n != 4 {
		t.Fatalf("WritePacket = (%d, %v)", n, err)
	}
	if d.Stats().TxPackets != 1 {
		t.Fatalf("TxPackets = %d", d.Stats().TxPackets)
	}
}

// TUN-22: a short kernel write is an error.
func TestLinuxWriteShort(t *testing.T) {
	rw := newFakeRW()
	rw.shortN = 2
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)
	if _, err := d.WritePacket([]byte{1, 2, 3, 4}); !errors.Is(err, ErrShortWrite) {
		t.Fatalf("err = %v, want ErrShortWrite", err)
	}
	if s := d.Stats(); s.TxShort != 1 || s.TxPackets != 0 {
		t.Fatalf("stats %+v", s)
	}
}

// TUN-08: Close closes the descriptor exactly once.
func TestLinuxCloseOnce(t *testing.T) {
	rw := newFakeRW()
	link := &fakeLink{}
	d := newDevice(rw, link, DefaultMTU, DefaultMTU)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if rw.closeN != 1 {
		t.Fatalf("descriptor closed %d times, want 1", rw.closeN)
	}
	if link.closeN != 1 {
		t.Fatalf("netlink closed %d times, want 1", link.closeN)
	}
}

// TUN-16 / TUN-17: MTU reduce always applies; increase needs authentication.
func TestLinuxSetMTU(t *testing.T) {
	rw := newFakeRW()
	link := &fakeLink{}
	d := newDevice(rw, link, DefaultMTU, DefaultMTU)

	if err := d.SetMTU(DefaultMTU-40, false); err != nil {
		t.Fatalf("reduce: %v", err)
	}
	if d.MTU() != DefaultMTU-40 || len(link.mtuSet) != 1 || link.mtuSet[0] != DefaultMTU-40 {
		t.Fatalf("reduce not applied: mtu=%d set=%v", d.MTU(), link.mtuSet)
	}
	if err := d.SetMTU(DefaultMTU, false); !errors.Is(err, ErrMTUIncrease) {
		t.Fatalf("unauthenticated increase: %v", err)
	}
	if len(link.mtuSet) != 1 {
		t.Fatalf("netlink touched for a rejected increase: %v", link.mtuSet)
	}
	if err := d.SetMTU(DefaultMTU, true); err != nil {
		t.Fatalf("authenticated increase: %v", err)
	}
	if d.MTU() != DefaultMTU {
		t.Fatalf("mtu = %d after authenticated increase", d.MTU())
	}
	// A no-op change touches nothing.
	before := len(link.mtuSet)
	if err := d.SetMTU(DefaultMTU, false); err != nil {
		t.Fatal(err)
	}
	if len(link.mtuSet) != before {
		t.Fatal("no-op SetMTU issued a netlink call")
	}
}

func skipUnlessRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("privileged TUN test: requires root / CAP_NET_ADMIN")
	}
}

// TUN-04..14, gate "descriptor and buffer leak checks": a real open/close
// round trip and a forced-failure path that must not leak the descriptor.
func TestOpenRealTUNRoundTrip(t *testing.T) {
	skipUnlessRoot(t)

	before := openFDCount(t)
	cfg := Config{Name: "", Address: netip.MustParsePrefix("10.99.0.2/30"), MTU: DefaultMTU}
	d, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if d.MTU() != DefaultMTU || d.Index() == 0 || d.Name() == "" {
		t.Fatalf("unexpected device: name=%q index=%d mtu=%d", d.Name(), d.Index(), d.MTU())
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if after := openFDCount(t); after > before {
		t.Fatalf("descriptor leak: %d -> %d open fds", before, after)
	}
}

// TUN-09: a failure after the descriptor is opened must close it. Requesting
// the name of an existing non-tun interface ("lo") makes TUNSETIFF fail with
// the fd already open.
func TestOpenClosesFDOnPostOpenFailure(t *testing.T) {
	skipUnlessRoot(t)

	before := openFDCount(t)
	_, err := Open(Config{Name: "lo", Address: netip.MustParsePrefix("10.99.1.2/24"), MTU: DefaultMTU})
	if err == nil {
		t.Fatal("Open with name \"lo\" unexpectedly succeeded")
	}
	if after := openFDCount(t); after > before {
		t.Fatalf("descriptor leak on a post-open failure: %d -> %d open fds", before, after)
	}
}

// TUN-08 / TUN-19: the production descriptor is an *os.File wrapping a char
// device. Both context cancellation and Close rely on the Go runtime poller
// accepting that fd — SetReadDeadline and an interrupting Close only work on a
// pollable descriptor. The unit tests use a fake that cannot prove this; these
// two exercise the real device with a read parked in the kernel and no traffic.
func TestOpenRealTUNCloseUnblocksParkedRead(t *testing.T) {
	skipUnlessRoot(t)

	d, err := Open(Config{Name: "", Address: netip.MustParsePrefix("10.99.2.2/30"), MTU: DefaultMTU})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, e := d.ReadPacket(context.Background(), make([]byte, DefaultMTU+64))
		done <- e
	}()
	time.Sleep(50 * time.Millisecond) // let the read block in the kernel

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case e := <-done:
		if !errors.Is(e, ErrClosed) {
			t.Fatalf("parked read returned %v, want ErrClosed", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock a parked ReadPacket")
	}
}

func TestOpenRealTUNContextCancelUnblocksParkedRead(t *testing.T) {
	skipUnlessRoot(t)

	d, err := Open(Config{Name: "", Address: netip.MustParsePrefix("10.99.3.2/30"), MTU: DefaultMTU})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := d.ReadPacket(ctx, make([]byte, DefaultMTU+64))
		done <- e
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("parked read returned %v, want context.Canceled", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancel did not unblock a parked ReadPacket")
	}
	if d.Stats().RxErrors != 0 {
		t.Fatalf("cancellation counted as a descriptor error")
	}
}

func openFDCount(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	return len(ents)
}
