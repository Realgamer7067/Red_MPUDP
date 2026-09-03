package quicdatagram

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/sys/unix"
)

type loopback struct {
	client *quic.Conn
	server *quic.Conn
	stop   func()
}

func newLoopback(tb testing.TB, echo bool) *loopback {
	tb.Helper()
	serverTLS, clientTLS, err := selfSignedTLS()
	if err != nil {
		tb.Fatal(err)
	}
	qconf := &quic.Config{EnableDatagrams: true}

	srvPC, err := BoundUDPConn("", 0)
	if err != nil {
		tb.Fatal(err)
	}
	srvTr := &quic.Transport{Conn: srvPC}
	ln, err := srvTr.Listen(serverTLS, qconf)
	if err != nil {
		tb.Fatal(err)
	}

	cliPC, err := BoundUDPConn("", 0)
	if err != nil {
		tb.Fatal(err)
	}
	cliTr := &quic.Transport{Conn: cliPC}

	accCh := make(chan *quic.Conn, 1)
	go func() {
		c, err := ln.Accept(context.Background())
		if err == nil {
			accCh <- c
		} else {
			accCh <- nil
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cc, err := cliTr.Dial(ctx, ln.Addr(), clientTLS, qconf)
	if err != nil {
		tb.Fatalf("dial: %v", err)
	}
	sc := <-accCh
	if sc == nil {
		tb.Fatal("accept failed")
	}

	stopEcho := make(chan struct{})
	if echo {
		go func() {
			for {
				select {
				case <-stopEcho:
					return
				default:
				}
				d, err := sc.ReceiveDatagram(context.Background())
				if err != nil {
					return
				}
				_ = sc.SendDatagram(d)
			}
		}()
	}

	return &loopback{
		client: cc,
		server: sc,
		stop: func() {
			close(stopEcho)
			cc.CloseWithError(0, "done")
			sc.CloseWithError(0, "done")
			ln.Close()
			srvTr.Close()
			cliTr.Close()
			srvPC.Close()
			cliPC.Close()
		},
	}
}

// SPIKE-51: a QUIC connection over a caller-owned UDP socket, with datagram
// support negotiated both ways.
func TestCallerOwnedSocketAndDatagramSupport(t *testing.T) {
	lb := newLoopback(t, true)
	defer lb.stop()

	st := lb.client.ConnectionState()
	if !st.SupportsDatagrams.Local || !st.SupportsDatagrams.Remote {
		t.Fatalf("datagram support not negotiated both ways: %+v", st.SupportsDatagrams)
	}
}

// SPIKE-52: the same socket can carry SO_BINDTODEVICE + SO_MARK (needs
// CAP_NET_ADMIN; skipped otherwise). This is the mechanism internal/transport/
// udp will use in M07.
func TestInterfaceBoundSocket(t *testing.T) {
	_, err := BoundUDPConn("lo", 0x524d0001)
	if errors.Is(err, unix.EPERM) {
		t.Skip("SO_BINDTODEVICE/SO_MARK need CAP_NET_ADMIN")
	}
	if err != nil {
		t.Fatalf("bound conn: %v", err)
	}
}

// SPIKE-53: unreliable datagrams round-trip with no stream fallback (no stream
// is ever opened).
func TestUnreliableDatagramRoundTrip(t *testing.T) {
	lb := newLoopback(t, true)
	defer lb.stop()

	payload := []byte("latency-sensitive inner packet")
	if err := lb.client.SendDatagram(payload); err != nil {
		t.Fatalf("SendDatagram: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := lb.client.ReceiveDatagram(ctx)
	if err != nil {
		t.Fatalf("ReceiveDatagram: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("echo mismatch: %q != %q", got, payload)
	}
}

// SPIKE-54: the current maximum datagram size is only discoverable reactively,
// from DatagramTooLargeError. There is no public getter.
func TestMaxDatagramSizeVisibility(t *testing.T) {
	lb := newLoopback(t, false)
	defer lb.stop()

	var maxOK int
	var reported int64
	for size := 200; size <= 1500; size += 4 {
		err := lb.client.SendDatagram(make([]byte, size))
		if err == nil {
			maxOK = size
			continue
		}
		var tooLarge *quic.DatagramTooLargeError
		if errors.As(err, &tooLarge) {
			reported = tooLarge.MaxDatagramPayloadSize
			break
		}
		t.Fatalf("unexpected SendDatagram error at size %d: %v", size, err)
	}
	t.Logf("largest accepted datagram payload: %d bytes; DatagramTooLargeError.MaxDatagramPayloadSize = %d", maxOK, reported)
	if reported == 0 {
		t.Fatal("never hit DatagramTooLargeError below 1500 bytes")
	}
	// FINDING: quic-go v0.62 exposes no Conn method returning the current max
	// datagram payload size. RED_MPUDP (design §10) must know the confirmed size
	// BEFORE sending; here it is only learnable by sending an oversized datagram
	// and parsing the error.
}

// SPIKE-55: there is no per-datagram delivery or loss feedback. SendDatagram
// returns nil once queued; the caller is never told whether the datagram was
// sent, ACKed, or dropped.
func TestNoPerDatagramDeliveryFeedback(t *testing.T) {
	lb := newLoopback(t, false)
	defer lb.stop()

	// Every send "succeeds" (is queued) with no delivery signal of any kind.
	for i := 0; i < 50; i++ {
		if err := lb.client.SendDatagram([]byte("x")); err != nil {
			t.Fatalf("SendDatagram %d: %v", i, err)
		}
	}
	// FINDING: quic-go tracks DATAGRAM frame ACK/loss internally for congestion
	// control but exposes no callback, channel, or return value for it. The
	// RED_MPUDP scheduler (PATH_REPORT, winner/rescue rate, per-path loss) needs
	// that signal per copy -> would require a fork or an application-level ACK
	// protocol layered on top of QUIC datagrams.
	t.Log("no delivery/loss feedback API on quic.Conn for DATAGRAM frames (v0.62.0)")
}

// SPIKE-56a: ReceiveDatagram honours context cancellation.
func TestReceiveDatagramCancellation(t *testing.T) {
	lb := newLoopback(t, false)
	defer lb.stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := lb.client.ReceiveDatagram(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReceiveDatagram(cancelled) = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 250*time.Millisecond {
		t.Fatalf("ReceiveDatagram(cancelled) took %v", d)
	}
}

// SPIKE-56b: the datagram send queue is a fixed 32-frame FIFO and SendDatagram
// BLOCKS when it is full — there is no byte bound, deadline, priority, or drop
// policy. Demonstrated by throttling the underlying socket so QUIC cannot flush
// the queue: a goroutine issuing 10k sends stalls almost immediately.
func TestSendQueueBlocksWhenFull(t *testing.T) {
	serverTLS, clientTLS, err := selfSignedTLS()
	if err != nil {
		t.Fatal(err)
	}
	qconf := &quic.Config{EnableDatagrams: true}

	srvPC, err := BoundUDPConn("", 0)
	if err != nil {
		t.Fatal(err)
	}
	srvTr := &quic.Transport{Conn: srvPC}
	ln, err := srvTr.Listen(serverTLS, qconf)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	rawCli, err := BoundUDPConn("", 0)
	if err != nil {
		t.Fatal(err)
	}
	// 20 KB/s: far below any datagram send rate, so the 32-frame queue fills.
	cliPC := newThrottledPacketConn(rawCli, 20_000)
	cliTr := &quic.Transport{Conn: cliPC}
	defer cliTr.Close()

	accCh := make(chan *quic.Conn, 1)
	go func() { c, _ := ln.Accept(context.Background()); accCh <- c }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cc, err := cliTr.Dial(ctx, ln.Addr(), clientTLS, qconf)
	if err != nil {
		t.Fatalf("dial (throttled): %v", err)
	}
	defer cc.CloseWithError(0, "done")
	if sc := <-accCh; sc != nil {
		defer sc.CloseWithError(0, "done")
	}

	var sent int64
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1000)
		for i := 0; i < 10000; i++ {
			if err := cc.SendDatagram(buf); err != nil {
				return
			}
			atomic.AddInt64(&sent, 1)
		}
		close(done)
	}()

	select {
	case <-done:
		t.Fatalf("SendDatagram completed all 10k sends over a 20 KB/s link — queue is not bounded/blocking as expected")
	case <-time.After(500 * time.Millisecond):
	}
	n := atomic.LoadInt64(&sent)
	// The queue is 32 frames; a handful may also be in flight / lost to CC.
	if n > 200 {
		t.Fatalf("SendDatagram accepted %d datagrams before blocking — expected it to stall near the 32-frame queue bound", n)
	}
	t.Logf("SendDatagram blocked after %d sends over a throttled link (fixed 32-frame queue, no byte bound / deadline / priority / drop)", n)

	// FINDING: RED_MPUDP design §9.3 needs per-path control/latency/primary/
	// replica queues bounded by packets AND bytes, with deadlines, tail-drop
	// after deadline, and expired-replica drop. quic-go's single blocking
	// 32-frame queue (source: datagram_queue.go maxDatagramSendQueueLen=32,
	// Add() blocks) provides none of that; the queue layer would be rebuilt
	// above QUIC, and a blocking SendDatagram would stall the path actor.
}
