// Package tun owns the RED_MPUDP layer-3 TUN interface: opening a safely owned
// /dev/net/tun descriptor, configuring the interface with structured netlink
// values, and reading and writing complete IPv4 packets through it
// (design §11, §16).
//
// The interface carries raw IPv4 packets with no packet-information header
// (IFF_TUN | IFF_NO_PI). Only the Linux implementation is functional; every
// other platform's Open returns [ErrUnsupported].
package tun

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"
)

// MTU bounds for the negotiated inner interface (design §10: the configured v1
// range is 1112–1400, default 1180).
const (
	MinMTU     = 1112
	MaxMTU     = 1400
	DefaultMTU = 1180
)

// ifaceNameMax is Linux IFNAMSIZ - 1.
const ifaceNameMax = 15

// Errors returned by this package. Callers compare with errors.Is.
var (
	// ErrUnsupported is returned by Open on non-Linux platforms.
	ErrUnsupported = errors.New("tun: not supported on this platform")
	// ErrClosed is returned by ReadPacket/WritePacket after Close, and by a
	// read unblocked by Close.
	ErrClosed = errors.New("tun: device is closed")
	// ErrPacketTooLarge is returned by ReadPacket when the kernel hands up a
	// packet larger than the configured maximum; the packet is dropped and
	// counted, never truncated silently.
	ErrPacketTooLarge = errors.New("tun: packet exceeds the configured maximum")
	// ErrShortWrite is returned when the kernel accepts fewer bytes than the
	// packet contained.
	ErrShortWrite = errors.New("tun: short write to the tun device")
	// ErrMTURange is returned when a requested MTU is outside [MinMTU, MaxMTU].
	ErrMTURange = errors.New("tun: mtu outside the configured range")
	// ErrMTUIncrease is returned when a live MTU increase is attempted without
	// an authenticated committed value (design §10).
	ErrMTUIncrease = errors.New("tun: refusing an unauthenticated live MTU increase")
	// ErrBufferTooSmall is returned by ReadPacket when the caller's buffer
	// cannot hold a maximum-size packet plus the slack needed to detect an
	// oversize packet.
	ErrBufferTooSmall = errors.New("tun: read buffer smaller than the configured maximum")
	// ErrMTUAboveMaxPacket is returned by SetMTU when an explicit Config.MaxPacket
	// ceiling would be exceeded by the new MTU. Raising the MTU past the read
	// ceiling would make the kernel deliver packets the reader must drop, so the
	// change is refused rather than silently applied.
	ErrMTUAboveMaxPacket = errors.New("tun: mtu would exceed the configured MaxPacket ceiling")
)

// Config describes the interface to create.
type Config struct {
	// Name is the requested interface name. Empty lets the kernel assign one.
	Name string
	// Address is the interface's IPv4 address and prefix length.
	Address netip.Prefix
	// MTU is the interface MTU; it must be in [MinMTU, MaxMTU].
	MTU int
	// MaxPacket is an explicit, fixed ceiling on ReadPacket. Zero means "track
	// the live MTU": the ceiling follows every authenticated SetMTU change, so a
	// raised MTU does not turn valid packets into RxOversize drops. A non-zero
	// value is a hard ceiling instead — it never moves, and SetMTU refuses any
	// MTU above it with ErrMTUAboveMaxPacket. It must therefore lie in
	// [MTU, MaxMTU]: below MTU it would drop packets the kernel legally
	// delivers, and above MaxMTU it would demand read buffers larger than the
	// static ceiling the rest of the API is specified against.
	//
	// Either way a packet larger than the ceiling is dropped and counted
	// (RxOversize), never returned truncated.
	//
	// Read buffers are sized against the static MaxMTU, not against this value
	// or the current MTU — see ReadPacket.
	MaxPacket int
}

// validate checks a Config before any descriptor is opened or netlink mutation
// is issued (TUN-06, TUN-15).
func (c Config) validate() error {
	if c.Name != "" {
		if err := validateName(c.Name); err != nil {
			return err
		}
	}
	if c.MTU < MinMTU || c.MTU > MaxMTU {
		return fmt.Errorf("%w: %d not in [%d, %d]", ErrMTURange, c.MTU, MinMTU, MaxMTU)
	}
	if !c.Address.IsValid() || !c.Address.Addr().Is4() {
		return fmt.Errorf("tun: address %q must be a valid IPv4 prefix", c.Address)
	}
	if c.MaxPacket < 0 {
		return errors.New("tun: MaxPacket must not be negative")
	}
	// An explicit ceiling below the MTU would make the kernel deliver
	// MTU-sized packets that ReadPacket is required to drop.
	if c.MaxPacket > 0 && c.MaxPacket < c.MTU {
		return fmt.Errorf("tun: MaxPacket %d is below MTU %d", c.MaxPacket, c.MTU)
	}
	// A ceiling above MaxMTU would break the contract that a buffer sized
	// against the static MaxMTU always satisfies ReadPacket: the kernel can
	// never deliver more than MaxMTU here, so a larger ceiling buys nothing and
	// would reject standard pooled buffers with ErrBufferTooSmall.
	if c.MaxPacket > MaxMTU {
		return fmt.Errorf("%w: MaxPacket %d exceeds MaxMTU %d", ErrMTURange, c.MaxPacket, MaxMTU)
	}
	return nil
}

// validateName rejects a name the kernel would truncate or reject. The
// character set mirrors the kernel's dev_valid_name: '/', ':' (reserved for
// interface aliases) and any whitespace, plus "." and ".." (TUN-06).
func validateName(name string) error {
	switch {
	case name == "":
		return errors.New("tun: interface name must not be empty")
	case len(name) > ifaceNameMax:
		return fmt.Errorf("tun: interface name %q exceeds %d bytes", name, ifaceNameMax)
	case strings.ContainsAny(name, "/: \t\n\v\f\r\x00") || name == "." || name == "..":
		return fmt.Errorf("tun: interface name %q contains an invalid character", name)
	}
	return nil
}

// guardMTU decides whether a live MTU change from current to target is allowed
// (TUN-16, TUN-17). A reduction is always allowed. An increase is allowed only
// when authenticated is true (the session holds an authenticated committed
// value). The result is the value that should be applied.
func guardMTU(current, target int, authenticated bool) (int, error) {
	if target < MinMTU || target > MaxMTU {
		return 0, fmt.Errorf("%w: %d not in [%d, %d]", ErrMTURange, target, MinMTU, MaxMTU)
	}
	if target > current && !authenticated {
		return 0, fmt.Errorf("%w: %d -> %d", ErrMTUIncrease, current, target)
	}
	return target, nil
}

// Stats is a snapshot of a device's bounded counters (TUN-25). The set is
// fixed; there are no per-packet or per-peer labels.
//
// Counter relationships, so a reader never has to guess:
//   - TxErrors counts every failed WritePacket, including a short write.
//     TxShort is the subset of those that were short writes, so
//     TxShort <= TxErrors always.
//   - TxPackets and TxBytes count fully written packets only. The bytes the
//     kernel accepted during a short write are deliberately not added to
//     TxBytes — a partially written packet is not a delivered packet.
//   - RxOversize and RxErrors are disjoint: an oversize packet was read
//     successfully and then dropped, which is not a descriptor failure.
type Stats struct {
	RxPackets  uint64
	RxBytes    uint64
	RxErrors   uint64
	RxOversize uint64
	TxPackets  uint64
	TxBytes    uint64
	TxErrors   uint64
	TxShort    uint64
}

// counters is the atomic backing store for Stats.
type counters struct {
	rxPackets, rxBytes, rxErrors, rxOversize atomic.Uint64
	txPackets, txBytes, txErrors, txShort    atomic.Uint64
}

func (c *counters) snapshot() Stats {
	return Stats{
		RxPackets:  c.rxPackets.Load(),
		RxBytes:    c.rxBytes.Load(),
		RxErrors:   c.rxErrors.Load(),
		RxOversize: c.rxOversize.Load(),
		TxPackets:  c.txPackets.Load(),
		TxBytes:    c.txBytes.Load(),
		TxErrors:   c.txErrors.Load(),
		TxShort:    c.txShort.Load(),
	}
}

// Device is an open TUN interface.
type Device interface {
	// Name is the kernel-assigned interface name (TUN-07).
	Name() string
	// Index is the interface's ifindex.
	Index() int
	// MTU is the current interface MTU.
	MTU() int
	// Address is the configured IPv4 address and prefix.
	Address() netip.Prefix

	// ReadPacket reads one complete IPv4 packet into buf, which the caller owns.
	// It returns the packet length.
	//
	// buf must be larger than the packet ceiling that is in force for the whole
	// life of the device. Because a default (zero MaxPacket) ceiling tracks the
	// live MTU, size buf against the static MaxMTU — for example MaxMTU+64 —
	// never against the MTU the device happens to hold right now. A buffer that
	// is large enough today can otherwise be rejected with ErrBufferTooSmall
	// after an authenticated MTU increase.
	//
	// A ctx that is cancelled unblocks the read with ctx.Err(); a permanent
	// descriptor failure returns a different error (TUN-18, TUN-19, TUN-20).
	ReadPacket(ctx context.Context, buf []byte) (int, error)

	// WritePacket writes one complete IPv4 packet from the caller-owned buf. A
	// kernel short write is an error (TUN-21, TUN-22).
	WritePacket(buf []byte) (int, error)

	// SetMTU changes the live interface MTU. A reduction always succeeds; an
	// increase succeeds only when authenticatedIncrease is true (TUN-16,
	// TUN-17), and never above an explicit Config.MaxPacket ceiling
	// (ErrMTUAboveMaxPacket).
	//
	// Concurrent calls are serialized: each one authorizes against the MTU left
	// by the call before it, so two apparent reductions can never compose into
	// an unauthenticated effective increase.
	SetMTU(mtu int, authenticatedIncrease bool) error

	// Stats returns a snapshot of the bounded counters (TUN-25).
	Stats() Stats

	// Close releases the interface. The descriptor is closed exactly once
	// (TUN-08); the non-persistent interface disappears with it (TUN-10).
	Close() error
}
