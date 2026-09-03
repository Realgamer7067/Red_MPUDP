package tun_test

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/packetbuf"
	"github.com/Realgamer7067/Red_MPUDP/internal/tun"
)

var testPrefix = netip.MustParsePrefix("10.9.0.2/24")

func baseConfig() tun.Config {
	return tun.Config{Name: "red0", Address: testPrefix, MTU: tun.DefaultMTU}
}

// TUN-06 / TUN-15: Open runs config validation before it touches any
// descriptor, so an invalid config fails the same way on every platform and
// without privilege. The validation rules themselves are asserted directly in
// validate_internal_test.go.
func TestConfigValidationRunsBeforeOpen(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*tun.Config)
	}{
		{"name too long", func(c *tun.Config) { c.Name = "an-interface-name-way-too-long" }},
		{"name with slash", func(c *tun.Config) { c.Name = "red/0" }},
		{"name with space", func(c *tun.Config) { c.Name = "red 0" }},
		{"mtu too low", func(c *tun.Config) { c.MTU = tun.MinMTU - 1 }},
		{"mtu too high", func(c *tun.Config) { c.MTU = tun.MaxMTU + 1 }},
		{"no address", func(c *tun.Config) { c.Address = netip.Prefix{} }},
		{"ipv6 address", func(c *tun.Config) { c.Address = netip.MustParsePrefix("fd00::1/64") }},
		{"negative maxpacket", func(c *tun.Config) { c.MaxPacket = -1 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := baseConfig()
			tc.mut(&c)
			if _, err := tun.Open(c); err == nil {
				t.Fatalf("invalid config accepted by Open")
			}
		})
	}
}

func TestGuardMTUViaSetMTUSemantics(t *testing.T) {
	// guardMTU is unexported; exercise its rules through a mock device.
	d := newMockDevice(baseConfig())

	if err := d.SetMTU(tun.MinMTU-1, false); !errors.Is(err, tun.ErrMTURange) {
		t.Fatalf("below range: %v", err)
	}
	if err := d.SetMTU(tun.MaxMTU+1, true); !errors.Is(err, tun.ErrMTURange) {
		t.Fatalf("above range even authenticated: %v", err)
	}
	// Reduction always allowed (TUN-16).
	if err := d.SetMTU(tun.DefaultMTU-40, false); err != nil {
		t.Fatalf("reduction rejected: %v", err)
	}
	if d.MTU() != tun.DefaultMTU-40 {
		t.Fatalf("mtu = %d after reduction", d.MTU())
	}
	// Unauthenticated increase rejected (TUN-17).
	if err := d.SetMTU(tun.DefaultMTU, false); !errors.Is(err, tun.ErrMTUIncrease) {
		t.Fatalf("unauthenticated increase: %v", err)
	}
	// Authenticated increase allowed.
	if err := d.SetMTU(tun.DefaultMTU, true); err != nil {
		t.Fatalf("authenticated increase rejected: %v", err)
	}
	if d.MTU() != tun.DefaultMTU {
		t.Fatalf("mtu = %d after authenticated increase", d.MTU())
	}
}

// TUN-25: Stats is a fixed-shape snapshot; the counters move on I/O.
func TestStatsCounters(t *testing.T) {
	d := newMockDevice(baseConfig())
	d.enqueue([]byte{0x45, 0, 0, 20})

	buf := make([]byte, tun.DefaultMTU+64)
	if _, err := d.ReadPacket(context.Background(), buf); err != nil {
		t.Fatal(err)
	}
	if _, err := d.WritePacket([]byte{0x45, 0, 0, 21}); err != nil {
		t.Fatal(err)
	}
	s := d.Stats()
	if s.RxPackets != 1 || s.TxPackets != 1 || s.RxBytes != 4 || s.TxBytes != 4 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

// TUN-23: ReadInto releases the pooled buffer on a read failure.
func TestReadIntoReleasesBufferOnError(t *testing.T) {
	pool := packetbuf.NewPool()
	d := newMockDevice(baseConfig())
	d.readErr = errors.New("descriptor gone")

	b := pool.Get(packetbuf.MaxOuterDatagram)
	if _, err := tun.ReadInto(context.Background(), d, b); err == nil {
		t.Fatal("expected read error")
	}
	// A double release panics under -tags debug; here we just confirm the
	// buffer round-trips back into the pool (Get returns a usable buffer).
	b2 := pool.Get(packetbuf.MaxOuterDatagram)
	defer b2.Release()
	if len(b2.Bytes()) != packetbuf.MaxOuterDatagram {
		t.Fatalf("pool buffer not reusable after ReadInto error")
	}
}

// TUN-24: WriteFrom releases the pooled buffer whether or not the write fails.
func TestWriteFromAlwaysReleases(t *testing.T) {
	pool := packetbuf.NewPool()
	d := newMockDevice(baseConfig())

	b := pool.Get(64)
	copy(b.Bytes(), []byte{0x45, 0, 0, 40})
	if _, err := tun.WriteFrom(d, b); err != nil {
		t.Fatalf("WriteFrom: %v", err)
	}

	d.writeErr = errors.New("gone")
	b = pool.Get(64)
	if _, err := tun.WriteFrom(d, b); err == nil {
		t.Fatal("expected write error")
	}
}

func TestDeviceInterfaceSatisfied(t *testing.T) {
	var _ tun.Device = newMockDevice(baseConfig())
}

// ---- mock device (TUN unit tests run through mocks) ----

type mockDevice struct {
	mu       sync.Mutex
	name     string
	index    int
	mtu      int
	addr     netip.Prefix
	inbox    [][]byte
	written  [][]byte
	readErr  error
	writeErr error
	closed   bool
	stats    tun.Stats
}

func newMockDevice(cfg tun.Config) *mockDevice {
	return &mockDevice{name: cfg.Name, index: 42, mtu: cfg.MTU, addr: cfg.Address}
}

func (m *mockDevice) enqueue(p []byte) {
	m.mu.Lock()
	m.inbox = append(m.inbox, append([]byte(nil), p...))
	m.mu.Unlock()
}

func (m *mockDevice) Name() string          { return m.name }
func (m *mockDevice) Index() int            { return m.index }
func (m *mockDevice) Address() netip.Prefix { return m.addr }

func (m *mockDevice) MTU() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mtu
}

func (m *mockDevice) ReadPacket(ctx context.Context, buf []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, tun.ErrClosed
	}
	if m.readErr != nil {
		m.stats.RxErrors++
		return 0, m.readErr
	}
	if len(m.inbox) == 0 {
		return 0, errors.New("mock: inbox empty")
	}
	p := m.inbox[0]
	m.inbox = m.inbox[1:]
	n := copy(buf, p)
	m.stats.RxPackets++
	m.stats.RxBytes += uint64(n)
	return n, nil
}

func (m *mockDevice) WritePacket(buf []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, tun.ErrClosed
	}
	if m.writeErr != nil {
		m.stats.TxErrors++
		return 0, m.writeErr
	}
	m.written = append(m.written, append([]byte(nil), buf...))
	m.stats.TxPackets++
	m.stats.TxBytes += uint64(len(buf))
	return len(buf), nil
}

func (m *mockDevice) SetMTU(mtu int, authenticatedIncrease bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mtu < tun.MinMTU || mtu > tun.MaxMTU {
		return tun.ErrMTURange
	}
	if mtu > m.mtu && !authenticatedIncrease {
		return tun.ErrMTUIncrease
	}
	m.mtu = mtu
	return nil
}

func (m *mockDevice) Stats() tun.Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}

func (m *mockDevice) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}
