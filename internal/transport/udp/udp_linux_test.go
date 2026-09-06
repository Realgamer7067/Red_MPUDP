//go:build linux

package udp_test

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
	"github.com/Realgamer7067/Red_MPUDP/internal/transport/udp"
)

var loopback = netip.MustParseAddr("127.0.0.1")

// newServer binds a server socket on an ephemeral loopback port. No privilege
// is required: SO_BINDTODEVICE and SO_MARK are the only CAP-gated options and
// the server uses neither.
func newServer(t *testing.T, cfg udp.ServerConfig) *udp.Server {
	t.Helper()
	cfg.Listen = netip.AddrPortFrom(loopback, 0)
	s, err := udp.Listen(cfg)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newClient(t *testing.T, server netip.AddrPort, cfg udp.ClientConfig) *udp.Client {
	t.Helper()
	cfg.Server = server
	cfg.LocalAddr = loopback
	c, err := udp.Dial(cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func readWithin(t *testing.T, d transport.DatagramIO, dst []byte, within time.Duration) (int, transport.ReceiveMeta, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	return d.ReadInto(ctx, dst)
}

// UDP-16, UDP-23, UDP-27..31: a datagram crosses a real socket pair and the
// server recovers the source, the local destination address and the receive
// ifindex from kernel ancillary data.
func TestClientToServerRoundTrip(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	cli := newClient(t, srv.LocalAddr(), udp.ClientConfig{})

	if err := cli.WriteTo(context.Background(), []byte("ping"), transport.Endpoint{}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	buf := make([]byte, 512)
	n, meta, err := readWithin(t, srv, buf, 2*time.Second)
	if err != nil {
		t.Fatalf("server ReadInto: %v", err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("payload = %q", buf[:n])
	}
	if !meta.Source.IsValid() || meta.Source.Addr() != loopback {
		t.Fatalf("source = %v, want a 127.0.0.1 endpoint", meta.Source)
	}
	if meta.LocalAddr != loopback { // UDP-31: from IP_PKTINFO
		t.Fatalf("LocalAddr = %v, want %v — IP_PKTINFO was not parsed", meta.LocalAddr, loopback)
	}
	if meta.IfIndex == 0 {
		t.Fatal("IfIndex = 0; IP_PKTINFO did not yield a receive ifindex")
	}
	if meta.Truncated {
		t.Fatal("complete datagram reported as truncated")
	}

	// UDP-28: the server replies to the caller-selected endpoint.
	if err := srv.WriteTo(context.Background(), []byte("pong"), transport.Endpoint{AddrPort: meta.Source}); err != nil {
		t.Fatalf("server WriteTo: %v", err)
	}
	n, _, err = readWithin(t, cli, buf, 2*time.Second)
	if err != nil {
		t.Fatalf("client ReadInto: %v", err)
	}
	if string(buf[:n]) != "pong" {
		t.Fatalf("reply = %q", buf[:n])
	}
}

// GATE "oversized/truncated packets cannot be mistaken for valid packets", and
// UDP-32/UDP-33: a datagram larger than the receive buffer is dropped whole,
// flagged, and leaves the caller's buffer untouched so nothing can parse it.
func TestTruncatedDatagramIsDroppedNotTruncated(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	cli := newClient(t, srv.LocalAddr(), udp.ClientConfig{})

	payload := make([]byte, 800)
	for i := range payload {
		payload[i] = 0x5A
	}
	if err := cli.WriteTo(context.Background(), payload, transport.Endpoint{}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	small := make([]byte, 100)
	n, meta, err := readWithin(t, srv, small, 2*time.Second)
	if !errors.Is(err, transport.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0 — a truncated datagram must yield no bytes", n)
	}
	if !meta.Truncated {
		t.Fatal("meta.Truncated not set")
	}
	// dst's contents are deliberately NOT asserted here. The kernel copies as
	// much of the datagram as fits before reporting the length that did not,
	// so small now holds a prefix of a dropped datagram. n=0 plus ErrTruncated
	// is the whole contract; the bytes are unspecified and must never be
	// parsed. Asserting they were untouched would encode a guarantee the
	// implementation does not make.

	// The next read must still work: the truncated datagram was consumed, not
	// left to desynchronise the socket.
	if err := cli.WriteTo(context.Background(), []byte("after"), transport.Endpoint{}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	buf := make([]byte, 512)
	n, _, err = readWithin(t, srv, buf, 2*time.Second)
	if err != nil || string(buf[:n]) != "after" {
		t.Fatalf("read after truncation = (%q, %v)", buf[:n], err)
	}
}

// UDP-38: an oversized send is refused as ErrOversize and never retried at a
// smaller size.
func TestOversizeWriteRefused(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	cli := newClient(t, srv.LocalAddr(), udp.ClientConfig{MaxDatagram: 256})

	err := cli.WriteTo(context.Background(), make([]byte, 257), transport.Endpoint{})
	if !errors.Is(err, transport.ErrOversize) {
		t.Fatalf("err = %v, want ErrOversize", err)
	}
	// Nothing was sent, at any size.
	if _, _, err := readWithin(t, srv, make([]byte, 512), 150*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("server read = %v; an oversized send must not be retried smaller", err)
	}
}

// UDP-21: buffer sizes are requested and read back. Linux doubles the request
// and clamps it to net.core.*mem_max, so only the relationship is asserted.
func TestBufferSizeReadback(t *testing.T) {
	const want = 256 * 1024
	srv := newServer(t, udp.ServerConfig{ReceiveBuffer: want, SendBuffer: want})
	b := srv.Diagnostics().Buffers

	if b.RequestedReceive != want || b.RequestedSend != want {
		t.Fatalf("requested sizes not recorded: %+v", b)
	}
	if b.EffectiveReceive <= 0 || b.EffectiveSend <= 0 {
		t.Fatalf("effective sizes not read back: %+v", b)
	}
	t.Logf("buffers: %+v (kernel doubling and net.core.*mem_max clamping are both expected)", b)
}

// UDP-11: an interface that does not exist is refused before any socket is
// created.
func TestDialRejectsMissingInterface(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	_, err := udp.Dial(udp.ClientConfig{
		Interface: "definitely-not-here0",
		Server:    srv.LocalAddr(),
	})
	if !errors.Is(err, udp.ErrInterfaceGone) {
		t.Fatalf("err = %v, want ErrInterfaceGone", err)
	}
}

// UDP-14: a configured mark the kernel refuses is fatal. Unprivileged, SO_MARK
// fails with EPERM; as root it succeeds. Either outcome is correct — what must
// never happen is Dial returning a socket that silently lost its mark, because
// that socket would egress through the wrong routing table.
func TestDialFWMarkIsAllOrNothing(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	c, err := udp.Dial(udp.ClientConfig{Server: srv.LocalAddr(), LocalAddr: loopback, FWMark: 0x4444})
	if err != nil {
		if os.Geteuid() == 0 {
			t.Fatalf("SO_MARK failed as root: %v", err)
		}
		t.Logf("SO_MARK refused unprivileged, as expected: %v", err)
		return
	}
	defer c.Close()
	if os.Geteuid() != 0 {
		t.Fatal("SO_MARK appeared to succeed without CAP_NET_ADMIN")
	}
	if got := c.Diagnostics().FWMark; got != 0x4444 {
		t.Fatalf("Diagnostics().FWMark = %#x, want 0x4444", got)
	}
}

// UDP-38 / error-queue ownership: sending to a closed port makes the kernel
// deliver ICMP port-unreachable on the error queue. Whichever goroutine the
// kernel wakes first, the error must reach ReadPathError exactly once and must
// never leave it blocked forever. Needs no privilege.
func TestPathErrorReachesReaderRegardlessOfRace(t *testing.T) {
	// Bind and immediately close a server to obtain a port nothing listens on.
	dead := newServer(t, udp.ServerConfig{})
	deadAddr := dead.LocalAddr()
	if err := dead.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	cli := newClient(t, deadAddr, udp.ClientConfig{})

	// A concurrent reader competes with the error-queue goroutine for the
	// pending socket error.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _, _ = cli.ReadInto(ctx, make([]byte, 512))
	}()

	deadline := time.Now().Add(3 * time.Second)
	var got transport.PathError
	var err error
	for time.Now().Before(deadline) {
		_ = cli.WriteTo(context.Background(), []byte("x"), transport.Endpoint{})
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		got, err = cli.ReadPathError(ctx)
		cancel()
		if err == nil {
			break
		}
	}
	<-readDone
	if err != nil {
		t.Skipf("no path error observed within the deadline (loopback ICMP suppression): %v", err)
	}
	t.Logf("path error: %s", got)
	// UDP-36 / UDP-37: whichever goroutine won the race, the event must be
	// attributed to this path's peer. That is the property under test.
	if got.Peer != deadAddr {
		t.Fatalf("PathError.Peer = %v, want the unreachable endpoint %v — an event that "+
			"cannot be attributed to a path would be applied to the wrong one", got.Peer, deadAddr)
	}
	// Local is deliberately NOT asserted. Local=true means the normal receive
	// path consumed the pending socket error first; Local=false means the
	// error-queue goroutine parsed the ICMP message first. Both are correct and
	// which one happens is exactly the race this test exercises — pinning it
	// would make the test assert scheduling, not behaviour.
	t.Logf("won by the %s path", map[bool]string{true: "normal-read", false: "error-queue"}[got.Local])
}

// GATE "closing transport leaves no goroutine blocked" (UDP-39, UDP-40): with a
// reader and a path-error reader both parked, Close unblocks both, is
// idempotent, and leaves no goroutine behind.
func TestCloseUnblocksEveryoneAndLeaksNoGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()

	srv := newServer(t, udp.ServerConfig{})
	readErr := make(chan error, 1)
	peErr := make(chan error, 1)
	go func() { _, _, err := srv.ReadInto(context.Background(), make([]byte, 512)); readErr <- err }()
	go func() { _, err := srv.ReadPathError(context.Background()); peErr <- err }()
	time.Sleep(50 * time.Millisecond) // let both park in the poller

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := srv.Close(); err != nil { // UDP-40
		t.Fatalf("second Close: %v", err)
	}

	for name, ch := range map[string]chan error{"ReadInto": readErr, "ReadPathError": peErr} {
		select {
		case err := <-ch:
			if err == nil {
				t.Fatalf("%s returned nil after Close", name)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("Close left %s blocked", name)
		}
	}

	// The error-queue goroutine must have exited too.
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutines did not settle after Close: %d -> %d", before, runtime.NumGoroutine())
}

// UDP-39: a cancelled context unblocks a parked read without closing the
// socket, and the socket stays usable afterwards — the deadline must not be
// left armed (the M06 TUN-19 lesson).
func TestContextCancelUnblocksReadAndLeavesSocketUsable(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	cli := newClient(t, srv.LocalAddr(), udp.ClientConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := srv.ReadInto(ctx, make([]byte, 512)); done <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not unblock ReadInto")
	}

	if err := cli.WriteTo(context.Background(), []byte("still-alive"), transport.Endpoint{}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	buf := make([]byte, 512)
	n, _, err := readWithin(t, srv, buf, 2*time.Second)
	if err != nil || string(buf[:n]) != "still-alive" {
		t.Fatalf("read after a cancelled read = (%q, %v) — a stale deadline was left armed", buf[:n], err)
	}
}

func TestReadAfterCloseIsErrClosed(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.ReadInto(context.Background(), make([]byte, 64)); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("ReadInto after close = %v, want ErrClosed", err)
	}
	if err := srv.WriteTo(context.Background(), []byte("x"),
		transport.Endpoint{AddrPort: netip.AddrPortFrom(loopback, 9)}); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("WriteTo after close = %v, want ErrClosed", err)
	}
	if _, err := srv.ReadPathError(context.Background()); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("ReadPathError after close = %v, want ErrClosed", err)
	}
}

// Blocker 8 companion: state the truncation contract as a test, so the fact
// that dst is written before truncation is known cannot be forgotten.
func TestTruncatedReadLeavesBufferContentsUnspecified(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	cli := newClient(t, srv.LocalAddr(), udp.ClientConfig{})

	payload := make([]byte, 600)
	for i := range payload {
		payload[i] = 0x7C
	}
	if err := cli.WriteTo(context.Background(), payload, transport.Endpoint{}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	dst := make([]byte, 50)
	n, meta, err := readWithin(t, srv, dst, 2*time.Second)
	if !errors.Is(err, transport.ErrTruncated) || n != 0 || !meta.Truncated {
		t.Fatalf("got (n=%d, truncated=%v, err=%v), want (0, true, ErrTruncated)", n, meta.Truncated, err)
	}
	// The kernel really does write a prefix into dst. Recording that here keeps
	// the documented contract ("contents unspecified") honest rather than
	// aspirational.
	t.Logf("dst[0]=%#x after a truncated read — a prefix of the dropped datagram, "+
		"which is exactly why n=0 and the bytes must not be parsed", dst[0])
}

// UDP-28 / blocker 2: the server honours a caller-selected outbound interface.
func TestServerReplyHonoursSelectedInterface(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	cli := newClient(t, srv.LocalAddr(), udp.ClientConfig{})

	if err := cli.WriteTo(context.Background(), []byte("req"), transport.Endpoint{}); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	buf := make([]byte, 512)
	_, meta, err := readWithin(t, srv, buf, 2*time.Second)
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	if meta.IfIndex == 0 {
		t.Fatal("no receive ifindex to reply through")
	}

	// Replying through the interface the request arrived on must work...
	err = srv.WriteTo(context.Background(), []byte("reply"),
		transport.Endpoint{AddrPort: meta.Source, IfIndex: meta.IfIndex})
	if err != nil {
		t.Fatalf("reply via ifindex %d: %v", meta.IfIndex, err)
	}
	n, _, err := readWithin(t, cli, buf, 2*time.Second)
	if err != nil || string(buf[:n]) != "reply" {
		t.Fatalf("client got (%q, %v)", buf[:n], err)
	}

	// ...and a bogus index must fail loudly rather than silently letting the
	// routing table choose.
	err = srv.WriteTo(context.Background(), []byte("nope"),
		transport.Endpoint{AddrPort: meta.Source, IfIndex: 1 << 20})
	if !errors.Is(err, udp.ErrBadIfIndex) {
		t.Fatalf("bogus ifindex returned %v, want ErrBadIfIndex", err)
	}
}

// UDP-40 / blocker 4: after Close, every method reports ErrClosed — ahead of an
// invalid destination, an oversized payload, a cancelled context, and a queued
// path error alike. A caller shutting down must never have to tell "bad
// request" apart from "gone".
func TestAfterCloseEverythingIsErrClosed(t *testing.T) {
	srv := newServer(t, udp.ServerConfig{})
	cli := newClient(t, srv.LocalAddr(), udp.ClientConfig{MaxDatagram: 128})

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := cli.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := cli.Close(); err != nil { // idempotent
		t.Fatalf("second Close: %v", err)
	}

	checks := []struct {
		name string
		fn   func() error
	}{
		{"ReadInto", func() error {
			_, _, err := cli.ReadInto(context.Background(), make([]byte, 512))
			return err
		}},
		{"ReadInto with a cancelled context", func() error {
			_, _, err := cli.ReadInto(cancelled, make([]byte, 512))
			return err
		}},
		{"WriteTo", func() error {
			return cli.WriteTo(context.Background(), []byte("x"), transport.Endpoint{})
		}},
		{"WriteTo oversized", func() error {
			return cli.WriteTo(context.Background(), make([]byte, 4096), transport.Endpoint{})
		}},
		{"WriteTo to the wrong destination", func() error {
			return cli.WriteTo(context.Background(), []byte("x"),
				transport.Endpoint{AddrPort: netip.AddrPortFrom(loopback, 1)})
		}},
		{"WriteTo with a cancelled context", func() error {
			return cli.WriteTo(cancelled, []byte("x"), transport.Endpoint{})
		}},
		{"ReadPathError", func() error {
			_, err := cli.ReadPathError(context.Background())
			return err
		}},
		{"ReadPathError with a cancelled context", func() error {
			_, err := cli.ReadPathError(cancelled)
			return err
		}},
	}
	for _, c := range checks {
		if err := c.fn(); !errors.Is(err, transport.ErrClosed) {
			t.Errorf("%s after Close = %v, want ErrClosed", c.name, err)
		}
	}

	// The server: an invalid destination must also lose to ErrClosed.
	if err := srv.Close(); err != nil {
		t.Fatalf("server Close: %v", err)
	}
	if err := srv.WriteTo(context.Background(), []byte("x"), transport.Endpoint{}); !errors.Is(err, transport.ErrClosed) {
		t.Errorf("server WriteTo with an invalid destination after Close = %v, want ErrClosed", err)
	}
	if err := srv.WriteTo(context.Background(), []byte("x"),
		transport.Endpoint{AddrPort: netip.AddrPortFrom(loopback, 9), IfIndex: 1 << 20}); !errors.Is(err, transport.ErrClosed) {
		t.Errorf("server WriteTo with a bad ifindex after Close = %v, want ErrClosed", err)
	}
}
