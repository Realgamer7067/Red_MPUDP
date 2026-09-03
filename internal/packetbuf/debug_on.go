//go:build debug

package packetbuf

// checkLive panics if the buffer has already been released (use-after-release).
func (b *Buffer) checkLive() {
	if b.released {
		panic("packetbuf: use of released buffer")
	}
}

// reportDoubleRelease panics: Release was called on an already-released buffer.
func reportDoubleRelease() {
	panic("packetbuf: double release")
}
