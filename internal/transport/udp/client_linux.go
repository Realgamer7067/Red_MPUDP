//go:build linux

package udp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
	"golang.org/x/sys/unix"
)

// ClientConfig describes one path socket (design §11.1).
type ClientConfig struct {
	// Interface is the physical uplink to bind to with SO_BINDTODEVICE. Empty
	// skips the binding, which is only appropriate in tests.
	Interface string
	// FWMark is the path's unique SO_MARK. Zero means no mark. A non-zero mark
	// that the kernel refuses is fatal: a path that silently lost its mark would
	// egress through the wrong table and could recurse through the tunnel.
	FWMark uint32
	// LocalAddr is the source address to bind. An invalid address lets the
	// kernel choose, and the effective address is read back into Diagnostics.
	LocalAddr netip.Addr
	// Server is the literal endpoint this path connects to.
	Server netip.AddrPort
	// ReceiveBuffer and SendBuffer are SO_RCVBUF/SO_SNDBUF requests. Zero leaves
	// the kernel default. The effective values are read back (UDP-21).
	ReceiveBuffer int
	SendBuffer    int
	// MaxDatagram bounds WriteTo. Zero uses DefaultMaxDatagram.
	MaxDatagram int
}

// DefaultMaxDatagram is the largest outer datagram v1 sends (design §10: a
// 1400-byte inner MTU plus 60 bytes of transport overhead, with headroom).
const DefaultMaxDatagram = 1500

// ErrInterfaceGone is returned when the configured interface cannot be resolved
// to an ifindex, including when it disappears between resolution and use
// (UDP-11).
var ErrInterfaceGone = errors.New("udp: interface is not present")

// Client is one interface-bound, connected UDP path socket.
type Client struct {
	*socket
	server netip.AddrPort
}

var _ transport.DatagramIO = (*Client)(nil)

// Dial creates one path socket: resolve the interface, open a nonblocking
// IPv4 UDP socket, bind it to the device and mark, bind the source address,
// connect to the server, set do-not-fragment and extended errors, size the
// buffers and read back what the kernel actually granted (UDP-09..21).
//
// Every failure after the descriptor exists closes it before returning.
func Dial(cfg ClientConfig) (*Client, error) {
	if !cfg.Server.IsValid() || !cfg.Server.Addr().Is4() {
		return nil, fmt.Errorf("udp: server %s must be a valid IPv4 endpoint", cfg.Server)
	}
	if cfg.MaxDatagram <= 0 {
		cfg.MaxDatagram = DefaultMaxDatagram
	}

	// UDP-10, UDP-11: resolve the interface before anything else exists.
	ifIndex := 0
	if cfg.Interface != "" {
		ifi, err := net.InterfaceByName(cfg.Interface)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInterfaceGone, cfg.Interface, err)
		}
		ifIndex = ifi.Index
	}

	// UDP-12: close-on-exec and nonblocking from creation, so no window exists
	// where a fork could inherit the descriptor.
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_UDP)
	if err != nil {
		return nil, fmt.Errorf("udp: socket: %w", err)
	}
	fail := func(format string, a ...any) (*Client, error) {
		unix.Close(fd)
		return nil, fmt.Errorf(format, a...)
	}

	if cfg.Interface != "" { // UDP-13
		if err := unix.SetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE, cfg.Interface); err != nil {
			// ENODEV means the interface went away between the lookup above and
			// this call.
			if errors.Is(err, unix.ENODEV) || errors.Is(err, unix.ENXIO) {
				return fail("%w: %s vanished before SO_BINDTODEVICE: %w", ErrInterfaceGone, cfg.Interface, err)
			}
			return fail("udp: SO_BINDTODEVICE %s: %w", cfg.Interface, err)
		}
		// UDP-10/UDP-11 race: the name was resolved to an ifindex before the
		// socket existed. If the interface was deleted and recreated in that
		// window, the name now refers to a different device with a different
		// index, and the socket would be bound to a link this path never chose.
		// Re-resolve and require the same index.
		again, err := net.InterfaceByName(cfg.Interface)
		if err != nil {
			return fail("%w: %s disappeared during setup: %w", ErrInterfaceGone, cfg.Interface, err)
		}
		if again.Index != ifIndex {
			return fail("%w: %s was recreated during setup (ifindex %d -> %d)",
				ErrInterfaceGone, cfg.Interface, ifIndex, again.Index)
		}
	}
	if cfg.FWMark != 0 { // UDP-14
		// EPERM here means the process lacks CAP_NET_ADMIN. Treating that as
		// tolerable would hand back a socket that egresses through the wrong
		// routing table, so it is fatal.
		if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_MARK, int(cfg.FWMark)); err != nil {
			return fail("udp: SO_MARK %#x (needs CAP_NET_ADMIN): %w", cfg.FWMark, err)
		}
	}
	if cfg.LocalAddr.IsValid() { // UDP-15
		if !cfg.LocalAddr.Is4() {
			return fail("udp: local address %s must be IPv4", cfg.LocalAddr)
		}
		if err := unix.Bind(fd, &unix.SockaddrInet4{Addr: cfg.LocalAddr.As4()}); err != nil {
			// EADDRNOTAVAIL is what a bind to the address of an interface that
			// has just gone away looks like.
			if errors.Is(err, unix.EADDRNOTAVAIL) {
				return fail("%w: source %s is no longer present: %w", ErrInterfaceGone, cfg.LocalAddr, err)
			}
			return fail("udp: bind %s: %w", cfg.LocalAddr, err)
		}
	}
	// UDP-16: connect to the literal server endpoint, so the kernel filters
	// sources for us and synchronous errors are attributable to this path.
	if err := connectSyscall(fd, &unix.SockaddrInet4{
		Addr: cfg.Server.Addr().As4(),
		Port: int(cfg.Server.Port()),
	}); err != nil {
		// The interface can be removed after SO_BINDTODEVICE succeeded. The
		// kernel reports that as ENODEV/ENXIO, which is a disappearance rather
		// than a routing failure, so it maps to ErrInterfaceGone too (UDP-11).
		via := ""
		if cfg.Interface != "" {
			via = " via " + cfg.Interface
		}
		if errors.Is(err, unix.ENODEV) || errors.Is(err, unix.ENXIO) {
			return fail("%w: outbound device%s went away before connect to %s: %w",
				ErrInterfaceGone, via, cfg.Server, err)
		}
		if errors.Is(err, unix.ENETUNREACH) || errors.Is(err, unix.EADDRNOTAVAIL) {
			return fail("%w: no route to %s%s: %w", ErrInterfaceGone, cfg.Server, via, err)
		}
		return fail("udp: connect %s: %w", cfg.Server, err)
	}
	// UDP-17: never fragment; an oversized send fails with EMSGSIZE instead
	// (design §10).
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO); err != nil {
		return fail("udp: IP_MTU_DISCOVER: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_RECVERR, 1); err != nil { // UDP-18
		return fail("udp: IP_RECVERR: %w", err)
	}

	bufs, err := applyBuffers(fd, cfg.ReceiveBuffer, cfg.SendBuffer) // UDP-19, UDP-20, UDP-21
	if err != nil {
		return fail("udp: %w", err)
	}

	local, err := localAddrPort(fd)
	if err != nil {
		return fail("udp: getsockname: %w", err)
	}

	s, err := newSocket(fd, "udp:"+cfg.Interface, cfg.MaxDatagram, cfg.Server, Diagnostics{
		LocalAddr: local,
		IfIndex:   ifIndex,
		IfName:    cfg.Interface,
		FWMark:    cfg.FWMark,
		Buffers:   bufs,
	})
	if err != nil {
		// newSocket closes nothing it did not create; fd is still ours unless
		// the *os.File already took it over.
		var owned errFileOwned
		if !errors.As(err, &owned) {
			unix.Close(fd)
		}
		return nil, err
	}
	return &Client{socket: s, server: cfg.Server}, nil
}

// Server is the endpoint this path is connected to.
func (c *Client) Server() netip.AddrPort { return c.server }

// ReadInto reads one datagram (UDP-29..33). The socket is connected, so the
// kernel already discarded anything from another source.
func (c *Client) ReadInto(ctx context.Context, dst []byte) (int, transport.ReceiveMeta, error) {
	if c.isClosed() {
		return 0, transport.ReceiveMeta{}, transport.ErrClosed
	}
	oob := make([]byte, 256)
	n, meta, err := c.recvOne(ctx, dst, oob)
	if err == nil && !meta.Source.IsValid() {
		// A connected socket may omit the source; it can only be the peer.
		meta.Source = c.server
	}
	return n, meta, err
}

// WriteTo sends one datagram to the connected server.
//
// A synchronous EMSGSIZE is surfaced as ErrOversize and the datagram is never
// retried at a smaller size — only authenticated probe results move the PMTU
// (design §10, UDP-38).
func (c *Client) WriteTo(ctx context.Context, pkt []byte, dst transport.Endpoint) error {
	// A closed socket reports ErrClosed before any argument complaint, so a
	// caller shutting down never has to distinguish "bad request" from "gone".
	if c.isClosed() {
		return transport.ErrClosed
	}
	if dst.AddrPort.IsValid() && dst.AddrPort != c.server {
		return fmt.Errorf("udp: path socket is connected to %s, refusing a send to %s", c.server, dst.AddrPort)
	}
	if len(pkt) > c.maxDatagram {
		return fmt.Errorf("%w: %d > %d", transport.ErrOversize, len(pkt), c.maxDatagram)
	}
	err := c.doWrite(ctx, func(fd uintptr) (bool, error) {
		e := unix.Send(int(fd), pkt, unix.MSG_DONTWAIT)
		if e == unix.EAGAIN || e == unix.EWOULDBLOCK || e == unix.EINTR {
			return false, nil
		}
		return true, e
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, transport.ErrClosed) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var errno unix.Errno
	if errors.As(err, &errno) {
		if isPathErrno(errno) {
			c.publishPathError(transport.PathError{Peer: c.server, Local: true})
		}
		if errno == unix.EMSGSIZE {
			return fmt.Errorf("%w: kernel refused %d bytes: %w", transport.ErrOversize, len(pkt), err)
		}
	}
	return fmt.Errorf("udp: send: %w", err)
}
