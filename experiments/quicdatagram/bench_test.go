package quicdatagram

import (
	"context"
	"encoding/binary"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
)

const innerPacket = 1200 // representative RED_MPUDP outer datagram payload

// SPIKE-57: datagram echo RTT distribution over loopback (best case — no real
// path delay), under a paced offered load. The offered target is the design
// §17.4 end-to-end figure of 10,000 packets/s; the test records the achieved
// rate and asserts a floor. QUIC is the rejected alternate (SPIKE-64), so these
// numbers matter only as comparative per-datagram overhead vs the Noise
// transport benchmarks (test/results/phase0/crypto-bench.txt).
func TestLatencyDistributionUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	lb := newLoopback(t, true)
	defer lb.stop()

	const (
		offeredPPS  = 10_000
		count       = 20_000
		minAchieved = 8_000 // floor: fail if the harness/QUIC can't sustain this
	)
	interval := time.Second / offeredPPS

	var mu sync.Mutex
	sendAt := make(map[uint64]time.Time, 16384)
	rtts := make([]time.Duration, 0, count)

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

	// Busy-deadline pacer: time.Ticker cannot deliver a reliable 100 µs tick,
	// so schedule each send against an absolute deadline and yield until due.
	start := time.Now()
	go func() {
		buf := make([]byte, innerPacket)
		for seq := uint64(0); seq < count; seq++ {
			due := start.Add(time.Duration(seq) * interval)
			for time.Now().Before(due) {
				runtime.Gosched()
			}
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
	achieved := float64(count) / elapsed.Seconds()

	if len(rtts) < count*9/10 {
		t.Fatalf("only %d/%d datagrams echoed back", len(rtts), count)
	}
	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
	pct := func(p float64) time.Duration { return rtts[int(float64(len(rtts)-1)*p)] }
	t.Logf("loopback datagram echo RTT, %d samples, offered %d pps, achieved ~%.0f pps (elapsed %v):",
		len(rtts), offeredPPS, achieved, elapsed)
	t.Logf("  p50=%v p95=%v p99=%v p99.9=%v max=%v",
		pct(0.50), pct(0.95), pct(0.99), pct(0.999), rtts[len(rtts)-1])

	if achieved < minAchieved {
		t.Errorf("sustained only ~%.0f pps (< %d floor); harness or QUIC stack cannot keep up", achieved, minAchieved)
	}
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
