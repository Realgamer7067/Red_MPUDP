//go:build linux

package udp

import (
	"errors"
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"
)

// TestDialMapsInterfaceDisappearanceOnConnect covers the connect(2) leg of
// UDP-11. SO_BINDTODEVICE can succeed and the device can then be removed
// before connect runs, in which case the kernel reports ENODEV rather than a
// routing errno. A caller that cannot tell that apart from an ordinary failure
// would retry a path whose interface no longer exists.
func TestDialMapsInterfaceDisappearanceOnConnect(t *testing.T) {
	cases := []struct {
		errno unix.Errno
		gone  bool
	}{
		{unix.ENODEV, true},
		{unix.ENXIO, true},
		{unix.ENETUNREACH, true},
		{unix.EADDRNOTAVAIL, true},
		{unix.ECONNREFUSED, false},
		{unix.EACCES, false},
	}
	for _, tc := range cases {
		t.Run(tc.errno.Error(), func(t *testing.T) {
			real := connectSyscall
			t.Cleanup(func() { connectSyscall = real })
			captured := -1
			connectSyscall = func(fd int, _ unix.Sockaddr) error {
				captured = fd
				return tc.errno
			}

			c, err := Dial(ClientConfig{Server: netip.MustParseAddrPort("127.0.0.1:9")})
			if err == nil {
				c.Close()
				t.Fatalf("Dial succeeded despite connect returning %v", tc.errno)
			}
			if !errors.Is(err, tc.errno) {
				t.Fatalf("Dial error %v does not wrap the underlying %v", err, tc.errno)
			}
			if got := errors.Is(err, ErrInterfaceGone); got != tc.gone {
				t.Fatalf("errors.Is(err, ErrInterfaceGone) = %v, want %v (err %v)", got, tc.gone, err)
			}
			// The descriptor must be gone before anything can recycle the
			// number; a leaked path socket keeps a mark and a device binding
			// alive. Closing it again must therefore report EBADF.
			if captured < 0 {
				t.Fatal("connect seam was never reached, so nothing was proven")
			}
			if cerr := unix.Close(captured); !errors.Is(cerr, unix.EBADF) {
				t.Fatalf("fd %d was still open after a failed Dial: close returned %v, want EBADF", captured, cerr)
			}
		})
	}
}
