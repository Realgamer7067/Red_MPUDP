//go:build linux

// Package udp implements the Linux DatagramIO sockets: one interface-bound
// client socket per path and one shared server socket (design §11.1, §13).
package udp

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
	"golang.org/x/sys/unix"
)

// BufferSizes records what was asked of the kernel and what it actually gave
// back (UDP-21). It is a fixed struct with no per-packet or per-peer labels.
//
// Linux stores roughly twice the requested SO_RCVBUF/SO_SNDBUF to cover its own
// bookkeeping and clamps the result to net.core.{rmem,wmem}_max, so Effective
// is routinely not equal to Requested in either direction. Nothing may assert
// equality; the pair is recorded so an operator can see the clamp.
type BufferSizes struct {
	RequestedReceive int
	EffectiveReceive int
	RequestedSend    int
	EffectiveSend    int
}

// Diagnostics is a socket's bounded diagnostic snapshot (UDP-21).
type Diagnostics struct {
	LocalAddr netip.AddrPort
	IfIndex   int
	IfName    string
	FWMark    uint32
	Buffers   BufferSizes
}

// pathErrDepth bounds the queue of pending path errors. A full queue drops the
// oldest entry: PMTU feedback is advisory (design §10), so unbounded growth
// under an error storm would be the worse failure.
const pathErrDepth = 16

// socket is the shared machinery behind the client and server sockets: an
// *os.File over a nonblocking fd, a single error-queue reader goroutine, and
// one bounded channel that owns every PathError.
type socket struct {
	file *os.File
	rc   syscall.RawConn

	maxDatagram int
	diag        Diagnostics

	// peer is the connected remote endpoint, or the zero value for the
	// unconnected server socket. It is the only identity a normal-read errno
	// can be attributed to, and the value an error-queue event must quote to be
	// accepted (UDP-37).
	peer netip.AddrPort

	// pathErrs is the single source of truth for path errors. Both the
	// error-queue goroutine and the synchronous send path publish here, so it
	// does not matter which of them observes a given socket error first.
	pathErrs chan transport.PathError

	// errFD is a dup of the socket used solely for MSG_ERRQUEUE reads, and
	// wakeR/wakeW is a pipe that interrupts its poll on Close.
	//
	// The error queue deliberately does NOT go through the same *os.File: Go's
	// poller takes a per-descriptor read lock, so a second goroutine calling
	// RawConn.Read on this fd would starve the real receive path for as long as
	// it waited. Polling a duplicate for POLLERR alone also means ordinary
	// inbound data never wakes this goroutine, so it does not spin.
	errFD int
	wakeR int
	wakeW int

	closeOnce sync.Once
	closeErr  error
	closed    chan struct{}
	wg        sync.WaitGroup
}

// newSocket wraps an already-configured nonblocking fd.
//
// Ownership: on success the returned socket owns fd and closes it in Close. On
// failure fd is NOT closed and NOT wrapped — ownership stays with the caller,
// which is still inside its own cleanup path and will close it exactly once.
// Closing here as well would double-close a descriptor number the kernel may
// already have handed to another goroutine.
func newSocket(fd int, name string, maxDatagram int, peer netip.AddrPort, diag Diagnostics) (*socket, error) {
	errFD, err := dupCloexec(fd)
	if err != nil {
		return nil, fmt.Errorf("udp: dup for the error queue: %w", err)
	}
	var pipe [2]int
	if err := pipe2(pipe[:]); err != nil {
		unix.Close(errFD)
		return nil, fmt.Errorf("udp: error-queue wakeup pipe: %w", err)
	}

	// From here on the *os.File owns fd, so every later failure closes the file
	// rather than the raw descriptor.
	f := os.NewFile(uintptr(fd), name)
	rc, err := f.SyscallConn()
	if err != nil {
		unix.Close(errFD)
		unix.Close(pipe[0])
		unix.Close(pipe[1])
		f.Close()
		return nil, fmt.Errorf("udp: raw conn: %w", errFileOwned{err})
	}
	// Dup3 with O_CLOEXEC rather than Dup: a plain dup clears close-on-exec, so
	// a fork between the two calls could leak the socket.
	s := &socket{
		file:        f,
		rc:          rc,
		maxDatagram: maxDatagram,
		diag:        diag,
		peer:        peer,
		pathErrs:    make(chan transport.PathError, pathErrDepth),
		errFD:       errFD,
		wakeR:       pipe[0],
		wakeW:       pipe[1],
		closed:      make(chan struct{}),
	}
	s.wg.Add(1)
	go s.drainErrorQueue() // UDP-34
	return s, nil
}

func (s *socket) MaxDatagramSize() int { return s.maxDatagram }

// Diagnostics returns the bounded diagnostic snapshot (UDP-21).
func (s *socket) Diagnostics() Diagnostics { return s.diag }

func (s *socket) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// publishPathError enqueues without blocking, dropping the oldest entry when
// the bounded queue is full. An event with no valid peer is discarded: a
// PathError that cannot be attributed to an endpoint is worse than none, since
// the PMTU logic would apply it to the wrong path (UDP-37).
func (s *socket) publishPathError(pe transport.PathError) {
	if !pe.Peer.IsValid() {
		return
	}
	if s.peer.IsValid() && pe.Peer != s.peer {
		return // not this path's error
	}
	if s.isClosed() {
		return
	}
	for {
		select {
		case s.pathErrs <- pe:
			return
		default:
		}
		select {
		case <-s.pathErrs: // drop the oldest and retry
		default:
			return
		}
	}
}

// ReadPathError implements transport.DatagramIO (UDP-34..37).
func (s *socket) ReadPathError(ctx context.Context) (transport.PathError, error) {
	// ErrClosed outranks a queued event and a cancelled context alike: once the
	// socket is closed every method reports that, stably, whatever else is
	// pending (UDP-40).
	if s.isClosed() {
		return transport.PathError{}, transport.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return transport.PathError{}, err
	}
	select {
	case pe := <-s.pathErrs:
		return pe, nil
	case <-ctx.Done():
		return transport.PathError{}, ctx.Err()
	case <-s.closed:
		return transport.PathError{}, transport.ErrClosed
	}
}

// withDeadline arms a context cancellation on the descriptor and clears it
// afterwards. The clear is ordered after the callback: when stop reports the
// callback already started, wait for it, or the clear can be overtaken and
// leave a permanently expired deadline behind (the M06 TUN-19 lesson).
func (s *socket) withDeadline(ctx context.Context, set func(time.Time) error) func() {
	if ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		_ = set(time.Unix(0, 1))
	})
	return func() {
		if !stop() {
			<-done
		}
		_ = set(time.Time{})
	}
}

// doRead runs fn on the fd when it is read-ready, retrying while fn reports
// EAGAIN.
//
// RawConn.Read's callback contract is inverted from the obvious reading:
// returning false means "not ready, park me and call again", so fn must return
// false only on EAGAIN/EWOULDBLOCK and true on every other outcome, success or
// hard error. RawConn.Read itself also returns an error of its own — a deadline
// from cancellation, or a closed descriptor — which is distinct from whatever
// fn captured, so both are inspected.
func (s *socket) doRead(ctx context.Context, fn func(fd uintptr) (bool, error)) error {
	if s.isClosed() {
		return transport.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	restore := s.withDeadline(ctx, s.file.SetReadDeadline)
	defer restore()

	var inner error
	rcErr := s.rc.Read(func(fd uintptr) bool {
		done, err := fn(fd)
		inner = err
		return done
	})
	if inner != nil {
		return inner
	}
	return s.translateWaitErr(ctx, rcErr)
}

func (s *socket) doWrite(ctx context.Context, fn func(fd uintptr) (bool, error)) error {
	if s.isClosed() {
		return transport.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	restore := s.withDeadline(ctx, s.file.SetWriteDeadline)
	defer restore()

	var inner error
	rcErr := s.rc.Write(func(fd uintptr) bool {
		done, err := fn(fd)
		inner = err
		return done
	})
	if inner != nil {
		return inner
	}
	return s.translateWaitErr(ctx, rcErr)
}

// translateWaitErr maps a poller-level failure onto the package's stable
// errors. A cancelled context surfaces as ctx.Err(), never as a raw timeout.
func (s *socket) translateWaitErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if s.isClosed() || errors.Is(err, os.ErrClosed) || errors.Is(err, net_ErrClosed) {
		return transport.ErrClosed
	}
	return err
}

// Close is idempotent, closes the descriptor exactly once, and unblocks every
// parked reader, writer and error-queue goroutine (UDP-39, UDP-40).
func (s *socket) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		// Wake the error-queue poller, then wait for it before closing the fds
		// it is using — the same "never tear down under an in-flight operation"
		// discipline the TUN device applies to its netlink socket.
		_, _ = unix.Write(s.wakeW, []byte{0})
		s.wg.Wait()
		unix.Close(s.errFD)
		unix.Close(s.wakeR)
		unix.Close(s.wakeW)
		s.closeErr = s.file.Close() // unblocks everyone parked in the poller
	})
	return s.closeErr
}

// recvOne performs one recvmsg into dst and fills meta.
//
// MSG_TRUNC is passed in the flags so the kernel reports the datagram's real
// length in n even when it did not fit; that length, and the MSG_TRUNC bit the
// kernel sets in the reply flags, are both treated as truncation. A truncated
// datagram is dropped whole — no bytes are returned and dst is not to be read
// (UDP-29, UDP-32, UDP-33).
func (s *socket) recvOne(ctx context.Context, dst []byte, oob []byte) (int, transport.ReceiveMeta, error) {
	var (
		meta  transport.ReceiveMeta
		n     int
		oobn  int
		flags int
		from  unix.Sockaddr
	)
	err := s.doRead(ctx, func(fd uintptr) (bool, error) {
		var e error
		n, oobn, flags, from, e = unix.Recvmsg(int(fd), dst, oob, unix.MSG_TRUNC|unix.MSG_DONTWAIT)
		if e == unix.EAGAIN || e == unix.EWOULDBLOCK || e == unix.EINTR {
			return false, nil // not ready: park and retry
		}
		return true, e
	})
	if err != nil {
		return 0, meta, s.mapRecvErr(err)
	}

	meta.Source = sockaddrToAddrPort(from)
	if oobn > 0 {
		if local, ifi, ok := parsePktinfo(oob[:oobn]); ok { // UDP-31
			meta.LocalAddr, meta.IfIndex = local, ifi
		}
	}
	if n > len(dst) || flags&unix.MSG_TRUNC != 0 { // UDP-32
		meta.Truncated = true
		return 0, meta, transport.ErrTruncated // UDP-33: dropped before any parsing
	}
	return n, meta, nil
}

// mapRecvErr turns a receive failure into a stable error. A pending socket
// error consumed by the normal receive path (rather than by the error-queue
// goroutine) is still published as a PathError, so ownership does not depend on
// which goroutine the kernel happened to wake.
//
// The event is attributed to the socket's connected peer. An unconnected server
// socket has no peer to attribute a bare errno to — the errno alone does not say
// which client it belongs to — so it publishes nothing and lets the error-queue
// goroutine, which does get a quoted tuple, be the only source.
func (s *socket) mapRecvErr(err error) error {
	if errors.Is(err, transport.ErrClosed) || errors.Is(err, os.ErrClosed) {
		return transport.ErrClosed
	}
	var errno unix.Errno
	if errors.As(err, &errno) && isPathErrno(errno) && s.peer.IsValid() {
		pe := transport.PathError{Peer: s.peer, Local: true}
		if errno == unix.EMSGSIZE {
			pe.MTU = 0 // a bare errno carries no next-hop MTU
		}
		s.publishPathError(pe)
	}
	return err
}

func isPathErrno(e unix.Errno) bool {
	switch e {
	case unix.EMSGSIZE, unix.ECONNREFUSED, unix.EHOSTUNREACH, unix.ENETUNREACH, unix.EHOSTDOWN, unix.ENETDOWN:
		return true
	}
	return false
}

// drainErrorQueue reads MSG_ERRQUEUE messages for the socket's lifetime
// (UDP-34..37). It parses only kernel-supplied binary structures — never any
// attacker-controlled string — and ignores anything it cannot map (UDP-37).
//
// It polls the duplicated descriptor for POLLERR only. POLLERR is reported
// whether or not it is requested, but requesting nothing else means ordinary
// inbound data does not wake this goroutine, so it neither spins nor competes
// with the receive path for the poller's per-descriptor read lock.
func (s *socket) drainErrorQueue() {
	defer s.wg.Done()
	oob := make([]byte, 1024)
	buf := make([]byte, 2048)
	for {
		fds := []unix.PollFd{
			{Fd: int32(s.errFD), Events: unix.POLLERR},
			{Fd: int32(s.wakeR), Events: unix.POLLIN},
		}
		if _, err := unix.Poll(fds, -1); err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if fds[1].Revents != 0 || s.isClosed() {
			return
		}
		if fds[0].Revents&unix.POLLERR == 0 {
			continue
		}
		// Drain every queued event, not just the first.
		for {
			_, oobn, _, from, err := unix.Recvmsg(s.errFD, buf, oob, unix.MSG_ERRQUEUE|unix.MSG_DONTWAIT)
			if err != nil {
				break
			}
			// publishPathError discards an event with no valid quoted peer, or
			// one quoting an endpoint this socket is not connected to (UDP-37).
			if pe, ok := parseErrorQueue(oob[:oobn], from); ok { // UDP-35, UDP-36
				s.publishPathError(pe)
			}
		}
	}
}
