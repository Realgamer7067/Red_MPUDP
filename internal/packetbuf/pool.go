// Package packetbuf provides pooled byte buffers sized for RED_MPUDP datagrams.
//
// # Ownership
//
// A Buffer has exactly one owner at a time. The code that calls Get owns the
// buffer until it either calls Release or hands the buffer to another component
// by an explicit transfer (for example, sending it on a channel). After a
// transfer the sender must not touch the buffer again; the receiver becomes the
// sole owner and is responsible for Release. Every error path that does not
// transfer the buffer must Release it.
//
// Release must be called exactly once. Building with the "debug" build tag
// turns double Release and use-after-Release into panics.
package packetbuf

import "sync"

// Datagram size ceilings, derived from the design (revision 4):
//
//   - Encapsulation overhead is 88 bytes (44 transport header + 16 AEAD tag +
//     8 UDP + 20 IPv4). §5.1 / §6.
//   - Maximum inner MTU is 1400 bytes. §6.3.
//   - Therefore the largest outer UDP payload RED_MPUDP ever sends is
//     1400 + 44 + 16 = 1460 bytes; with room for a 1500-byte inner packet plus
//     header/tag the hard ceiling is 1560. §6.3.
//   - Noise handshake messages carry noise_len <= 512 plus framing, well under
//     the data ceiling.
const (
	// MaxHandshakeDatagram bounds a handshake message on the wire.
	MaxHandshakeDatagram = 768
	// MaxDataDatagram bounds an encapsulated data packet on the wire: the
	// maximum inner MTU (1400) plus the 44-byte transport header plus the
	// 16-byte AEAD tag, rounded up.
	MaxDataDatagram = 1500
	// MaxOuterDatagram is the largest buffer any size class must satisfy. It
	// exceeds MaxDataDatagram so a full 1500-byte inner packet at the maximum
	// negotiated MTU still fits without a fresh allocation.
	MaxOuterDatagram = 1560
)

// sizeClasses is the ascending set of buffer capacities the default Pool
// serves. The largest must be >= MaxOuterDatagram; TestSizeClassesCoverCeiling
// enforces that so a future MTU change cannot silently truncate.
var sizeClasses = []int{128, MaxHandshakeDatagram, MaxOuterDatagram}

// Pool hands out Buffers from a small set of fixed size classes. It is safe for
// concurrent use.
type Pool struct {
	classes []int
	pools   []*sync.Pool
}

// NewPool returns a Pool serving the default RED_MPUDP size classes.
func NewPool() *Pool { return newPoolWithClasses(sizeClasses) }

func newPoolWithClasses(classes []int) *Pool {
	p := &Pool{classes: append([]int(nil), classes...)}
	for _, sz := range p.classes {
		sz := sz
		p.pools = append(p.pools, &sync.Pool{
			New: func() any { return &Buffer{data: make([]byte, sz), class: sz} },
		})
	}
	return p
}

// Get returns a Buffer whose capacity is at least n and whose length is exactly
// n. It panics if n exceeds the largest size class.
func (p *Pool) Get(n int) *Buffer {
	if n < 0 {
		panic("packetbuf: negative length")
	}
	for i, sz := range p.classes {
		if n <= sz {
			b := p.pools[i].Get().(*Buffer)
			b.pool = p
			b.poolIdx = i
			b.data = b.data[:n]
			b.released = false
			return b
		}
	}
	panic("packetbuf: requested size exceeds largest class")
}

// Buffer is a pooled byte slice. Obtain one from Pool.Get; return it with
// Release. See the package doc for ownership rules.
type Buffer struct {
	data     []byte
	class    int
	pool     *Pool
	poolIdx  int
	released bool
}

// Bytes returns the underlying slice, length n as requested from Get. It
// panics (debug build) if the buffer has been released.
func (b *Buffer) Bytes() []byte {
	b.checkLive()
	return b.data
}

// Cap returns the size class capacity backing this buffer.
func (b *Buffer) Cap() int { return b.class }

// Resize sets the buffer length to n, which must not exceed its capacity.
func (b *Buffer) Resize(n int) {
	b.checkLive()
	if n < 0 || n > b.class {
		panic("packetbuf: Resize out of range")
	}
	b.data = b.data[:n]
}

// Release returns the buffer to its pool. After Release the caller must not use
// the buffer. Calling Release twice panics on a debug build.
func (b *Buffer) Release() {
	if b.released {
		reportDoubleRelease()
		return
	}
	b.released = true
	b.data = b.data[:b.class]
	pool := b.pool
	idx := b.poolIdx
	b.pool = nil
	pool.pools[idx].Put(b)
}
