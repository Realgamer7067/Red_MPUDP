//go:build linux

package udp

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
	"golang.org/x/sys/unix"
)

// openFDCount counts this process's open descriptors, so a leak or a
// double-close is visible.
func openFDCount(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	return len(ents)
}

// Blocker 3: newSocket must not close a descriptor it did not create. The
// caller is still in its own cleanup path and will close fd exactly once; a
// second close here would release a descriptor number the kernel may already
// have reissued to another goroutine — the classic double-close bug, which
// silently corrupts an unrelated connection rather than failing loudly.
//
// Both post-construction failure points are injected, and each asserts the
// descriptor count returns to its starting value: neither leaked, neither
// double-closed.
func TestNewSocketDoesNotDoubleCloseOnInitFailure(t *testing.T) {
	realDup, realPipe := dupCloexec, pipe2
	t.Cleanup(func() { dupCloexec, pipe2 = realDup, realPipe })

	cases := []struct {
		name  string
		setup func()
		want  string
	}{
		{
			name: "dup fails",
			setup: func() {
				dupCloexec = func(int) (int, error) { return -1, unix.EMFILE }
				pipe2 = realPipe
			},
			want: "dup for the error queue",
		},
		{
			name: "wakeup pipe fails",
			setup: func() {
				dupCloexec = realDup
				pipe2 = func([]int) error { return unix.EMFILE }
			},
			want: "error-queue wakeup pipe",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			before := openFDCount(t)

			fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_UDP)
			if err != nil {
				t.Fatalf("socket: %v", err)
			}

			s, err := newSocket(fd, "test", 1500, netip.AddrPort{}, Diagnostics{})
			if err == nil {
				s.Close()
				t.Fatal("newSocket succeeded despite the injected failure")
			}
			if got := err.Error(); !strings.Contains(got, tc.want) {
				t.Fatalf("err = %q, want it to mention %q", got, tc.want)
			}

			// Ownership stayed with us: closing here must succeed exactly once.
			if err := unix.Close(fd); err != nil {
				t.Fatalf("caller close after a failed newSocket: %v — newSocket closed a "+
					"descriptor it does not own", err)
			}
			if err := unix.Close(fd); !errors.Is(err, unix.EBADF) {
				t.Fatalf("second close returned %v, want EBADF; the descriptor was not actually released", err)
			}
			if after := openFDCount(t); after != before {
				t.Fatalf("descriptor count %d -> %d across a failed newSocket", before, after)
			}
		})
	}
}

// A successful newSocket owns everything it opened, and Close releases all of
// it: the socket, the error-queue duplicate, and both ends of the wakeup pipe.
func TestSocketCloseReleasesEveryDescriptor(t *testing.T) {
	before := openFDCount(t)

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_UDP)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	s, err := newSocket(fd, "test", 1500, netip.AddrPort{}, Diagnostics{})
	if err != nil {
		unix.Close(fd)
		t.Fatalf("newSocket: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if after := openFDCount(t); after > before {
		t.Fatalf("descriptor leak across open/close: %d -> %d", before, after)
	}
}

// The one ownership branch the injected dup/pipe cases do not reach: a failure
// AFTER the *os.File has taken the descriptor. Here ownership has transferred,
// so newSocket must close the file itself and tell the caller not to — which is
// what errFileOwned marks. A caller that closed again would release a
// descriptor number the kernel may already have reissued.
func TestNewSocketFileOwnedFailureClosesTheDescriptorItself(t *testing.T) {
	realSyscallConn := syscallConn
	t.Cleanup(func() { syscallConn = realSyscallConn })
	syscallConn = func(*os.File) (syscall.RawConn, error) { return nil, unix.EINVAL }

	before := openFDCount(t)

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_UDP)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	s, err := newSocket(fd, "test", 1500, netip.AddrPort{}, Diagnostics{})
	if err == nil {
		s.Close()
		t.Fatal("newSocket succeeded despite the injected SyscallConn failure")
	}

	// The caller must be told ownership already transferred.
	var owned errFileOwned
	if !errors.As(err, &owned) {
		t.Fatalf("err = %v, want it to wrap errFileOwned so callers skip their own close", err)
	}
	// And the descriptor is genuinely gone: closing again is EBADF, not success.
	if cerr := unix.Close(fd); !errors.Is(cerr, unix.EBADF) {
		t.Fatalf("close after an errFileOwned failure returned %v, want EBADF — "+
			"the *os.File did not actually close the descriptor", cerr)
	}
	if after := openFDCount(t); after != before {
		t.Fatalf("descriptor count %d -> %d across an errFileOwned failure", before, after)
	}
}

// Blocker 4 coverage gap: the earlier after-close tests never enqueued an event
// first, so "ErrClosed outranks a queued path error" was asserted in prose and
// by inspection but never exercised. Publish a real event, confirm the queue is
// non-empty, then close and require ErrClosed rather than the pending event.
func TestErrClosedOutranksAQueuedPathError(t *testing.T) {
	peer := netip.MustParseAddrPort("127.0.0.1:9")

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, unix.IPPROTO_UDP)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	s, err := newSocket(fd, "test", 1500, peer, Diagnostics{})
	if err != nil {
		unix.Close(fd)
		t.Fatalf("newSocket: %v", err)
	}

	s.publishPathError(transport.PathError{Peer: peer, MTU: 1300, Local: true})
	if len(s.pathErrs) != 1 {
		t.Fatalf("queued %d events, want 1 — the test must start from a non-empty queue", len(s.pathErrs))
	}
	// While open, that event is readable.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	pe, err := s.ReadPathError(ctx)
	cancel()
	if err != nil || pe.MTU != 1300 {
		t.Fatalf("open socket ReadPathError = (%+v, %v)", pe, err)
	}

	// Re-queue, then close with the event still pending.
	s.publishPathError(transport.PathError{Peer: peer, MTU: 1300, Local: true})
	if len(s.pathErrs) != 1 {
		t.Fatalf("re-queued %d events, want 1", len(s.pathErrs))
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := s.ReadPathError(context.Background()); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("ReadPathError with an event still queued = %v, want ErrClosed", err)
	}
	// A post-close publish is dropped rather than queued.
	s.publishPathError(transport.PathError{Peer: peer, MTU: 1200, Local: true})
	if _, err := s.ReadPathError(context.Background()); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("ReadPathError after a post-close publish = %v, want ErrClosed", err)
	}
}
