//go:build linux

package udp

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
	"golang.org/x/sys/unix"
)

// ServerConfig describes the shared listen socket (design §13).
type ServerConfig struct {
	// Listen is the bind address. An unspecified address listens on every
	// interface, which is why IP_PKTINFO is mandatory here.
	Listen netip.AddrPort
	// ReceiveBuffer and SendBuffer are SO_RCVBUF/SO_SNDBUF requests.
	ReceiveBuffer int
	SendBuffer    int
	// MaxDatagram bounds WriteTo. Zero uses DefaultMaxDatagram.
	MaxDatagram int
}

// Server is the shared, unconnected listen socket. One socket serves every
// client path, so each datagram's source, local destination address and receive
// ifindex come from the kernel per datagram rather than from the socket.
type Server struct {
	*socket
}

var _ transport.DatagramIO = (*Server)(nil)

// Listen binds the shared server socket (UDP-22..27).
func Listen(cfg ServerConfig) (*Server, error) {
	if !cfg.Listen.Addr().Is4() {
		return nil, fmt.Errorf("udp: listen address %s must be IPv4", cfg.Listen)
	}
	if cfg.MaxDatagram <= 0 {
		cfg.MaxDatagram = DefaultMaxDatagram
	}

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_UDP)
	if err != nil {
		return nil, fmt.Errorf("udp: socket: %w", err)
	}
	fail := func(format string, a ...any) (*Server, error) {
		unix.Close(fd)
		return nil, fmt.Errorf(format, a...)
	}

	if err := unix.Bind(fd, &unix.SockaddrInet4{ // UDP-23
		Addr: cfg.Listen.Addr().As4(),
		Port: int(cfg.Listen.Port()),
	}); err != nil {
		return fail("udp: bind %s: %w", cfg.Listen, err)
	}
	// UDP-24: without IP_PKTINFO a wildcard-bound socket cannot tell which of
	// its addresses a datagram arrived on, so a reply could leave by the wrong
	// interface.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_PKTINFO, 1); err != nil {
		return fail("udp: IP_PKTINFO: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_RECVERR, 1); err != nil { // UDP-25
		return fail("udp: IP_RECVERR: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO); err != nil { // UDP-26
		return fail("udp: IP_MTU_DISCOVER: %w", err)
	}

	bufs, err := applyBuffers(fd, cfg.ReceiveBuffer, cfg.SendBuffer)
	if err != nil {
		return fail("udp: %w", err)
	}
	local, err := localAddrPort(fd)
	if err != nil {
		return fail("udp: getsockname: %w", err)
	}

	s, err := newSocket(fd, "udp:server", cfg.MaxDatagram, Diagnostics{
		LocalAddr: local,
		Buffers:   bufs,
	})
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &Server{socket: s}, nil
}

// LocalAddr is the socket's effective bind address.
func (s *Server) LocalAddr() netip.AddrPort { return s.diag.LocalAddr }

// ReadInto reads one datagram and records the source, the local destination
// address and the receive ifindex from ancillary data (UDP-27, UDP-29..33).
func (s *Server) ReadInto(ctx context.Context, dst []byte) (int, transport.ReceiveMeta, error) {
	oob := make([]byte, 256)
	return s.recvOne(ctx, dst, oob)
}

// WriteTo sends one datagram to a caller-selected remote endpoint (UDP-28).
// The session layer chooses that endpoint; this socket does not validate it
// beyond requiring IPv4.
func (s *Server) WriteTo(ctx context.Context, pkt []byte, dst transport.Endpoint) error {
	if !dst.AddrPort.IsValid() || !dst.AddrPort.Addr().Is4() {
		return fmt.Errorf("udp: destination %s must be a valid IPv4 endpoint", dst.AddrPort)
	}
	if len(pkt) > s.maxDatagram {
		return fmt.Errorf("%w: %d > %d", transport.ErrOversize, len(pkt), s.maxDatagram)
	}
	sa := &unix.SockaddrInet4{Addr: dst.AddrPort.Addr().As4(), Port: int(dst.AddrPort.Port())}
	err := s.doWrite(ctx, func(fd uintptr) (bool, error) {
		e := unix.Sendto(int(fd), pkt, unix.MSG_DONTWAIT, sa)
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
			s.publishPathError(transport.PathError{Peer: dst.AddrPort, Local: true})
		}
		if errno == unix.EMSGSIZE { // UDP-38
			return fmt.Errorf("%w: kernel refused %d bytes: %w", transport.ErrOversize, len(pkt), err)
		}
	}
	return fmt.Errorf("udp: sendto: %w", err)
}
