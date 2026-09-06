package transport

import (
	"context"
	"math/rand/v2"
	"net/netip"
	"sync"
)

// MemoryOptions configures one direction of an in-memory DatagramIO pair. The
// injection knobs let unit tests reproduce reorder, loss and duplication
// deterministically — seed the Rand and the sequence is repeatable (UDP-05,
// UDP-06, UDP-07).
type MemoryOptions struct {
	// Capacity bounds the receive queue. Zero uses DefaultMemoryCapacity. A
	// datagram arriving at a full queue is dropped and counted, never buffered
	// without limit (UDP-08).
	Capacity int
	// LossRate drops a fraction of arriving datagrams, in [0, 1].
	LossRate float64
	// DuplicateRate delivers a second copy of a fraction of datagrams.
	DuplicateRate float64
	// ReorderRate holds back a fraction of datagrams by one delivery slot, so
	// the next datagram overtakes them.
	ReorderRate float64
	// Rand supplies the injection decisions. Nil uses a fixed-seed source, so a
	// test that sets a rate but no source is still deterministic.
	Rand *rand.Rand
	// MaxDatagram bounds WriteTo. Zero uses DefaultMemoryMaxDatagram.
	MaxDatagram int
}

const (
	// DefaultMemoryCapacity is the default bounded receive depth.
	DefaultMemoryCapacity = 64
	// DefaultMemoryMaxDatagram matches the largest outer datagram v1 sends.
	DefaultMemoryMaxDatagram = 1560
)

// MemoryStats is a bounded counter set for one endpoint. There are no per-peer
// or per-packet labels.
type MemoryStats struct {
	Delivered  uint64
	Dropped    uint64 // injected loss
	Overflowed uint64 // queue was full
	Duplicated uint64
	Reordered  uint64
	Sent       uint64
}

// memoryEndpoint is one half of a pair.
type memoryEndpoint struct {
	addr netip.AddrPort
	opts MemoryOptions

	mu      sync.Mutex
	queue   []memoryDatagram
	held    *memoryDatagram // withheld by reorder injection
	stats   MemoryStats
	closed  bool
	notify  chan struct{}
	done    chan struct{}
	pathErr []PathError

	peer *memoryEndpoint
}

type memoryDatagram struct {
	payload []byte
	meta    ReceiveMeta
}

// NewMemoryPair returns two connected in-memory DatagramIO endpoints (UDP-04).
// aOpts applies to datagrams arriving at a, bOpts to datagrams arriving at b,
// so a test can make one direction lossy and leave the other clean.
func NewMemoryPair(aAddr, bAddr netip.AddrPort, aOpts, bOpts MemoryOptions) (*MemoryConn, *MemoryConn) {
	a := &memoryEndpoint{addr: aAddr, opts: aOpts.withDefaults(),
		notify: make(chan struct{}, 1), done: make(chan struct{})}
	b := &memoryEndpoint{addr: bAddr, opts: bOpts.withDefaults(),
		notify: make(chan struct{}, 1), done: make(chan struct{})}
	a.peer, b.peer = b, a
	return &MemoryConn{ep: a}, &MemoryConn{ep: b}
}

func (o MemoryOptions) withDefaults() MemoryOptions {
	if o.Capacity <= 0 {
		o.Capacity = DefaultMemoryCapacity
	}
	if o.MaxDatagram <= 0 {
		o.MaxDatagram = DefaultMemoryMaxDatagram
	}
	if o.Rand == nil {
		o.Rand = rand.New(rand.NewPCG(1, 2))
	}
	return o
}

// MemoryConn is an in-memory DatagramIO for unit tests.
type MemoryConn struct{ ep *memoryEndpoint }

var _ DatagramIO = (*MemoryConn)(nil)

// LocalAddr is the address peers see as the source.
func (c *MemoryConn) LocalAddr() netip.AddrPort { return c.ep.addr }

// Stats returns a snapshot of the bounded counters.
func (c *MemoryConn) Stats() MemoryStats {
	c.ep.mu.Lock()
	defer c.ep.mu.Unlock()
	return c.ep.stats
}

// InjectPathError queues a PathError for ReadPathError to return.
func (c *MemoryConn) InjectPathError(pe PathError) {
	c.ep.mu.Lock()
	c.ep.pathErr = append(c.ep.pathErr, pe)
	c.ep.mu.Unlock()
	c.ep.wake()
}

func (c *MemoryConn) MaxDatagramSize() int { return c.ep.opts.MaxDatagram }

func (e *memoryEndpoint) wake() {
	select {
	case e.notify <- struct{}{}:
	default:
	}
}

// deliver applies the injection knobs and appends to the bounded queue. It runs
// on the sender's goroutine but locks the receiver.
func (e *memoryEndpoint) deliver(d memoryDatagram) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	r := e.opts.Rand
	if e.opts.LossRate > 0 && r.Float64() < e.opts.LossRate {
		e.stats.Dropped++
		e.mu.Unlock()
		return
	}
	// Reorder: hold this one back and let the next datagram pass it. The held
	// datagram is released as soon as another arrives, so nothing is lost.
	if e.opts.ReorderRate > 0 && e.held == nil && r.Float64() < e.opts.ReorderRate {
		held := d
		e.held = &held
		e.stats.Reordered++
		e.mu.Unlock()
		return
	}
	e.push(d)
	if e.held != nil {
		h := *e.held
		e.held = nil
		e.push(h)
	}
	if e.opts.DuplicateRate > 0 && r.Float64() < e.opts.DuplicateRate {
		e.stats.Duplicated++
		e.push(d)
	}
	e.mu.Unlock()
	e.wake()
}

// push appends under e.mu, dropping at capacity (UDP-08).
func (e *memoryEndpoint) push(d memoryDatagram) {
	if len(e.queue) >= e.opts.Capacity {
		e.stats.Overflowed++
		return
	}
	e.queue = append(e.queue, d)
}

func (c *MemoryConn) WriteTo(ctx context.Context, pkt []byte, dst Endpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.ep.mu.Lock()
	closed := c.ep.closed
	c.ep.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if len(pkt) > c.ep.opts.MaxDatagram {
		return ErrOversize
	}
	// Copy: WriteTo never retains the caller's buffer (UDP-02).
	cp := append([]byte(nil), pkt...)

	c.ep.mu.Lock()
	c.ep.stats.Sent++
	c.ep.mu.Unlock()

	c.ep.peer.deliver(memoryDatagram{
		payload: cp,
		meta: ReceiveMeta{
			Source:    c.ep.addr,
			LocalAddr: c.ep.peer.addr.Addr(),
			IfIndex:   dst.IfIndex,
		},
	})
	return nil
}

func (c *MemoryConn) ReadInto(ctx context.Context, dst []byte) (int, ReceiveMeta, error) {
	e := c.ep
	for {
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return 0, ReceiveMeta{}, ErrClosed
		}
		if len(e.queue) > 0 {
			d := e.queue[0]
			e.queue = e.queue[1:]
			e.stats.Delivered++
			e.mu.Unlock()
			if len(d.payload) > len(dst) {
				meta := d.meta
				meta.Truncated = true
				return 0, meta, ErrTruncated // UDP-32, UDP-33
			}
			return copy(dst, d.payload), d.meta, nil
		}
		e.mu.Unlock()

		select {
		case <-ctx.Done():
			return 0, ReceiveMeta{}, ctx.Err()
		case <-e.done:
			return 0, ReceiveMeta{}, ErrClosed
		case <-e.notify:
		}
	}
}

func (c *MemoryConn) ReadPathError(ctx context.Context) (PathError, error) {
	e := c.ep
	for {
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return PathError{}, ErrClosed
		}
		if len(e.pathErr) > 0 {
			pe := e.pathErr[0]
			e.pathErr = e.pathErr[1:]
			e.mu.Unlock()
			return pe, nil
		}
		e.mu.Unlock()

		select {
		case <-ctx.Done():
			return PathError{}, ctx.Err()
		case <-e.done:
			return PathError{}, ErrClosed
		case <-e.notify:
		}
	}
}

// Close is idempotent and unblocks every parked caller (UDP-39, UDP-40).
func (c *MemoryConn) Close() error {
	e := c.ep
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()
	// done, not notify: deliver may be about to wake() on notify, and closing a
	// channel someone is sending to panics. A closed done wakes every waiter
	// and keeps waking later ones.
	close(e.done)
	return nil
}
