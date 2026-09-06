//go:build debug

package tun_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/packetbuf"
	"github.com/Realgamer7067/Red_MPUDP/internal/tun"
)

// TUN-23 / TUN-24 under -tags debug: packetbuf turns a double Release into a
// panic, so this proves ReadInto and WriteFrom release the pooled buffer
// exactly once on every path — and, on ReadInto's success path, do not release
// it at all (the caller still owns it).
func TestPooledHelpersReleaseExactlyOnce(t *testing.T) {
	pool := packetbuf.NewPool()
	cfg := tun.Config{Name: "red0", Address: testPrefix, MTU: tun.DefaultMTU}

	// ReadInto error path: released once. A second release inside ReadInto
	// would panic before this call returns.
	d := newMockDevice(cfg)
	d.readErr = errors.New("descriptor gone")
	b := pool.Get(packetbuf.MaxOuterDatagram)
	if _, err := tun.ReadInto(context.Background(), d, b); err == nil {
		t.Fatal("expected a read error")
	}

	// ReadInto success path: ReadInto must NOT release; the caller does, and
	// that release must be the first (no panic).
	d2 := newMockDevice(cfg)
	d2.enqueue([]byte{0x45, 0, 0, 20})
	b2 := pool.Get(packetbuf.MaxOuterDatagram)
	if _, err := tun.ReadInto(context.Background(), d2, b2); err != nil {
		t.Fatal(err)
	}
	b2.Release()

	// WriteFrom always releases exactly once, on success and on failure.
	d3 := newMockDevice(cfg)
	bOK := pool.Get(64)
	copy(bOK.Bytes(), []byte{0x45, 0, 0, 40})
	if _, err := tun.WriteFrom(d3, bOK); err != nil {
		t.Fatal(err)
	}
	d3.writeErr = errors.New("gone")
	bErr := pool.Get(64)
	if _, err := tun.WriteFrom(d3, bErr); err == nil {
		t.Fatal("expected a write error")
	}
}
