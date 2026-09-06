//go:build !linux

package tun

// Open is unsupported off Linux; it returns a stable error (TUN-02).
func Open(Config) (Device, error) {
	return nil, ErrUnsupported
}
