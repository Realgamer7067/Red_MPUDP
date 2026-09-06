//go:build linux

package udp_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
	"github.com/Realgamer7067/Red_MPUDP/internal/transport/udp"
)

// benchPair builds a loopback client/server pair. No privilege required.
func benchPair(b *testing.B) (*udp.Client, *udp.Server) {
	b.Helper()
	srv, err := udp.Listen(udp.ServerConfig{Listen: netip.AddrPortFrom(loopback, 0)})
	if err != nil {
		b.Fatalf("Listen: %v", err)
	}
	cli, err := udp.Dial(udp.ClientConfig{Server: srv.LocalAddr(), LocalAddr: loopback})
	if err != nil {
		srv.Close()
		b.Fatalf("Dial: %v", err)
	}
	b.Cleanup(func() { cli.Close(); srv.Close() })
	return cli, srv
}

// UDP-48: send allocations. The caller's buffer is reused across iterations, so
// anything reported here is allocated by the transport itself.
func BenchmarkSend(b *testing.B) {
	cli, srv := benchPair(b)
	pkt := make([]byte, 1200)
	ctx := context.Background()

	// Drain the server so its receive buffer cannot fill and block the sender.
	// The drain uses a plain Background context: a per-iteration
	// context.WithTimeout here would allocate on the benchmark's own goroutines
	// and show up as send cost that the transport never paid.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		buf := make([]byte, 2048)
		for {
			if _, _, err := srv.ReadInto(context.Background(), buf); err != nil {
				return // Close at cleanup ends the loop
			}
		}
	}()
	b.Cleanup(func() { srv.Close(); <-drained })

	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.WriteTo(ctx, pkt, transport.Endpoint{}); err != nil {
			b.Fatalf("WriteTo: %v", err)
		}
	}
}

// UDP-47: receive allocations, measured on the datagram that actually arrives.
func BenchmarkReceive(b *testing.B) {
	cli, srv := benchPair(b)
	pkt := make([]byte, 1200)
	dst := make([]byte, 2048)
	ctx := context.Background()

	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.WriteTo(ctx, pkt, transport.Endpoint{}); err != nil {
			b.Fatalf("WriteTo: %v", err)
		}
		if _, _, err := srv.ReadInto(ctx, dst); err != nil {
			b.Fatalf("ReadInto: %v", err)
		}
	}
}

// UDP-49: baseline throughput for one socket — a send followed by its receive.
// The number is machine- and loopback-specific; it is a recorded baseline, not
// a threshold anything asserts.
func BenchmarkSendReceiveRoundTrip(b *testing.B) {
	cli, srv := benchPair(b)
	pkt := make([]byte, 1200)
	dst := make([]byte, 2048)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.WriteTo(ctx, pkt, transport.Endpoint{}); err != nil {
			b.Fatalf("WriteTo: %v", err)
		}
		if _, _, err := srv.ReadInto(ctx, dst); err != nil {
			b.Fatalf("ReadInto: %v", err)
		}
	}
	b.StopTimer()
	if b.N > 0 {
		pps := float64(b.N) / b.Elapsed().Seconds()
		b.ReportMetric(pps, "packets/s")
	}
}
