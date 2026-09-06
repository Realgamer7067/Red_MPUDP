package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// design bounds, all from the 2026-09-02 design (revision 4).
const (
	minTunMTU      = 1112
	maxTunMTU      = 1400
	minPaths       = 1
	maxPaths       = 4
	minDedupWindow = 4096
	maxDedupWindow = 1048576
	minProbe       = 100_000_000   // 100ms in ns
	maxProbe       = 5_000_000_000 // 5s in ns
	ifaceNameMax   = 15            // Linux IFNAMSIZ - 1
)

// errList accumulates validation failures so a single call reports every
// problem rather than only the first.
type errList struct{ errs []string }

func (e *errList) addf(format string, a ...any) { e.errs = append(e.errs, fmt.Sprintf(format, a...)) }

func (e *errList) err() error {
	switch len(e.errs) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("%s", e.errs[0])
	default:
		return fmt.Errorf("%d problems:\n  - %s", len(e.errs), strings.Join(e.errs, "\n  - "))
	}
}

// checkServerAddr enforces CONF-08 and CONF-09: a literal IPv4 address and
// port, not unspecified / multicast / broadcast / zero.
func checkServerAddr(field string, ap netip.AddrPort, e *errList) {
	if !ap.IsValid() {
		e.addf("%s: must be a literal IPv4 address and port", field)
		return
	}
	a := ap.Addr()
	if !a.Is4() {
		e.addf("%s: must be a literal IPv4 address (got %s)", field, a)
	}
	if ap.Port() == 0 {
		e.addf("%s: port must be non-zero", field)
	}
	switch {
	case a.IsUnspecified():
		e.addf("%s: address must not be unspecified (0.0.0.0)", field)
	case a.IsMulticast():
		e.addf("%s: address must not be multicast", field)
	case a == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		e.addf("%s: address must not be the broadcast address", field)
	}
}

// checkListenAddr allows an unspecified bind address (servers listen on
// 0.0.0.0) but still requires IPv4 and a non-zero port.
func checkListenAddr(field string, ap netip.AddrPort, e *errList) {
	if !ap.IsValid() || !ap.Addr().Is4() {
		e.addf("%s: must be a literal IPv4 address and port", field)
		return
	}
	if ap.Port() == 0 {
		e.addf("%s: port must be non-zero", field)
	}
}

// checkMetricsAddr enforces CONF-29: a listener outside loopback needs an
// explicit opt-in.
func checkMetricsAddr(field string, ap netip.AddrPort, allowPublic bool, e *errList) {
	if !ap.IsValid() {
		return // metrics disabled
	}
	if !ap.Addr().Is4() && !ap.Addr().Is6() {
		e.addf("%s: invalid listen address", field)
		return
	}
	if !ap.Addr().IsLoopback() && !allowPublic {
		e.addf("%s: %s is not loopback; set metrics_allow_public: true to expose metrics off-host", field, ap.Addr())
	}
}

func checkDedupWindow(n int, e *errList) {
	if n < minDedupWindow || n > maxDedupWindow {
		e.addf("session.dedup_window_packets: %d outside [%d, %d]", n, minDedupWindow, maxDedupWindow)
		return
	}
	if n&(n-1) != 0 {
		e.addf("session.dedup_window_packets: %d is not a power of two", n)
	}
}

func checkProbeInterval(field string, d Duration, e *errList) {
	ns := int64(d.Std())
	if ns < minProbe || ns > maxProbe {
		e.addf("%s: %s outside [100ms, 5s]", field, d)
	}
}

func checkTunMTU(field string, mtu int, e *errList) {
	if mtu < minTunMTU || mtu > maxTunMTU {
		e.addf("%s: %d outside [%d, %d]", field, mtu, minTunMTU, maxTunMTU)
	}
}

func checkIfaceName(name string, e *errList) {
	switch {
	case name == "":
		e.addf("interface name must not be empty")
	case len(name) > ifaceNameMax:
		e.addf("interface name %q exceeds %d bytes (would be truncated by the kernel)", name, ifaceNameMax)
	case strings.ContainsAny(name, "/ \t\n"):
		e.addf("interface name %q contains an invalid character", name)
	}
}

// requireDur rejects non-positive durations.
func requireDur(field string, d Duration, e *errList) {
	if d.Std() <= 0 {
		e.addf("%s: must be a positive duration", field)
	}
}
