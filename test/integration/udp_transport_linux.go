//go:build linux && integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
	"github.com/Realgamer7067/Red_MPUDP/internal/transport/udp"
)

// udpPathCheck is the "udp-path" helper mode (UDP-41..46). It runs inside
// client-ns and drives one real path socket over the named interface, proving
// that a datagram sent on that socket leaves by that interface and no other.
//
// arg is "<iface>,<localIP>,<serverAddrPort>,<checks>" where checks is a
// "+"-separated list: send, oversize, pmtu.
func udpPathCheck(arg string) int {
	parts := strings.Split(arg, ",")
	if len(parts) != 4 {
		fmt.Fprintf(os.Stderr, "udp-path: want <iface>,<local>,<server>,<checks>, got %q\n", arg)
		return 1
	}
	iface, localStr, serverStr, checks := parts[0], parts[1], parts[2], parts[3]

	local, err := netip.ParseAddr(localStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "udp-path: local:", err)
		return 1
	}
	server, err := netip.ParseAddrPort(serverStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "udp-path: server:", err)
		return 1
	}

	// UDP-13: SO_BINDTODEVICE pins egress to this interface. Combined with the
	// tcpdump capture the test runs on each interface, this is what proves a
	// path's packets leave by its own uplink and nowhere else (UDP-42, UDP-44).
	cli, err := udp.Dial(udp.ClientConfig{
		Interface: iface,
		LocalAddr: local,
		Server:    server,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "udp-path: dial on %s: %v\n", iface, err)
		return 1
	}
	defer cli.Close()

	diag := cli.Diagnostics()
	if diag.IfName != iface || diag.IfIndex == 0 {
		fmt.Fprintf(os.Stderr, "udp-path: diagnostics %+v do not name %s\n", diag, iface)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, check := range strings.Split(checks, "+") {
		switch check {
		case "send": // UDP-41, UDP-43
			if err := cli.WriteTo(ctx, []byte("udp-path-probe"), transport.Endpoint{}); err != nil {
				fmt.Fprintf(os.Stderr, "udp-path: send on %s: %v\n", iface, err)
				return 1
			}

		case "oversize": // UDP-45 (send side of the truncation proof)
			big := make([]byte, 2000)
			if err := cli.WriteTo(ctx, big, transport.Endpoint{}); !errors.Is(err, transport.ErrOversize) {
				fmt.Fprintf(os.Stderr, "udp-path: oversize send returned %v, want ErrOversize\n", err)
				return 1
			}

		case "pmtu": // UDP-46
			// The caller lowered this path's MTU. With IP_MTU_DISCOVER set to
			// PMTUDISC_DO the kernel must refuse a datagram above it rather than
			// fragment: either synchronously with EMSGSIZE, or by posting an
			// error-queue event. Both are acceptable; silence is not.
			payload := make([]byte, 1400)
			werr := cli.WriteTo(ctx, payload, transport.Endpoint{})
			if errors.Is(werr, transport.ErrOversize) {
				fmt.Fprintln(os.Stderr, "udp-path: pmtu observed synchronously (EMSGSIZE)")
				continue
			}
			peCtx, peCancel := context.WithTimeout(ctx, 3*time.Second)
			pe, perr := cli.ReadPathError(peCtx)
			peCancel()
			if perr != nil {
				fmt.Fprintf(os.Stderr, "udp-path: a %d-byte datagram over a reduced-MTU path "+
					"produced neither EMSGSIZE (write err: %v) nor an error-queue event: %v\n",
					len(payload), werr, perr)
				return 1
			}
			fmt.Fprintf(os.Stderr, "udp-path: pmtu observed on the error queue: %s\n", pe)

		default:
			fmt.Fprintf(os.Stderr, "udp-path: unknown check %q\n", check)
			return 1
		}
	}
	return 0
}

// udpSinkCheck is the "udp-sink" helper mode: it binds the shared server socket
// in server-ns, reports the metadata of the datagrams it receives, and exits.
// arg is "<listenAddrPort>,<count>,<bufSize>".
func udpSinkCheck(arg string) int {
	parts := strings.Split(arg, ",")
	if len(parts) != 3 {
		fmt.Fprintf(os.Stderr, "udp-sink: want <listen>,<count>,<bufsize>, got %q\n", arg)
		return 1
	}
	listen, err := netip.ParseAddrPort(parts[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "udp-sink: listen:", err)
		return 1
	}
	var count, bufSize int
	if _, err := fmt.Sscanf(parts[1], "%d", &count); err != nil {
		fmt.Fprintln(os.Stderr, "udp-sink: count:", err)
		return 1
	}
	if _, err := fmt.Sscanf(parts[2], "%d", &bufSize); err != nil {
		fmt.Fprintln(os.Stderr, "udp-sink: bufsize:", err)
		return 1
	}

	srv, err := udp.Listen(udp.ServerConfig{Listen: listen})
	if err != nil {
		fmt.Fprintln(os.Stderr, "udp-sink: listen:", err)
		return 1
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buf := make([]byte, bufSize)
	for i := 0; i < count; i++ {
		n, meta, err := srv.ReadInto(ctx, buf)
		switch {
		case errors.Is(err, transport.ErrTruncated):
			// UDP-45: the oversized datagram must be reported as truncated, must
			// return no bytes, and must never be mistaken for a valid packet.
			if n != 0 || !meta.Truncated {
				fmt.Fprintf(os.Stderr, "udp-sink: truncated read returned n=%d truncated=%v\n", n, meta.Truncated)
				return 1
			}
			fmt.Printf("recv truncated src=%s local=%s ifindex=%d\n", meta.Source, meta.LocalAddr, meta.IfIndex)
		case err != nil:
			fmt.Fprintf(os.Stderr, "udp-sink: read %d: %v\n", i, err)
			return 1
		default:
			// UDP-27, UDP-31: the local address and receive ifindex come from
			// ancillary data, so the server can tell which uplink a datagram
			// arrived on even though one socket serves every path.
			if !meta.LocalAddr.IsValid() || meta.IfIndex == 0 {
				fmt.Fprintf(os.Stderr, "udp-sink: missing pktinfo: %+v\n", meta)
				return 1
			}
			fmt.Printf("recv ok src=%s local=%s ifindex=%d len=%d\n", meta.Source, meta.LocalAddr, meta.IfIndex, n)
		}
	}
	return 0
}
