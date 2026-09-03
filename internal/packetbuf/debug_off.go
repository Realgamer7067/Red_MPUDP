//go:build !debug

package packetbuf

// checkLive is a no-op in production builds.
func (b *Buffer) checkLive() {}

// reportDoubleRelease is a no-op in production builds: a double Release is
// swallowed rather than crashing a running VPN. Build with -tags debug in tests
// to turn it into a panic.
func reportDoubleRelease() {}
