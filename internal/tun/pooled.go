package tun

import (
	"context"

	"github.com/Realgamer7067/Red_MPUDP/internal/packetbuf"
)

// ReadInto reads one packet from d into a pooled buffer. On any error the
// buffer is released before returning, so no read-failure path leaks a pooled
// buffer (TUN-23). On success the caller owns b and must release it once it is
// done with the packet; b is resized to the packet length.
//
// b's capacity must exceed the device's read ceiling for the whole life of the
// device. That ceiling tracks the live MTU unless Config.MaxPacket pins it, so
// size against the static tun.MaxMTU, never against the current MTU: obtain b
// from a pool whose largest class is at least packetbuf.MaxOuterDatagram, which
// clears MaxMTU with room to spare.
func ReadInto(ctx context.Context, d Device, b *packetbuf.Buffer) (int, error) {
	b.Resize(b.Cap())
	n, err := d.ReadPacket(ctx, b.Bytes())
	if err != nil {
		b.Release()
		return 0, err
	}
	b.Resize(n)
	return n, nil
}

// WriteFrom writes the packet in b to d and always releases b — the write
// consumes the buffer whether or not it succeeds, so no write path leaks a
// pooled buffer (TUN-24).
func WriteFrom(d Device, b *packetbuf.Buffer) (int, error) {
	defer b.Release()
	return d.WritePacket(b.Bytes())
}
