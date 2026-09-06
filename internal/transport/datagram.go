// Package transport is the datagram layer beneath the RED_MPUDP session engine
// (design §13). It does not authenticate anything and does not validate
// endpoints — the crypto/session layer owns both. Its job is to move opaque
// byte slices, to report receive metadata precisely enough that a truncated
// datagram can never be mistaken for a complete one, and to surface path errors
// that can be mapped back to a socket without parsing attacker-controlled data.
package transport

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
)

// Errors returned by every DatagramIO implementation. Callers compare with
// errors.Is (UDP-03).
var (
	// ErrClosed is returned by every method after Close, and by a read that
	// Close unblocked.
	ErrClosed = errors.New("transport: datagram socket is closed")
	// ErrTruncated is returned by ReadInto when the kernel delivered a datagram
	// larger than dst. The datagram is dropped, never returned in part
	// (UDP-32, UDP-33).
	ErrTruncated = errors.New("transport: datagram larger than the receive buffer")
	// ErrUnsupported is returned by constructors on platforms without a Linux
	// socket implementation.
	ErrUnsupported = errors.New("transport: not supported on this platform")
	// ErrOversize is returned by WriteTo when the datagram exceeds
	// MaxDatagramSize, and wraps a synchronous kernel EMSGSIZE. The datagram is
	// never retried at a smaller size — that decision belongs to the PMTU logic
	// (design §10, UDP-38).
	ErrOversize = errors.New("transport: datagram exceeds the maximum size")
)

// Endpoint names a datagram peer. IfIndex is the interface the datagram should
// leave by, or 0 when the socket's own binding decides.
type Endpoint struct {
	AddrPort netip.AddrPort
	IfIndex  int
}

func (e Endpoint) String() string { return fmt.Sprintf("%s%%%d", e.AddrPort, e.IfIndex) }

// ReceiveMeta describes one received datagram (design §13).
//
// Truncated is authoritative: when it is true the datagram was larger than the
// caller's buffer, no bytes are returned, and nothing about the datagram may be
// parsed. LocalAddr and IfIndex come from kernel ancillary data, never from the
// datagram's own contents.
type ReceiveMeta struct {
	Source    netip.AddrPort
	LocalAddr netip.Addr
	IfIndex   int
	Truncated bool
}

// PathError is an asynchronous or synchronous transport error attributable to
// one path (design §10, §13).
//
// Peer is the quoted peer tuple when the kernel supplied one, otherwise the
// zero value. MTU is the reported next-hop MTU when present, otherwise 0. Local
// reports a synchronous local error (a refused send) rather than an ICMP error
// delivered on the error queue; only a local error may block a size
// immediately, and no PathError ever raises a confirmed PMTU.
type PathError struct {
	Peer  netip.AddrPort
	MTU   int
	Local bool
}

func (p PathError) String() string {
	kind := "icmp"
	if p.Local {
		kind = "local"
	}
	return fmt.Sprintf("path error (%s) peer=%s mtu=%d", kind, p.Peer, p.MTU)
}

// DatagramIO is one datagram socket: a client path socket or the server's
// shared listen socket (design §13).
//
// # Buffer ownership (UDP-02)
//
// Every method borrows the caller's slice for the duration of the call and
// never retains it. No implementation stores, mutates after return, or hands
// another goroutine a reference to a caller's buffer.
//
//   - ReadInto: the caller owns dst and must not alter it while the call runs.
//     On return the first n bytes are the datagram and the caller owns them
//     again. On any error, including ErrTruncated, n is 0 and dst holds nothing
//     the caller may rely on.
//   - WriteTo: the caller owns pkt throughout. The implementation copies or
//     hands it to the kernel before returning and never keeps it, so the caller
//     may release a pooled buffer as soon as WriteTo returns.
//   - ReadPathError returns values, not buffers, so nothing is shared.
//
// # Concurrency
//
// One reader, one writer and one ReadPathError caller may run concurrently.
// Close is safe to call at any time from any goroutine and must unblock all
// three (UDP-39, UDP-40).
type DatagramIO interface {
	// ReadInto reads one datagram into dst. A cancelled ctx unblocks it with
	// ctx.Err(); Close unblocks it with ErrClosed. A datagram larger than dst
	// is dropped and reported as ErrTruncated with meta.Truncated set.
	ReadInto(ctx context.Context, dst []byte) (n int, meta ReceiveMeta, err error)

	// WriteTo sends pkt to dst. A connected client socket ignores dst's address
	// and refuses a mismatch. An oversized datagram fails with ErrOversize and
	// is never retried smaller.
	WriteTo(ctx context.Context, pkt []byte, dst Endpoint) error

	// ReadPathError returns the next path error, blocking until one arrives,
	// ctx is done, or the socket closes.
	ReadPathError(ctx context.Context) (PathError, error)

	// MaxDatagramSize is the largest payload WriteTo will accept.
	MaxDatagramSize() int

	// Close releases the socket. It is idempotent and unblocks every parked
	// caller (UDP-39, UDP-40).
	Close() error
}
