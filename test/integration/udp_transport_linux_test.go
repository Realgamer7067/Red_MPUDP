//go:build linux && integration

package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestUDPPathsBindToTheirOwnInterface covers UDP-41..44 and the gate "interface
// binding and marking are proven independently for both paths": a socket bound
// to path A must put its datagrams on pa0 and never on pb0, and vice versa.
//
// Egress is proven by capturing on both interfaces at once while exactly one
// path sends, so the assertion is "seen here AND not seen there" rather than
// merely "something was sent".
func TestUDPPathsBindToTheirOwnInterface(t *testing.T) {
	skipUnlessPrivileged(t)
	requireBinary(t, "tcpdump")

	top := NewTopology(t)
	defer top.Close()

	const port = 51820
	cases := []struct {
		name       string
		iface      string
		local      string
		server     string
		otherIface string
	}{
		{"path A", "pa0", pathAClient, pathAServer, "pb0"},
		{"path B", "pb0", pathBClient, pathBServer, "pa0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			own := startCapture(t, nsFull(keyClient), tc.iface, port)
			other := startCapture(t, nsFull(keyClient), tc.otherIface, port)

			arg := fmt.Sprintf("%s,%s,%s:%d,send", tc.iface, tc.local, tc.server, port)
			if out, err := helperCmd(nsFull(keyClient), "udp-path", arg).CombinedOutput(); err != nil {
				t.Fatalf("udp-path on %s: %v\n%s", tc.iface, err, out)
			}

			if n := own.count(t); n == 0 {
				t.Fatalf("no datagram left %s; SO_BINDTODEVICE did not pin egress", tc.iface)
			}
			if n := other.count(t); n != 0 {
				t.Fatalf("%d datagram(s) leaked onto %s from a socket bound to %s",
					n, tc.otherIface, tc.iface)
			}
		})
	}
}

// TestUDPOversizeAndTruncation covers UDP-45 and the gate "oversized/truncated
// packets cannot be mistaken for valid packets" on a real two-namespace path:
// the sender refuses its own oversized datagram, and a datagram larger than the
// receiver's buffer is reported truncated with no bytes returned.
func TestUDPOversizeAndTruncation(t *testing.T) {
	skipUnlessPrivileged(t)

	top := NewTopology(t)
	defer top.Close()

	const port = 51821
	sink := top.spawnHelper(t, nsFull(keyServer), "udp-sink",
		fmt.Sprintf("%s:%d,1,64", pathAServer, port))
	sink.waitReadyLine(t, 5*time.Second)

	// Two distinct properties, deliberately not conflated:
	//
	//   wire-oversize  sends a datagram the socket WILL transmit (below
	//                  MaxDatagramSize) but that overflows the sink's 64-byte
	//                  buffer, so the sink observes MSG_TRUNC. A payload above
	//                  MaxDatagramSize could never prove this — it is rejected
	//                  locally and never reaches the wire at all.
	//   local-oversize checks the separate local rejection above MaxDatagramSize.
	arg := fmt.Sprintf("pa0,%s,%s:%d,wire-oversize+local-oversize", pathAClient, pathAServer, port)
	if out, err := helperCmd(nsFull(keyClient), "udp-path", arg).CombinedOutput(); err != nil {
		t.Fatalf("udp-path: %v\n%s", err, out)
	}

	out, err := sink.wait()
	if err != nil {
		t.Fatalf("udp-sink: %v\n%s", err, out)
	}
	if !strings.Contains(out, "recv truncated") {
		t.Fatalf("sink did not report truncation for a datagram larger than its buffer:\n%s", out)
	}
}

// TestUDPReducedPathMTUIsReported covers UDP-46: with the path MTU lowered, a
// datagram above it must produce EMSGSIZE or an error-queue event, never
// silent fragmentation.
func TestUDPReducedPathMTUIsReported(t *testing.T) {
	skipUnlessPrivileged(t)

	top := NewTopology(t)
	defer top.Close()

	// Lower the client's path-A MTU below the datagram the helper will send.
	if err := ipNS(nsFull(keyClient), "link", "set", "dev", "pa0", "mtu", "1280"); err != nil {
		t.Fatalf("lower pa0 mtu: %v", err)
	}

	// A real sink must be listening. Without one, a port-unreachable ICMP would
	// arrive and could be mistaken for PMTU feedback, so the test would pass for
	// entirely the wrong reason.
	const port = 51822
	sink := top.spawnHelper(t, nsFull(keyServer), "udp-sink",
		fmt.Sprintf("%s:%d,1,2048", pathAServer, port))
	sink.waitReadyLine(t, 5*time.Second)

	arg := fmt.Sprintf("pa0,%s,%s:%d,pmtu", pathAClient, pathAServer, port)
	out, err := helperCmd(nsFull(keyClient), "udp-path", arg).CombinedOutput()
	if err != nil {
		t.Fatalf("a 1400-byte datagram over a 1280-MTU path was not reported: %v\n%s", err, out)
	}
	// The helper itself requires the event to quote this path's server and to
	// carry a credible next-hop MTU (576 <= mtu < 1400), so a stray ICMP cannot
	// satisfy it.
	t.Logf("pmtu feedback: %s", out)
}
