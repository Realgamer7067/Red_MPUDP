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
)

// Config describes the interface to create.
type Config struct {
	// Name is the requested interface name. Empty lets the kernel assign one.
	Name string
	// Address is the interface's IPv4 address and prefix length.
	Address netip.Prefix
	// MTU is the interface MTU; it must be in [MinMTU, MaxMTU].
	MTU int
	// MaxPacket bounds ReadPacket. Zero means MTU. A packet larger than this is
	// dropped and counted (RxOversize), never returned truncated.
	MaxPacket int
}

func (c Config) maxPacket() int {
	if c.MaxPacket > 0 {
		return c.MaxPacket
	}
	return c.MTU
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
	return nil
}

// validateName rejects a name the kernel would truncate or that contains a
// path or whitespace character (TUN-06).
func validateName(name string) error {
	switch {
	case name == "":
		return errors.New("tun: interface name must not be empty")
	case len(name) > ifaceNameMax:
		return fmt.Errorf("tun: interface name %q exceeds %d bytes", name, ifaceNameMax)
	case strings.ContainsAny(name, "/ \t\n\r\x00") || name == "." || name == "..":
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

	// ReadPacket reads one complete IPv4 packet into buf, which the caller owns
	// and which must be larger than the configured maximum packet size. It
	// returns the packet length. A ctx that is cancelled unblocks the read
	// with ctx.Err(); a permanent descriptor failure returns a different error
	// (TUN-18, TUN-19, TUN-20).
	ReadPacket(ctx context.Context, buf []byte) (int, error)

	// WritePacket writes one complete IPv4 packet from the caller-owned buf. A
	// kernel short write is an error (TUN-21, TUN-22).
	WritePacket(buf []byte) (int, error)

	// SetMTU changes the live interface MTU. A reduction always succeeds; an
	// increase succeeds only when authenticatedIncrease is true (TUN-16,
	// TUN-17).
	SetMTU(mtu int, authenticatedIncrease bool) error

	// Stats returns a snapshot of the bounded counters (TUN-25).
	Stats() Stats

	// Close releases the interface. The descriptor is closed exactly once
	// (TUN-08); the non-persistent interface disappears with it (TUN-10).
	Close() error
}
