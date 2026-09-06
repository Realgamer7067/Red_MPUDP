//go:build linux

package udp

import (
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"

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
