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

// fakeRW stands in for the *os.File wrapping /dev/net/tun. The read deadline is
// modelled as state rather than a one-shot signal, so a test can observe a
// deadline that was left armed; a read parks until data, a deadline, or a close
// wakes it.
type fakeRW struct {
	mu       sync.Mutex
	cond     *sync.Cond
	queue    [][]byte
	readErr  error
	writeErr error
	shortN   int
	written  [][]byte
	closeN   int
	closed   bool
	closeErr error
	waiting  int

	deadline time.Time
	dlCalls  []time.Time

	// pastGate, when armed, blocks a SetReadDeadline call that arms a past
	// deadline until releasePastGate. pastEntered closes as soon as such a call
	// is entered, before it blocks.
	pastGate    chan struct{}
	pastEntered chan struct{}
}

func newFakeRW() *fakeRW {
	f := &fakeRW{}
	f.cond = sync.NewCond(&f.mu)
	return f
}

func (f *fakeRW) armPastGate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pastGate = make(chan struct{})
	f.pastEntered = make(chan struct{})
}

func (f *fakeRW) releasePastGate() {
	f.mu.Lock()
	g := f.pastGate
	f.mu.Unlock()
	close(g)
}

func (f *fakeRW) enqueue(p []byte) {
	f.mu.Lock()
	f.queue = append(f.queue, append([]byte(nil), p...))
	f.cond.Broadcast()
	f.mu.Unlock()
}

func (f *fakeRW) readDeadline() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deadline
}

func (f *fakeRW) lastDeadlineCall() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.dlCalls) == 0 {
		return time.Time{}, false
	}
	return f.dlCalls[len(f.dlCalls)-1], true
}

func (f *fakeRW) closes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeN
}

// waitReadParked blocks until a Read is waiting for data.
func (f *fakeRW) waitReadParked(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		f.mu.Lock()
		w := f.waiting
		f.mu.Unlock()
		if w > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("read never parked")
}

func (f *fakeRW) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for {
		if f.readErr != nil {
			return 0, f.readErr
		}
		// Queued data wins over an expired deadline, matching a real read(2)
		// with a packet already sitting in the device queue.
		if len(f.queue) > 0 {
			q := f.queue[0]
			f.queue = f.queue[1:]
			return copy(p, q), nil
		}
		if f.closed {
			return 0, os.ErrClosed
		}
		if !f.deadline.IsZero() && !f.deadline.After(time.Now()) {
			return 0, os.ErrDeadlineExceeded
		}
		f.waiting++
		f.cond.Wait()
		f.waiting--
	}
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
	f.closed = true
	f.cond.Broadcast()
	return f.closeErr
}

func (f *fakeRW) SetReadDeadline(t time.Time) error {
	if past := !t.IsZero() && !t.After(time.Now()); past {
		f.mu.Lock()
		if f.pastEntered != nil {
			select {
			case <-f.pastEntered:
			default:
				close(f.pastEntered)
			}
		}
		gate := f.pastGate
		f.mu.Unlock()
		if gate != nil {
			<-gate
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deadline = t
	f.dlCalls = append(f.dlCalls, t)
	f.cond.Broadcast()
	return nil
}

// fakeLink is the netlink stand-in. Besides the applied MTU sequence it records
// the peak number of mutations ever in flight at once, which is the direct
// evidence for SetMTU serialization, and whether one was issued after close.
type fakeLink struct {
	mu         sync.Mutex
	mtuSet     []int
	setErr     error
	closeN     int
	closeErr   error
	hold       time.Duration
	inFlight   int
	peak       int
	closed     bool
	afterClose bool
}

func (l *fakeLink) setMTU(mtu int) error {
	l.mu.Lock()
	if l.setErr != nil {
		err := l.setErr
		l.mu.Unlock()
		return err
	}
	if l.closed {
		l.afterClose = true
	}
	l.inFlight++
	if l.inFlight > l.peak {
		l.peak = l.inFlight
	}
	l.mtuSet = append(l.mtuSet, mtu)
	hold := l.hold
	l.mu.Unlock()

	if hold > 0 {
		time.Sleep(hold) // widen the window a racing caller could slip into
	}

	l.mu.Lock()
	l.inFlight--
	l.mu.Unlock()
	return nil
}

func (l *fakeLink) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeN++
	l.closed = true
	return l.closeErr
}

func (l *fakeLink) calls() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int(nil), l.mtuSet...)
}

func (l *fakeLink) maxConcurrent() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.peak
}

func (l *fakeLink) usedAfterClose() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.afterClose
}

func (l *fakeLink) closes() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closeN
}

// newDevice builds a device directly. explicitMax mirrors Config.MaxPacket:
// zero means the read ceiling tracks the live MTU.
func newDevice(rw *fakeRW, link *fakeLink, mtu, explicitMax int) *linuxDevice {
	d := &linuxDevice{
		rw:          rw,
		name:        "red0",
		index:       5,
		addr:        netip.MustParsePrefix("10.9.0.2/24"),
		explicitMax: explicitMax,
		link:        link,
	}
	d.mtu.Store(int64(mtu))
	return d
}

func TestLinuxReadPacket(t *testing.T) {
	rw := newFakeRW()
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)
	rw.enqueue(make([]byte, 100))

	buf := make([]byte, MaxMTU+64)
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

	buf := make([]byte, MaxMTU+200)
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
		_, err := d.ReadPacket(ctx, make([]byte, MaxMTU+64))
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
	if dl := rw.readDeadline(); !dl.IsZero() {
		t.Fatalf("read deadline left armed at %v after a cancelled read", dl)
	}
}

func TestLinuxReadPermanentError(t *testing.T) {
	rw := newFakeRW()
	rw.readErr = errors.New("EIO")
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)

	if _, err := d.ReadPacket(context.Background(), make([]byte, MaxMTU+64)); err == nil {
		t.Fatal("expected an error")
	}
	if d.Stats().RxErrors != 1 {
		t.Fatalf("RxErrors = %d", d.Stats().RxErrors)
	}
}

func TestLinuxReadWriteAfterClose(t *testing.T) {
	d := newDevice(newFakeRW(), &fakeLink{}, DefaultMTU, DefaultMTU)
	_ = d.Close()
	if _, err := d.ReadPacket(context.Background(), make([]byte, MaxMTU+64)); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
	if _, err := d.WritePacket([]byte{1, 2, 3}); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if err := d.SetMTU(DefaultMTU-40, false); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetMTU after close: %v", err)
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
	s := d.Stats()
	if s.TxShort != 1 || s.TxErrors != 1 || s.TxPackets != 0 || s.TxBytes != 0 {
		t.Fatalf("stats %+v: want TxShort=1 TxErrors=1 TxPackets=0 TxBytes=0", s)
	}
}

// A hard write failure is counted in TxErrors but is not a short write.
func TestLinuxWriteErrorNotCountedAsShort(t *testing.T) {
	rw := newFakeRW()
	rw.writeErr = errors.New("EIO")
	d := newDevice(rw, &fakeLink{}, DefaultMTU, DefaultMTU)
	if _, err := d.WritePacket([]byte{1, 2, 3, 4}); err == nil {
		t.Fatal("expected a write error")
	}
	if s := d.Stats(); s.TxErrors != 1 || s.TxShort != 0 {
		t.Fatalf("stats %+v: a hard write failure is not a short write", s)
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
	if rw.closes() != 1 {
		t.Fatalf("descriptor closed %d times, want 1", rw.closes())
	}
	if link.closes() != 1 {
		t.Fatalf("netlink closed %d times, want 1", link.closes())
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
	if d.MTU() != DefaultMTU-40 || len(link.calls()) != 1 || link.calls()[0] != DefaultMTU-40 {
		t.Fatalf("reduce not applied: mtu=%d set=%v", d.MTU(), link.calls())
	}
	if err := d.SetMTU(DefaultMTU, false); !errors.Is(err, ErrMTUIncrease) {
		t.Fatalf("unauthenticated increase: %v", err)
	}
	if len(link.calls()) != 1 {
		t.Fatalf("netlink touched for a rejected increase: %v", link.calls())
	}
	if err := d.SetMTU(DefaultMTU, true); err != nil {
		t.Fatalf("authenticated increase: %v", err)
	}
	if d.MTU() != DefaultMTU {
		t.Fatalf("mtu = %d after authenticated increase", d.MTU())
	}
	// A no-op change touches nothing.
	before := len(link.calls())
	if err := d.SetMTU(DefaultMTU, false); err != nil {
		t.Fatal(err)
	}
	if len(link.calls()) != before {
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
		_, e := d.ReadPacket(context.Background(), make([]byte, MaxMTU+64))
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
		_, e := d.ReadPacket(ctx, make([]byte, MaxMTU+64))
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

// TUN-19 regression: the deadline the cancel callback arms must be cleared only
// after that callback has finished. If ReadPacket returns while the callback is
// still in flight, its deferred clear can land first and the callback then
// re-arms a permanently expired deadline, poisoning every later read.
//
// The fake blocks the callback inside SetReadDeadline while data arrives, which
// reproduces exactly that interleaving: the read succeeds and reaches the
// deferred teardown with the callback still running.
func TestLinuxReadPacketOrdersCancelCallbackBeforeClearingDeadline(t *testing.T) {
	rw := newFakeRW()
	rw.armPastGate()
	d := newDevice(rw, &fakeLink{}, DefaultMTU, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := d.ReadPacket(ctx, make([]byte, MaxMTU+64))
		done <- result{n, err}
	}()

	rw.waitReadParked(t)
	cancel()
	<-rw.pastEntered // the callback has started and is now blocked

	// Data lands at the same instant, so the read completes successfully and
	// ReadPacket reaches its deferred teardown with the callback still running.
	rw.enqueue(make([]byte, 100))

	select {
	case r := <-done:
		t.Fatalf("ReadPacket returned (%d, %v) while its cancel callback was still running: "+
			"the deferred clear can now be overtaken by the callback, leaving the deadline armed", r.n, r.err)
	case <-time.After(200 * time.Millisecond):
	}

	rw.releasePastGate()

	select {
	case r := <-done:
		if r.err != nil || r.n != 100 {
			t.Fatalf("ReadPacket = (%d, %v), want (100, nil)", r.n, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadPacket did not return after its cancel callback finished")
	}

	if dl := rw.readDeadline(); !dl.IsZero() {
		t.Fatalf("read deadline left at %v; every later read is poisoned", dl)
	}
	if last, ok := rw.lastDeadlineCall(); !ok || !last.IsZero() {
		t.Fatalf("last SetReadDeadline call was %v (present=%v), want the zero clear last", last, ok)
	}

	// The descriptor is still usable with a fresh context.
	rw.enqueue(make([]byte, 40))
	if n, err := d.ReadPacket(context.Background(), make([]byte, MaxMTU+64)); err != nil || n != 40 {
		t.Fatalf("read after a cancelled read: (%d, %v), want (40, nil)", n, err)
	}
}

// TUN-16 / TUN-20 regression: with the default (zero) MaxPacket the read ceiling
// must follow an authenticated MTU increase. A ceiling frozen at the initial MTU
// keeps dropping packets that became legal the moment the MTU rose.
func TestLinuxDefaultMaxPacketTracksAuthenticatedMTUIncrease(t *testing.T) {
	rw := newFakeRW()
	d := newDevice(rw, &fakeLink{}, DefaultMTU, 0) // 0 = track the live MTU

	big := DefaultMTU + 50         // legal only once the MTU is raised
	buf := make([]byte, MaxMTU+64) // sized against the static ceiling

	rw.enqueue(make([]byte, big))
	if _, err := d.ReadPacket(context.Background(), buf); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("before the increase: err = %v, want ErrPacketTooLarge", err)
	}

	if err := d.SetMTU(MaxMTU, true); err != nil {
		t.Fatalf("authenticated increase: %v", err)
	}
	if got := d.maxPacket(); got != MaxMTU {
		t.Fatalf("ceiling = %d after the increase, want %d", got, MaxMTU)
	}

	rw.enqueue(make([]byte, big))
	n, err := d.ReadPacket(context.Background(), buf)
	if err != nil || n != big {
		t.Fatalf("after the authenticated increase: (%d, %v), want (%d, nil) — the ceiling did not track the MTU",
			n, err, big)
	}
	if s := d.Stats(); s.RxOversize != 1 || s.RxPackets != 1 {
		t.Fatalf("stats %+v", s)
	}

	// A reduction pulls the ceiling back down with it.
	if err := d.SetMTU(DefaultMTU, false); err != nil {
		t.Fatalf("reduce: %v", err)
	}
	rw.enqueue(make([]byte, big))
	if _, err := d.ReadPacket(context.Background(), buf); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("after the reduction: err = %v, want ErrPacketTooLarge", err)
	}
}

// An explicit Config.MaxPacket is a fixed ceiling: it never tracks the MTU, and
// the MTU may not be raised past it.
func TestLinuxExplicitMaxPacketIsAHardCeiling(t *testing.T) {
	rw := newFakeRW()
	link := &fakeLink{}
	d := newDevice(rw, link, DefaultMTU, DefaultMTU)

	if err := d.SetMTU(MaxMTU, true); !errors.Is(err, ErrMTUAboveMaxPacket) {
		t.Fatalf("err = %v, want ErrMTUAboveMaxPacket", err)
	}
	if len(link.calls()) != 0 {
		t.Fatalf("netlink touched for a refused MTU change: %v", link.calls())
	}
	if d.MTU() != DefaultMTU {
		t.Fatalf("mtu moved to %d despite the refusal", d.MTU())
	}

	// Reducing the MTU leaves the explicit ceiling where it was.
	if err := d.SetMTU(DefaultMTU-40, false); err != nil {
		t.Fatalf("reduce: %v", err)
	}
	if got := d.maxPacket(); got != DefaultMTU {
		t.Fatalf("explicit ceiling moved to %d", got)
	}
	rw.enqueue(make([]byte, DefaultMTU))
	if n, err := d.ReadPacket(context.Background(), make([]byte, MaxMTU+64)); err != nil || n != DefaultMTU {
		t.Fatalf("explicit ceiling should still admit %d bytes: (%d, %v)", DefaultMTU, n, err)
	}
}

// TUN-17 regression: two concurrent callers must not each authorize against the
// same stale MTU snapshot. Both targets below are reductions from the starting
// MTU, so without serialization the later netlink write can raise the effective
// MTU with no authentication. The fake reports the peak number of simultaneous
// netlink mutations, which is the direct evidence of serialization.
func TestLinuxSetMTUConcurrentCallsAreSerialized(t *testing.T) {
	rw := newFakeRW()
	link := &fakeLink{hold: 5 * time.Millisecond}
	d := newDevice(rw, link, DefaultMTU, 0)

	targets := []int{DefaultMTU - 40, DefaultMTU - 10}
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i, target int) {
			defer wg.Done()
			errs[i] = d.SetMTU(target, false)
		}(i, target)
	}
	wg.Wait()

	if peak := link.maxConcurrent(); peak != 1 {
		t.Fatalf("%d netlink MTU mutations in flight at once; SetMTU is not serialized", peak)
	}
	// Either ordering ends at the lower value: if the deeper reduction lands
	// first the shallower one is refused as an increase; if it lands second it
	// is simply another reduction.
	if got := d.MTU(); got != DefaultMTU-40 {
		t.Fatalf("final mtu = %d, want %d — an unauthenticated increase slipped through",
			got, DefaultMTU-40)
	}
	prev := DefaultMTU
	for _, m := range link.calls() {
		if m > prev {
			t.Fatalf("applied MTU sequence %v increases (%d -> %d) with no authentication",
				link.calls(), prev, m)
		}
		prev = m
	}
	for i, err := range errs {
		if err != nil && !errors.Is(err, ErrMTUIncrease) {
			t.Fatalf("target %d: unexpected error %v", targets[i], err)
		}
	}
}

// TUN-08 / TUN-17: Close and SetMTU share the device mutex, so the netlink
// socket is never closed underneath an in-flight MTU mutation. Run under -race.
func TestLinuxSetMTUDoesNotRaceClose(t *testing.T) {
	rw := newFakeRW()
	link := &fakeLink{hold: time.Millisecond}
	d := newDevice(rw, link, DefaultMTU, 0)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := d.SetMTU(DefaultMTU-1-i, false)
			if err != nil && !errors.Is(err, ErrClosed) && !errors.Is(err, ErrMTUIncrease) {
				t.Errorf("SetMTU: unexpected error %v", err)
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := d.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	wg.Wait()

	if link.usedAfterClose() {
		t.Fatal("a netlink MTU mutation was issued after the netlink socket was closed")
	}
	if link.closes() != 1 || rw.closes() != 1 {
		t.Fatalf("close counts: netlink=%d descriptor=%d, want 1 and 1", link.closes(), rw.closes())
	}
}
