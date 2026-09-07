//go:build linux

package udp

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"

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
var dupCloexec = func(fd int) (int, error) {
	nfd, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	return nfd, nil
}

// pipe2 is a seam so a test can force the wakeup pipe to fail.
var pipe2 = func(p []int) error { return unix.Pipe2(p, unix.O_CLOEXEC|unix.O_NONBLOCK) }

// syscallConn is a seam so a test can force the one newSocket failure that
// happens after the *os.File has taken ownership of the descriptor.
var syscallConn = func(f *os.File) (syscall.RawConn, error) { return f.SyscallConn() }

// errFileOwned marks a newSocket failure that happened after the *os.File took
// ownership of the descriptor, so the caller must not close it again.
type errFileOwned struct{ err error }

func (e errFileOwned) Error() string { return e.err.Error() }
func (e errFileOwned) Unwrap() error { return e.err }

// ErrBadIfIndex is returned when a caller names an interface index that is not
// present on this host.
var ErrBadIfIndex = errors.New("udp: unknown outbound interface index")

// validateIfIndex rejects an index that names no interface, so a typo or a
// stale ReceiveMeta fails loudly instead of silently falling back to whatever
// the routing table would have chosen.
func validateIfIndex(idx int) error {
	if idx <= 0 {
		return fmt.Errorf("%w: %d", ErrBadIfIndex, idx)
	}
	if _, err := net.InterfaceByIndex(idx); err != nil {
		return fmt.Errorf("%w: %d: %w", ErrBadIfIndex, idx, err)
	}
	return nil
}

// connectSyscall is a seam so a test can force connect(2) to fail with the
// errnos an interface removal produces, without needing CAP_NET_ADMIN.
var connectSyscall = func(fd int, sa unix.Sockaddr) error { return unix.Connect(fd, sa) }
