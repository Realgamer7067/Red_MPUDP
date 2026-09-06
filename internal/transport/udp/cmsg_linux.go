//go:build linux

package udp

import (
	"net"
	"net/netip"
	"unsafe"

	"github.com/Realgamer7067/Red_MPUDP/internal/transport"
	"golang.org/x/sys/unix"
)

// net_ErrClosed is net.ErrClosed, referenced without importing net elsewhere.
var net_ErrClosed = net.ErrClosed

// sockaddrToAddrPort converts a kernel sockaddr. An unexpected family yields
// the zero value rather than a guess.
func sockaddrToAddrPort(sa unix.Sockaddr) netip.AddrPort {
	switch v := sa.(type) {
	case *unix.SockaddrInet4:
		return netip.AddrPortFrom(netip.AddrFrom4(v.Addr), uint16(v.Port))
	case *unix.SockaddrInet6:
		a := netip.AddrFrom16(v.Addr)
		if a4 := a.Unmap(); a4.Is4() {
			a = a4
		}
		return netip.AddrPortFrom(a, uint16(v.Port))
	}
	return netip.AddrPort{}
}

// parsePktinfo extracts the local destination address and receive ifindex from
// IP_PKTINFO ancillary data (UDP-27, UDP-31). Everything here is a kernel
// structure of known size; nothing from the datagram payload is read.
func parsePktinfo(oob []byte) (netip.Addr, int, bool) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return netip.Addr{}, 0, false
	}
	for _, m := range msgs {
		if m.Header.Level != unix.IPPROTO_IP || m.Header.Type != unix.IP_PKTINFO {
			continue
		}
		if len(m.Data) < int(unsafe.Sizeof(unix.Inet4Pktinfo{})) {
			continue
		}
		pi := (*unix.Inet4Pktinfo)(unsafe.Pointer(&m.Data[0]))
		return netip.AddrFrom4(pi.Spec_dst), int(pi.Ifindex), true
	}
	return netip.Addr{}, 0, false
}

// parseErrorQueue turns IP_RECVERR ancillary data into a PathError
// (UDP-34..37).
//
// The reported MTU is taken from sock_extended_err.Info only for a genuine
// ICMP fragmentation-needed event or a local EMSGSIZE, which is where the
// kernel places a next-hop MTU; for any other origin Info means something else
// entirely and is ignored. The quoted peer comes from the offender sockaddr the
// kernel appends after the structure. An event that maps to neither is reported
// with zero values rather than guessed at, and one whose origin is unrecognised
// is ignored outright (UDP-37).
func parseErrorQueue(oob []byte, offender unix.Sockaddr) (transport.PathError, bool) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return transport.PathError{}, false
	}
	const seeSize = int(unsafe.Sizeof(unix.SockExtendedErr{}))
	for _, m := range msgs {
		if m.Header.Level != unix.IPPROTO_IP || m.Header.Type != unix.IP_RECVERR {
			continue
		}
		if len(m.Data) < seeSize {
			continue
		}
		see := (*unix.SockExtendedErr)(unsafe.Pointer(&m.Data[0]))

		var pe transport.PathError
		switch see.Origin {
		case unix.SO_EE_ORIGIN_ICMP:
			// Only fragmentation-needed carries a next-hop MTU (UDP-35).
			if see.Type == 3 && see.Code == 4 {
				pe.MTU = int(see.Info)
			}
		case unix.SO_EE_ORIGIN_LOCAL:
			pe.Local = true
			if see.Errno == uint32(unix.EMSGSIZE) {
				pe.MTU = int(see.Info)
			}
		default:
			// ICMP6 and anything else this IPv4-only socket should not see.
			continue // UDP-37
		}
		pe.Peer = sockaddrToAddrPort(offender) // UDP-36; zero when absent
		return pe, true
	}
	return transport.PathError{}, false
}

// buildPktinfo encodes an IP_PKTINFO control message selecting the outbound
// interface for one datagram (UDP-28). The server's single socket serves every
// client path, so a reply must be able to name the interface the request
// arrived on — a plain sendto would let the routing table pick, which on a
// multi-homed server can answer out of the wrong uplink.
func buildPktinfo(ifIndex int) []byte {
	pi := unix.Inet4Pktinfo{Ifindex: int32(ifIndex)}
	const dataLen = int(unsafe.Sizeof(pi))
	buf := make([]byte, unix.CmsgSpace(dataLen))
	h := (*unix.Cmsghdr)(unsafe.Pointer(&buf[0]))
	h.Level = unix.IPPROTO_IP
	h.Type = unix.IP_PKTINFO
	h.SetLen(unix.CmsgLen(dataLen))
	copy(buf[unix.CmsgLen(0):], (*(*[dataLen]byte)(unsafe.Pointer(&pi)))[:])
	return buf
}
