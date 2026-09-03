//go:build linux && integration

package integration

import (
	"context"
	"net"
	"os"
	"time"
)

// runHelper is invoked from TestMain when RED_MPUDP_HELPER is set: the process
// is a re-exec of the test binary running inside internet-ns as a target
// server. It runs until killed.
func runHelper(mode, addr string) {
	switch mode {
	case "udp-echo":
		udpEcho(addr)
	case "tcp-echo":
		tcpEcho(addr)
	case "dns":
		dnsStub(addr)
	default:
		os.Exit(2)
	}
}

func udpEcho(addr string) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		os.Exit(1)
	}
	buf := make([]byte, 65535)
	for {
		n, peer, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		_, _ = pc.WriteTo(buf[:n], peer)
	}
}

func tcpEcho(addr string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		os.Exit(1)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			b := make([]byte, 4096)
			for {
				n, err := c.Read(b)
				if err != nil {
					return
				}
				if _, err := c.Write(b[:n]); err != nil {
					return
				}
			}
		}(conn)
	}
}

// dnsStub answers every A query with a fixed address so tests are
// deterministic (HARNESS-21). It parses just enough of the query to echo the
// ID and question.
func dnsStub(addr string) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		os.Exit(1)
	}
	const fixedA = "\x0a\x4d\x00\x63" // 10.77.0.99
	buf := make([]byte, 512)
	for {
		n, peer, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < 12 {
			continue
		}
		q := append([]byte(nil), buf[:n]...)
		q[2] |= 0x80 // QR = response
		q[3] |= 0x00
		// ANCOUNT = 1
		q[6], q[7] = 0x00, 0x01
		// Answer: pointer to question name, type A, class IN, ttl 60, rdlen 4.
		ans := []byte{0xc0, 0x0c, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x3c, 0x00, 0x04}
		ans = append(ans, fixedA...)
		_, _ = pc.WriteTo(append(q, ans...), peer)
	}
}

// waitDial retries a TCP dial until it succeeds or ctx expires; harness tests
// use it after StartEchoTargets.
func waitDial(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	for {
		c, err := d.DialContext(ctx, network, addr)
		if err == nil {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(25 * time.Millisecond):
		}
	}
}
