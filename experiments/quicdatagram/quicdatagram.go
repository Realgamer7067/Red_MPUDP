// Package quicdatagram is an isolated M02 Phase 0 spike (SPIKE-49..59). It has
// its own go.mod so github.com/quic-go/quic-go never enters the main module.
//
// It evaluates whether QUIC DATAGRAM (RFC 9221) via quic-go meets the RED_MPUDP
// data-plane requirements without a private fork: a caller-owned,
// interface-bound UDP socket; unreliable datagrams with no stream fallback;
// visibility of the current maximum datagram size; per-datagram delivery/loss
// feedback; and a send path RED_MPUDP can bound, prioritise, and cancel.
//
// The transport decision (docs/decisions/0001-v1-transport.md) rejected QUIC;
// this module is retained as that decision's evidence (SPIKE-66) and stays out
// of the main module / CI via its own go.mod.
package quicdatagram

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// throttledPacketConn rate-limits WriteTo to bytesPerSec, so the QUIC send path
// backs up and the fixed datagram send queue can be observed filling (SPIKE-56).
type throttledPacketConn struct {
	net.PacketConn
	bytesPerSec float64

	mu     sync.Mutex
	credit float64
	last   time.Time
}

func newThrottledPacketConn(pc net.PacketConn, bytesPerSec float64) *throttledPacketConn {
	return &throttledPacketConn{PacketConn: pc, bytesPerSec: bytesPerSec}
}

func (c *throttledPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	now := time.Now()
	if c.last.IsZero() {
		c.last = now
	}
	c.credit += c.bytesPerSec * now.Sub(c.last).Seconds()
	if c.credit > c.bytesPerSec { // cap burst at 1 s
		c.credit = c.bytesPerSec
	}
	c.last = now
	need := float64(len(p))
	var wait time.Duration
	if c.credit < need {
		wait = time.Duration((need - c.credit) / c.bytesPerSec * float64(time.Second))
		c.credit = 0
	} else {
		c.credit -= need
	}
	c.mu.Unlock()
	if wait > 0 {
		time.Sleep(wait)
	}
	return c.PacketConn.WriteTo(p, addr)
}

// pass through the optional optimisation interfaces quic-go probes for.
func (c *throttledPacketConn) SetReadBuffer(n int) error {
	if u, ok := c.PacketConn.(interface{ SetReadBuffer(int) error }); ok {
		return u.SetReadBuffer(n)
	}
	return nil
}

func (c *throttledPacketConn) SetWriteBuffer(n int) error {
	if u, ok := c.PacketConn.(interface{ SetWriteBuffer(int) error }); ok {
		return u.SetWriteBuffer(n)
	}
	return nil
}

func (c *throttledPacketConn) SyscallConn() (syscall.RawConn, error) {
	if u, ok := c.PacketConn.(interface {
		SyscallConn() (syscall.RawConn, error)
	}); ok {
		return u.SyscallConn()
	}
	return nil, errors.New("no SyscallConn")
}

// ALPN used by the spike.
const ALPN = "red-mpudp-quic-spike"

// selfSignedTLS returns matched server and client TLS configs for loopback.
func selfSignedTLS() (server, client *tls.Config, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "red-mpudp-spike"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	server = &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{ALPN}}
	client = &tls.Config{InsecureSkipVerify: true, NextProtos: []string{ALPN}} //nolint:gosec // loopback spike
	return server, client, nil
}

// BoundUDPConn creates a loopback IPv4 UDP socket, optionally applying
// SO_BINDTODEVICE and SO_MARK the same way internal/transport/udp will in M07.
// Both socket options require CAP_NET_ADMIN; callers handle unix.EPERM.
func BoundUDPConn(iface string, mark int) (*net.UDPConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var opErr error
			if err := c.Control(func(fd uintptr) {
				if iface != "" {
					opErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, iface)
					if opErr != nil {
						return
					}
				}
				if mark != 0 {
					opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
				}
			}); err != nil {
				return err
			}
			return opErr
		},
	}
	pc, err := lc.ListenPacket(context.Background(), "udp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	uc, ok := pc.(*net.UDPConn)
	if !ok {
		pc.Close()
		return nil, errors.New("not a *net.UDPConn")
	}
	return uc, nil
}
