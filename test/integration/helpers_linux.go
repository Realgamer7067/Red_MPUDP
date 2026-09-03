//go:build linux && integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/journal"
)

// runHelper is invoked from TestMain when RED_MPUDP_HELPER is set: the process
// is a re-exec of the test binary running inside a namespace. The echo/dns
// modes are long-lived targets in internet-ns; probe-tcp is a one-shot
// reachability check; journal-recover exercises JOURNAL-24 entirely inside
// client-ns.
func runHelper(mode, addr string) {
	switch mode {
	case "udp-echo":
		udpEcho(addr)
	case "tcp-echo":
		tcpEcho(addr)
	case "dns":
		dnsStub(addr)
	case "probe-tcp":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, err := waitDial(ctx, "tcp", addr)
		if err != nil {
			os.Exit(1)
		}
		_, _ = c.Write([]byte("ping"))
		c.Close()
		os.Exit(0)
	case "journal-recover":
		os.Exit(journalRecoverInNamespace())
	case "tun-plaintext":
		os.Exit(tunPlaintextCheck())
	default:
		os.Exit(2)
	}
}

// journalRecoverInNamespace runs inside client-ns (JOURNAL-24). It installs one
// route it will claim to own and one it will not, both in table 200, then runs
// journal.Recover with the real LinuxHost and verifies that only the owned
// route was removed. Returns a process exit code.
func journalRecoverInNamespace() int {
	const (
		ownedDst     = "10.99.0.0/24"
		unrelatedDst = "10.98.0.0/24"
		table        = "200"
	)
	sh := func(args ...string) error {
		out, err := exec.Command("ip", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("ip %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	for _, dst := range []string{ownedDst, unrelatedDst} {
		if err := sh("route", "replace", dst, "via", pathAServer, "dev", "pa0", "table", table); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	j := &journal.Journal{
		Schema:     journal.SchemaVersion,
		Role:       journal.RoleClient,
		InstanceID: "red-mpudp-ns-" + runID,
		Routes: []journal.RouteRecord{
			{Table: 200, Dst: ownedDst, Dev: "pa0", Via: pathAServer},
		},
	}
	if _, err := journal.Recover(j, journal.RoleClient, j.InstanceID, journal.LinuxHost{}, nil); err != nil {
		fmt.Fprintln(os.Stderr, "recover:", err)
		return 1
	}

	shown, err := exec.Command("ip", "route", "show", "table", table).CombinedOutput()
	if err != nil {
		fmt.Fprintln(os.Stderr, "route show:", err)
		return 1
	}
	text := string(shown)
	if strings.Contains(text, "10.99.0.0/24") {
		fmt.Fprintln(os.Stderr, "owned route was not removed:\n"+text)
		return 1
	}
	if !strings.Contains(text, "10.98.0.0/24") {
		fmt.Fprintln(os.Stderr, "unrelated route was removed:\n"+text)
		return 1
	}
	return 0
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
