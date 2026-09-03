//go:build linux

package tun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jsimonetti/rtnetlink"
	"golang.org/x/sys/unix"
)

const devNetTun = "/dev/net/tun"

// linkConfigurer is the slice of netlink the device keeps open for its lifetime
// (SetMTU). It is an interface so tests can substitute a fake.
type linkConfigurer interface {
	setMTU(mtu int) error
	close() error
}

// deadlineReader is implemented by *os.File; it lets ReadPacket honour context
// cancellation.
type deadlineReader interface {
	SetReadDeadline(time.Time) error
}

type linuxDevice struct {
	rw        io.ReadWriteCloser
	name      string
	index     int
	addr      netip.Prefix
	mtu       atomic.Int64
	maxPacket int

	link linkConfigurer

	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool

	stats counters
}

// Open creates a non-persistent TUN interface, configures its address and MTU
// with structured netlink values, brings it up, and verifies the result
// (TUN-03..14). A failure after the descriptor is opened closes it before
// returning (TUN-09).
func Open(cfg Config) (Device, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	fd, err := unix.Open(devNetTun, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("tun: open %s: %w", devNetTun, err)
	}
	// From here every early return must close fd.
	fail := func(format string, a ...any) (Device, error) {
		unix.Close(fd)
		return nil, fmt.Errorf(format, a...)
	}

	ifr, err := unix.NewIfreq(cfg.Name)
	if err != nil {
		return fail("tun: build ifreq: %w", err)
	}
	ifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI) // TUN-05
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		return fail("tun: TUNSETIFF: %w", err)
	}
	name := ifr.Name() // TUN-07

	// TUN-10: the interface is non-persistent by default — it exists only while
	// this descriptor is open and vanishes on Close. v1 never issues
	// TUNSETPERSIST, so it is never marked persistent.

	iface, err := net.InterfaceByName(name)
	if err != nil {
		return fail("tun: look up %s: %w", name, err)
	}
	index := iface.Index

	rtnl, err := rtnetlink.Dial(nil)
	if err != nil {
		return fail("tun: dial rtnetlink: %w", err)
	}
	failNL := func(format string, a ...any) (Device, error) {
		rtnl.Close()
		unix.Close(fd)
		return nil, fmt.Errorf(format, a...)
	}

	// TUN-12 + TUN-13: MTU and up in one structured request.
	if err := rtnl.Link.Set(&rtnetlink.LinkMessage{
		Index:  uint32(index),
		Flags:  unix.IFF_UP,
		Change: unix.IFF_UP,
		Attributes: &rtnetlink.LinkAttributes{
			MTU: uint32(cfg.MTU),
		},
	}); err != nil {
		return failNL("tun: set link mtu/up: %w", err)
	}

	// TUN-11: address via structured netlink values.
	ip := cfg.Address.Addr().AsSlice()
	if err := rtnl.Address.New(&rtnetlink.AddressMessage{
		Family:       unix.AF_INET,
		PrefixLength: uint8(cfg.Address.Bits()),
		Scope:        unix.RT_SCOPE_UNIVERSE,
		Index:        uint32(index),
		Attributes: &rtnetlink.AddressAttributes{
			Address: net.IP(ip),
			Local:   net.IP(ip),
		},
	}); err != nil {
		return failNL("tun: add address %s: %w", cfg.Address, err)
	}

	// TUN-14: read back and verify.
	if err := verifyLink(rtnl, index, cfg.MTU); err != nil {
		return failNL("tun: verify link: %w", err)
	}
	if err := verifyAddress(rtnl, index, cfg.Address); err != nil {
		return failNL("tun: verify address: %w", err)
	}

	d := &linuxDevice{
		rw:        os.NewFile(uintptr(fd), devNetTun+":"+name),
		name:      name,
		index:     index,
		addr:      cfg.Address,
		maxPacket: cfg.maxPacket(),
		link:      &rtnlLink{conn: rtnl, index: index},
	}
	d.mtu.Store(int64(cfg.MTU))
	return d, nil
}

func verifyLink(rtnl *rtnetlink.Conn, index, wantMTU int) error {
	lm, err := rtnl.Link.Get(uint32(index))
	if err != nil {
		return err
	}
	if lm.Attributes == nil || int(lm.Attributes.MTU) != wantMTU {
		return fmt.Errorf("mtu readback = %v, want %d", lm.Attributes, wantMTU)
	}
	if lm.Flags&unix.IFF_UP == 0 {
		return errors.New("interface is not up after set")
	}
	return nil
}

func verifyAddress(rtnl *rtnetlink.Conn, index int, want netip.Prefix) error {
	addrs, err := rtnl.Address.List()
	if err != nil {
		return err
	}
	wantIP := want.Addr()
	for _, a := range addrs {
		if int(a.Index) != index || a.Attributes == nil {
			continue
		}
		got, ok := netip.AddrFromSlice(a.Attributes.Address)
		if ok && got.Unmap() == wantIP && int(a.PrefixLength) == want.Bits() {
			return nil
		}
	}
	return fmt.Errorf("address %s not present on ifindex %d after add", want, index)
}

func (d *linuxDevice) Name() string          { return d.name }
func (d *linuxDevice) Index() int            { return d.index }
func (d *linuxDevice) MTU() int              { return int(d.mtu.Load()) }
func (d *linuxDevice) Address() netip.Prefix { return d.addr }
func (d *linuxDevice) Stats() Stats          { return d.stats.snapshot() }

// ReadPacket implements TUN-18, TUN-19, TUN-20.
func (d *linuxDevice) ReadPacket(ctx context.Context, buf []byte) (int, error) {
	if d.closed.Load() {
		return 0, ErrClosed
	}
	if len(buf) <= d.maxPacket {
		return 0, fmt.Errorf("%w: len(buf)=%d, need > %d", ErrBufferTooSmall, len(buf), d.maxPacket)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	if dr, ok := d.rw.(deadlineReader); ok {
		stop := context.AfterFunc(ctx, func() { _ = dr.SetReadDeadline(time.Unix(0, 1)) })
		defer func() {
			stop()
			_ = dr.SetReadDeadline(time.Time{})
		}()
	}

	n, err := d.rw.Read(buf)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr // TUN-19: cancellation, not a descriptor failure
		}
		if errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed) {
			return 0, ErrClosed
		}
		d.stats.rxErrors.Add(1)
		return 0, fmt.Errorf("tun read: %w", err) // permanent descriptor failure
	}
	if n > d.maxPacket {
		d.stats.rxOversize.Add(1)
		return 0, fmt.Errorf("%w: %d > %d", ErrPacketTooLarge, n, d.maxPacket) // TUN-20
	}
	d.stats.rxPackets.Add(1)
	d.stats.rxBytes.Add(uint64(n))
	return n, nil
}

// WritePacket implements TUN-21, TUN-22.
func (d *linuxDevice) WritePacket(buf []byte) (int, error) {
	if d.closed.Load() {
		return 0, ErrClosed
	}
	if len(buf) == 0 {
		return 0, errors.New("tun: refusing to write an empty packet")
	}
	n, err := d.rw.Write(buf)
	if err != nil {
		d.stats.txErrors.Add(1)
		if errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed) {
			return 0, ErrClosed
		}
		return n, fmt.Errorf("tun write: %w", err)
	}
	if n != len(buf) {
		d.stats.txShort.Add(1)
		return n, fmt.Errorf("%w: wrote %d of %d", ErrShortWrite, n, len(buf))
	}
	d.stats.txPackets.Add(1)
	d.stats.txBytes.Add(uint64(n))
	return n, nil
}

// SetMTU implements TUN-16 and TUN-17.
func (d *linuxDevice) SetMTU(mtu int, authenticatedIncrease bool) error {
	if d.closed.Load() {
		return ErrClosed
	}
	cur := int(d.mtu.Load())
	applied, err := guardMTU(cur, mtu, authenticatedIncrease)
	if err != nil {
		return err
	}
	if applied == cur {
		return nil
	}
	if err := d.link.setMTU(applied); err != nil {
		return fmt.Errorf("tun: set mtu %d: %w", applied, err)
	}
	d.mtu.Store(int64(applied))
	return nil
}

// Close implements TUN-08 and TUN-10.
func (d *linuxDevice) Close() error {
	d.closeOnce.Do(func() {
		d.closed.Store(true)
		errFile := d.rw.Close()
		errNL := d.link.close()
		d.closeErr = errors.Join(errFile, errNL)
	})
	return d.closeErr
}

// rtnlLink is the production linkConfigurer.
type rtnlLink struct {
	conn  *rtnetlink.Conn
	index int
}

func (r *rtnlLink) setMTU(mtu int) error {
	return r.conn.Link.Set(&rtnetlink.LinkMessage{
		Index:      uint32(r.index),
		Attributes: &rtnetlink.LinkAttributes{MTU: uint32(mtu)},
	})
}

func (r *rtnlLink) close() error { return r.conn.Close() }
