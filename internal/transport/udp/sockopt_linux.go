//go:build linux

package udp

import (
	"fmt"
	"net/netip"

	"golang.org/x/sys/unix"
)

// applyBuffers sets the requested SO_RCVBUF/SO_SNDBUF and reads back what the
// kernel granted (UDP-19, UDP-20, UDP-21).
//
// Linux stores about twice the requested value and clamps to
// net.core.{rmem,wmem}_max, so the effective size is expected to differ from
// the request. A refused request is fatal — a socket quietly running with a far
// smaller buffer than the operator asked for would drop datagrams under load
// with no signal — but the granted value itself is only recorded, never
// asserted against.
func applyBuffers(fd, wantRecv, wantSend int) (BufferSizes, error) {
	b := BufferSizes{RequestedReceive: wantRecv, RequestedSend: wantSend}
	if wantRecv > 0 {
		if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, wantRecv); err != nil {
			return b, fmt.Errorf("SO_RCVBUF %d: %w", wantRecv, err)
		}
	}
	if wantSend > 0 {
		if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, wantSend); err != nil {
			return b, fmt.Errorf("SO_SNDBUF %d: %w", wantSend, err)
		}
	}
	var err error
	if b.EffectiveReceive, err = unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF); err != nil {
		return b, fmt.Errorf("read back SO_RCVBUF: %w", err)
	}
	if b.EffectiveSend, err = unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF); err != nil {
		return b, fmt.Errorf("read back SO_SNDBUF: %w", err)
	}
	return b, nil
}

// localAddrPort reads the socket's effective local address.
func localAddrPort(fd int) (netip.AddrPort, error) {
	sa, err := unix.Getsockname(fd)
	if err != nil {
		return netip.AddrPort{}, err
	}
	return sockaddrToAddrPort(sa), nil
}

// dupCloexec duplicates fd with close-on-exec set atomically. fcntl(F_DUPFD_CLOEXEC)
// is used rather than dup3 because dup3 requires a specific target descriptor.
func dupCloexec(fd int) (int, error) {
	nfd, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	return nfd, nil
}
