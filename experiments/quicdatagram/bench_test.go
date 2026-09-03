package quicdatagram

import (
	"context"
	"encoding/binary"
	"sort"
	"sync"
	"testing"
	"time"
)

const innerPacket = 1200 // representative RED_MPUDP outer datagram payload

// SPIKE-57: p50/p95/p99/p99.9 datagram echo RTT at the design §17.4 target rate
// of 10,000 packets/s, over loopback (best case: no real path delay).
func TestLatencyDistributionAtTargetRate(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	lb := newLoopback(t, true)
	defer lb.stop()

	const (
		rate  = 10_000 // packets/s offered
		count = 20_000
	)
	interval := time.Second / rate

	var mu sync.Mutex
	sendAt := make(map[uint64]time.Time, 8192)
	rtts := make([]time.Duration, 0, count)

	// receiver goroutine
	recvDone := make(chan struct{})
	go func() {
		defer close(recvDone)
		for i := 0; i < count; i++ {
			d, err := lb.client.ReceiveDatagram(context.Background())
			if err != nil {
				return
			}
			seq := binary.BigEndian.Uint64(d[:8])
			mu.Lock()
			if s, ok := sendAt[seq]; ok {
				rtts = append(rtts, time.Since(s))
				delete(sendAt, seq)
			}
			mu.Unlock()
		}
	}()

	// sender goroutine, paced
	start := time.Now()
	go func() {
		buf := make([]byte, innerPacket)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for seq := uint64(0); seq < count; seq++ {
			<-ticker.C
			binary.BigEndian.PutUint64(buf[:8], seq)
			mu.Lock()
			sendAt[seq] = time.Now()
			mu.Unlock()
			if err := lb.client.SendDatagram(buf); err != nil {
				t.Errorf("SendDatagram: %v", err)
				return
			}
		}
	}()

	select {
	case <-recvDone:
	case <-time.After(30 * time.Second):
		t.Fatal("did not receive all echoes in 30s")
	}
	elapsed := time.Since(start)

	if len(rtts) < count*9/10 {
		t.Fatalf("only %d/%d datagrams echoed back", len(rtts), count)
	}
	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
	pct := func(p float64) time.Duration { return rtts[int(float64(len(rtts)-1)*p)] }
	t.Logf("loopback datagram echo RTT over %d samples at ~%.0f pps (elapsed %v):",
		len(rtts), float64(count)/elapsed.Seconds(), elapsed)
	t.Logf("  p50=%v p95=%v p99=%v p99.9=%v max=%v",
		pct(0.50), pct(0.95), pct(0.99), pct(0.999), rtts[len(rtts)-1])
}

// SPIKE-58: datagram send throughput and allocations.
func BenchmarkDatagramSend(b *testing.B) {
	lb := newLoopback(b, false)
	defer lb.stop()

	// drain on the server so the send queue never wedges
	go func() {
		for {
			if _, err := lb.server.ReceiveDatagram(context.Background()); err != nil {
				return
			}
		}
	}()

	buf := make([]byte, innerPacket)
	b.SetBytes(innerPacket)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := lb.client.SendDatagram(buf); err != nil {
			b.Fatalf("SendDatagram: %v", err)
		}
	}
}

// BenchmarkDatagramEcho measures full send+echo+receive.
func BenchmarkDatagramEcho(b *testing.B) {
	lb := newLoopback(b, true)
	defer lb.stop()

	buf := make([]byte, innerPacket)
	b.SetBytes(innerPacket)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := lb.client.SendDatagram(buf); err != nil {
			b.Fatal(err)
		}
		if _, err := lb.client.ReceiveDatagram(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
