//go:build linux && integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Namespace names used by the topology. They are suffixed per test process
// (HARNESS-06) so parallel packages do not collide.
const (
	nsClient   = "rmp-client"
	nsServer   = "rmp-server"
	nsInternet = "rmp-inet"
)

// Deterministic addressing (HARNESS-15).
const (
	pathASubnet = "10.77.1.0/24"
	pathAClient = "10.77.1.2"
	pathAServer = "10.77.1.1"

	pathBSubnet = "10.77.2.0/24"
	pathBClient = "10.77.2.2"
	pathBServer = "10.77.2.1"

	uplinkSubnet   = "10.77.9.0/24"
	uplinkServer   = "10.77.9.1"
	uplinkInternet = "10.77.9.9"
)

// Link identifies one impairable link end for the netem helpers.
type Link struct {
	NS  string // namespace holding the device
	Dev string // device name inside that namespace
}

// Path A and Path B, as seen from each namespace.
var (
	PathAClientSide = Link{nsClient, "pa0"}
	PathAServerSide = Link{nsServer, "pa1"}
	PathBClientSide = Link{nsClient, "pb0"}
	PathBServerSide = Link{nsServer, "pb1"}
)

// Topology is a live three-namespace test network. Build one with NewTopology
// and always defer Close.
type Topology struct {
	t       testing.TB
	suffix  string
	nsNames []string

	mu    sync.Mutex
	procs []*exec.Cmd
	logs  []*os.File
}

func nsName(base, suffix string) string { return base + "-" + suffix }

// NewTopology creates client/server/internet namespaces, both veth paths, the
// server uplink, deterministic subnets, up links, and namespace-local routes
// (HARNESS-06..17). It fails the test (not skips) on any error, having already
// torn down whatever it created.
func NewTopology(t testing.TB) *Topology {
	t.Helper()
	skipUnlessPrivileged(t)

	suffix := strconv.Itoa(os.Getpid())
	top := &Topology{t: t, suffix: suffix}

	c := nsName(nsClient, suffix)
	s := nsName(nsServer, suffix)
	i := nsName(nsInternet, suffix)

	steps := [][]string{
		{"netns", "add", c},
		{"netns", "add", s},
		{"netns", "add", i},

		// Path A veth pair, ends moved into client/server (HARNESS-10, 11).
		{"link", "add", "pa0", "type", "veth", "peer", "name", "pa1"},
		{"link", "set", "pa0", "netns", c},
		{"link", "set", "pa1", "netns", s},

		// Path B veth pair (HARNESS-12, 13).
		{"link", "add", "pb0", "type", "veth", "peer", "name", "pb1"},
		{"link", "set", "pb0", "netns", c},
		{"link", "set", "pb1", "netns", s},

		// Server <-> internet uplink (HARNESS-14).
		{"link", "add", "up0", "type", "veth", "peer", "name", "up1"},
		{"link", "set", "up0", "netns", s},
		{"link", "set", "up1", "netns", i},
	}
	for _, args := range steps {
		if err := ipRun(top, "", args...); err != nil {
			top.Close()
			t.Fatalf("topology setup (%v): %v", args, err)
		}
	}
	top.nsNames = []string{c, s, i}

	// Addresses + link up (HARNESS-15, 16).
	addr := [][2]string{
		// ns, "ip/prefixlen dev DEV"
		{c, pathAClient + "/24 dev pa0"},
		{s, pathAServer + "/24 dev pa1"},
		{c, pathBClient + "/24 dev pb0"},
		{s, pathBServer + "/24 dev pb1"},
		{s, uplinkServer + "/24 dev up0"},
		{i, uplinkInternet + "/24 dev up1"},
	}
	for _, a := range addr {
		if err := ipRun(top, a[0], append([]string{"addr", "add"}, strings.Fields(a[1])...)...); err != nil {
			top.Close()
			t.Fatalf("addr add %v: %v", a, err)
		}
	}
	for _, nd := range [][2]string{
		{c, "pa0"}, {c, "pb0"}, {c, "lo"},
		{s, "pa1"}, {s, "pb1"}, {s, "up0"}, {s, "lo"},
		{i, "up1"}, {i, "lo"},
	} {
		if err := ipRun(top, nd[0], "link", "set", nd[1], "up"); err != nil {
			top.Close()
			t.Fatalf("link up %v: %v", nd, err)
		}
	}

	// Namespace-local routes (HARNESS-17): client reaches the internet subnet
	// via either server-side path address; server forwards to internet-ns.
	routes := [][2]string{
		{c, uplinkSubnet + " via " + pathAServer + " dev pa0"},
		{s, "0.0.0.0/0 via " + uplinkInternet + " dev up0 metric 100"},
	}
	for _, r := range routes {
		if err := ipRun(top, r[0], append([]string{"route", "add"}, strings.Fields(r[1])...)...); err != nil {
			top.Close()
			t.Fatalf("route add %v: %v", r, err)
		}
	}
	// Enable forwarding in server-ns so it can relay to internet-ns.
	if err := nsExec(top, s, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1"); err != nil {
		top.Close()
		t.Fatalf("enable forwarding: %v", err)
	}

	return top
}

// Close deletes every namespace this topology created and stops every target
// process (HARNESS-31, HARNESS-32). It is safe to call more than once.
func (top *Topology) Close() {
	top.mu.Lock()
	for _, p := range top.procs {
		if p.Process != nil {
			_ = p.Process.Kill()
		}
	}
	for _, f := range top.logs {
		_ = f.Close()
	}
	top.procs, top.logs = nil, nil
	top.mu.Unlock()

	for _, ns := range top.nsNames {
		_ = exec.Command("ip", "netns", "del", ns).Run()
	}
	top.nsNames = nil
}

// ---- impairment (HARNESS-23..27) ----

func (top *Topology) netem(l Link, args ...string) error {
	// replace is idempotent whether or not a qdisc is already present.
	base := []string{"qdisc", "replace", "dev", l.Dev, "root", "netem"}
	return tcRun(top, l.NS, append(base, args...)...)
}

// SetDelay adds one-way delay on the given link end (HARNESS-23).
func (top *Topology) SetDelay(l Link, d time.Duration) error {
	return top.netem(l, "delay", fmt.Sprintf("%dms", d.Milliseconds()))
}

// SetLoss sets independent loss percentage on the given link end (HARNESS-24).
func (top *Topology) SetLoss(l Link, pct float64) error {
	return top.netem(l, "loss", fmt.Sprintf("%.4f%%", pct))
}

// SetDuplication sets duplication percentage (HARNESS-25).
func (top *Topology) SetDuplication(l Link, pct float64) error {
	return top.netem(l, "duplicate", fmt.Sprintf("%.4f%%", pct))
}

// SetReorder sets reorder percentage with a fixed correlation and a small
// gap-delay so the reorder is deterministic (HARNESS-26).
func (top *Topology) SetReorder(l Link, pct float64) error {
	return top.netem(l, "delay", "10ms", "reorder", fmt.Sprintf("%.4f%%", pct), "50%")
}

// SetRate caps egress bandwidth on the link end (HARNESS-27).
func (top *Topology) SetRate(l Link, kbit int) error {
	return top.netem(l, "rate", fmt.Sprintf("%dkbit", kbit))
}

// ClearImpairment removes the netem qdisc from a link end.
func (top *Topology) ClearImpairment(l Link) error {
	return tcRun(top, l.NS, "qdisc", "del", "dev", l.Dev, "root")
}

// ---- link / address / gateway mutation (HARNESS-28..30) ----

func (top *Topology) LinkDown(l Link) error { return ipRun(top, l.NS, "link", "set", l.Dev, "down") }
func (top *Topology) LinkUp(l Link) error   { return ipRun(top, l.NS, "link", "set", l.Dev, "up") }

// SetAddress replaces the primary address on a link end (HARNESS-29).
func (top *Topology) SetAddress(l Link, cidr string) error {
	_ = ipRun(top, l.NS, "addr", "flush", "dev", l.Dev)
	return ipRun(top, l.NS, "addr", "add", cidr, "dev", l.Dev)
}

// SetGateway replaces the default route in a namespace (HARNESS-30).
func (top *Topology) SetGateway(ns, via, dev string) error {
	_ = ipRun(top, ns, "route", "del", "default")
	return ipRun(top, ns, "route", "add", "default", "via", via, "dev", dev)
}

// ---- targets (HARNESS-18..22) ----

// StartEchoTargets starts ICMP-reachable, UDP-echo, TCP-echo, and DNS targets
// in internet-ns. Subprocess output is captured per test (HARNESS-22).
func (top *Topology) StartEchoTargets() error {
	i := nsName(nsInternet, top.suffix)
	// ICMP reachability needs nothing beyond up1 being up (HARNESS-18). The
	// other three targets are helper processes re-executing this test binary
	// with RED_MPUDP_HELPER set (see TestMain).
	for _, tgt := range []struct{ tag, mode, addr string }{
		{"udp-echo", "udp-echo", uplinkInternet + ":7"},
		{"tcp-echo", "tcp-echo", uplinkInternet + ":7"},
		{"dns", "dns", uplinkInternet + ":53"},
	} {
		if err := top.spawn(i, tgt.tag, tgt.mode, tgt.addr); err != nil {
			return err
		}
	}
	time.Sleep(150 * time.Millisecond) // let listeners bind
	return nil
}

func (top *Topology) spawn(ns, tag, mode, addr string) error {
	logf, err := os.CreateTemp("", "rmp-"+tag+"-*.log")
	if err != nil {
		return err
	}
	cmd := exec.Command("ip", "netns", "exec", ns, os.Args[0], "-test.run=TestMainHelperNoop")
	cmd.Env = append(os.Environ(), "RED_MPUDP_HELPER="+mode, "RED_MPUDP_HELPER_ADDR="+addr)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		return err
	}
	top.mu.Lock()
	top.procs = append(top.procs, cmd)
	top.logs = append(top.logs, logf)
	top.mu.Unlock()
	return nil
}

// ---- command helpers ----

func ipRun(top *Topology, ns string, args ...string) error {
	if ns == "" {
		return execRun(top, "ip", args...)
	}
	return execRun(top, "ip", append([]string{"-n", ns}, args...)...)
}

func tcRun(top *Topology, ns string, args ...string) error {
	return nsExec(top, ns, append([]string{"tc"}, args...)...)
}

func nsExec(top *Topology, ns string, argv ...string) error {
	return execRun(top, "ip", append([]string{"netns", "exec", ns}, argv...)...)
}

func execRun(top *Topology, name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// LeakedNamespaces returns any rmp-* namespace still present (HARNESS-33).
func LeakedNamespaces() []string {
	out, err := exec.Command("ip", "netns", "list").Output()
	if err != nil {
		return nil
	}
	var leaked []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.Fields(line)
		if len(name) == 0 {
			continue
		}
		if strings.HasPrefix(name[0], "rmp-") {
			leaked = append(leaked, name[0])
		}
	}
	return leaked
}
