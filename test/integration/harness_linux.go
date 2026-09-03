//go:build linux && integration

package integration

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// runID scopes every namespace and re-exec this suite creates to one test run.
// A re-executed child (helper target, deliberate-fail scenario) inherits it via
// RED_MPUDP_RUN_ID so parent and child agree on names and leak detection stays
// scoped to this run only (HARNESS-06, HARNESS-33).
var runID = resolveRunID()

func resolveRunID() string {
	if v := os.Getenv("RED_MPUDP_RUN_ID"); v != "" {
		return v
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "pid" + strconv.Itoa(os.Getpid())
	}
	return hex.EncodeToString(b)
}

// nsKey names one of the three namespaces independent of the run suffix.
type nsKey string

const (
	keyClient   nsKey = "client"
	keyServer   nsKey = "server"
	keyInternet nsKey = "internet"
)

func nsFull(k nsKey) string { return "rmp-" + string(k) + "-" + runID }

// nsPrefix is the common prefix of every namespace this run owns.
func nsPrefix() string { return "rmp-" }

func nsSuffix() string { return "-" + runID }

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

	echoPort = 7
	dnsPort  = 53
)

// rootVeths are the veth ends as first created in the root namespace, before
// being moved. Deleting one end deletes its peer, so cleaning these up covers
// every partial-setup state.
var rootVeths = []string{"pa0", "pb0", "up0"}

// Link identifies one impairable link end for the netem helpers.
type Link struct {
	ns  nsKey
	Dev string
}

// Path A and Path B link ends, as seen from each namespace.
var (
	PathAClientSide = Link{keyClient, "pa0"}
	PathAServerSide = Link{keyServer, "pa1"}
	PathBClientSide = Link{keyClient, "pb0"}
	PathBServerSide = Link{keyServer, "pb1"}
)

// Topology is a live three-namespace test network. Build one with NewTopology
// and always defer Close.
type Topology struct {
	t testing.TB

	mu      sync.Mutex
	created bool // at least one ip command ran; cleanup is warranted
	procs   []*exec.Cmd
	logs    []*os.File
}

// NewTopology creates client/server/internet namespaces, both veth paths, the
// server uplink, deterministic subnets, up links, and namespace-local routes in
// both directions (HARNESS-06..17). It fails the test (not skips) on any error,
// having already torn down whatever it created — including namespaces and veth
// ends created before the failing step.
func NewTopology(t testing.TB) *Topology {
	t.Helper()
	skipUnlessPrivileged(t)

	top := &Topology{t: t}
	// From here on any created object must be cleaned up on failure. Mark
	// cleanup as warranted before the first mutation so an early failure still
	// removes the namespaces/veths that did get created.
	top.mu.Lock()
	top.created = true
	top.mu.Unlock()

	c, s, i := nsFull(keyClient), nsFull(keyServer), nsFull(keyInternet)

	fail := func(format string, a ...any) *Topology {
		top.Close()
		t.Fatalf(format, a...)
		return nil // unreachable
	}

	steps := [][]string{
		{"netns", "add", c},
		{"netns", "add", s},
		{"netns", "add", i},

		{"link", "add", "pa0", "type", "veth", "peer", "name", "pa1"},
		{"link", "set", "pa0", "netns", c},
		{"link", "set", "pa1", "netns", s},

		{"link", "add", "pb0", "type", "veth", "peer", "name", "pb1"},
		{"link", "set", "pb0", "netns", c},
		{"link", "set", "pb1", "netns", s},

		{"link", "add", "up0", "type", "veth", "peer", "name", "up1"},
		{"link", "set", "up0", "netns", s},
		{"link", "set", "up1", "netns", i},
	}
	for _, args := range steps {
		if err := ipRoot(args...); err != nil {
			return fail("topology setup (%v): %v", args, err)
		}
	}

	addr := []struct {
		ns   string
		spec string
	}{
		{c, pathAClient + "/24 dev pa0"},
		{s, pathAServer + "/24 dev pa1"},
		{c, pathBClient + "/24 dev pb0"},
		{s, pathBServer + "/24 dev pb1"},
		{s, uplinkServer + "/24 dev up0"},
		{i, uplinkInternet + "/24 dev up1"},
	}
	for _, a := range addr {
		if err := ipIn(a.ns, append([]string{"addr", "add"}, strings.Fields(a.spec)...)...); err != nil {
			return fail("addr add %v: %v", a, err)
		}
	}
	for _, nd := range []struct{ ns, dev string }{
		{c, "pa0"}, {c, "pb0"}, {c, "lo"},
		{s, "pa1"}, {s, "pb1"}, {s, "up0"}, {s, "lo"},
		{i, "up1"}, {i, "lo"},
	} {
		if err := ipIn(nd.ns, "link", "set", nd.dev, "up"); err != nil {
			return fail("link up %v: %v", nd, err)
		}
	}

	// Namespace-local routes in BOTH directions (HARNESS-17):
	//  - client reaches the internet subnet via the server-side path address;
	//  - server relays with a default route into internet-ns;
	//  - internet-ns has return routes to both path subnets via the server.
	routes := []struct{ ns, spec string }{
		{c, uplinkSubnet + " via " + pathAServer + " dev pa0"},
		{s, "0.0.0.0/0 via " + uplinkInternet + " dev up0 metric 100"},
		{i, pathASubnet + " via " + uplinkServer + " dev up1"},
		{i, pathBSubnet + " via " + uplinkServer + " dev up1"},
	}
	for _, r := range routes {
		if err := ipIn(r.ns, append([]string{"route", "add"}, strings.Fields(r.spec)...)...); err != nil {
			return fail("route add %v: %v", r, err)
		}
	}

	if err := nsExec(s, "sysctl", "-q", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fail("enable forwarding: %v", err)
	}

	return top
}

// Close stops every target process and deletes every namespace and root-side
// veth this run created (HARNESS-31, HARNESS-32). It is safe to call more than
// once.
func (top *Topology) Close() {
	top.mu.Lock()
	procs, logs := top.procs, top.logs
	top.procs, top.logs = nil, nil
	warranted := top.created
	top.created = false
	top.mu.Unlock()

	for idx, p := range procs {
		if p.Process != nil {
			_ = p.Process.Kill()
			_, _ = p.Process.Wait()
		}
		f := logs[idx]
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			if b, _ := io.ReadAll(f); len(b) > 0 {
				top.t.Logf("target %s output:\n%s", f.Name(), b)
			}
		}
		_ = f.Close()
		_ = os.Remove(f.Name())
	}

	if !warranted {
		return
	}
	for _, k := range []nsKey{keyClient, keyServer, keyInternet} {
		_ = exec.Command("ip", "netns", "del", nsFull(k)).Run()
	}
	for _, v := range rootVeths {
		_ = exec.Command("ip", "link", "del", v).Run()
	}
}

// ---- impairment (HARNESS-23..27) ----

func (top *Topology) netem(l Link, args ...string) error {
	base := []string{"qdisc", "replace", "dev", l.Dev, "root", "netem"}
	return tcIn(nsFull(l.ns), append(base, args...)...)
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

// SetReorder sets reorder percentage with a fixed correlation and gap-delay so
// the reorder is deterministic (HARNESS-26).
func (top *Topology) SetReorder(l Link, pct float64) error {
	return top.netem(l, "delay", "10ms", "reorder", fmt.Sprintf("%.4f%%", pct), "50%")
}

// SetRate caps egress bandwidth on the link end (HARNESS-27).
func (top *Topology) SetRate(l Link, kbit int) error {
	return top.netem(l, "rate", fmt.Sprintf("%dkbit", kbit))
}

// ClearImpairment removes the netem qdisc from a link end.
func (top *Topology) ClearImpairment(l Link) error {
	return tcIn(nsFull(l.ns), "qdisc", "del", "dev", l.Dev, "root")
}

// ---- link / address / gateway mutation (HARNESS-28..30) ----

func (top *Topology) LinkDown(l Link) error { return ipIn(nsFull(l.ns), "link", "set", l.Dev, "down") }
func (top *Topology) LinkUp(l Link) error   { return ipIn(nsFull(l.ns), "link", "set", l.Dev, "up") }

// SetAddress replaces the primary address on a link end (HARNESS-29).
func (top *Topology) SetAddress(l Link, cidr string) error {
	_ = ipIn(nsFull(l.ns), "addr", "flush", "dev", l.Dev)
	return ipIn(nsFull(l.ns), "addr", "add", cidr, "dev", l.Dev)
}

// SetGateway replaces the default route in a namespace (HARNESS-30).
func (top *Topology) SetGateway(k nsKey, via, dev string) error {
	_ = ipIn(nsFull(k), "route", "del", "default")
	return ipIn(nsFull(k), "route", "add", "default", "via", via, "dev", dev)
}

// ---- targets (HARNESS-18..22) ----

// StartEchoTargets starts UDP-echo, TCP-echo, and DNS targets in internet-ns
// (ICMP reachability needs no process). Each is a re-exec of this test binary
// with RED_MPUDP_HELPER set; output is captured per target and surfaced on
// failure. It blocks until the TCP target actually accepts a connection, or
// returns an error on timeout — no fixed sleep.
func (top *Topology) StartEchoTargets() error {
	i := nsFull(keyInternet)
	for _, tgt := range []struct{ tag, mode, addr string }{
		{"udp-echo", "udp-echo", fmt.Sprintf("%s:%d", uplinkInternet, echoPort)},
		{"tcp-echo", "tcp-echo", fmt.Sprintf("%s:%d", uplinkInternet, echoPort)},
		{"dns", "dns", fmt.Sprintf("%s:%d", uplinkInternet, dnsPort)},
	} {
		if err := top.spawn(i, tgt.tag, tgt.mode, tgt.addr); err != nil {
			return err
		}
	}
	return top.waitReady(i, fmt.Sprintf("%s:%d", uplinkInternet, echoPort), 3*time.Second)
}

// waitReady polls a probe-tcp re-exec inside ns until the address accepts a
// connection.
func (top *Topology) waitReady(ns, addr string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if err := nsExec(ns, os.Args[0], "-test.run=TestMainHelperNoop"); err == nil {
			return nil
		}
		cmd := helperCmd(ns, "probe-tcp", addr)
		if cmd.Run() == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("targets in %s not ready within %s", ns, within)
}

func helperCmd(ns, mode, addr string) *exec.Cmd {
	cmd := exec.Command("ip", "netns", "exec", ns, os.Args[0], "-test.run=TestMainHelperNoop")
	cmd.Env = append(os.Environ(),
		"RED_MPUDP_HELPER="+mode,
		"RED_MPUDP_HELPER_ADDR="+addr,
		"RED_MPUDP_RUN_ID="+runID,
	)
	return cmd
}

func (top *Topology) spawn(ns, tag, mode, addr string) error {
	logf, err := os.CreateTemp("", "rmp-"+tag+"-"+runID+"-*.log")
	if err != nil {
		return err
	}
	cmd := helperCmd(ns, mode, addr)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		os.Remove(logf.Name())
		return err
	}
	top.mu.Lock()
	top.procs = append(top.procs, cmd)
	top.logs = append(top.logs, logf)
	top.mu.Unlock()
	return nil
}

// ---- command helpers ----

func ipRoot(args ...string) error { return execRun("ip", args...) }

func ipIn(ns string, args ...string) error {
	return execRun("ip", append([]string{"-n", ns}, args...)...)
}

func tcIn(ns string, args ...string) error {
	return nsExec(ns, append([]string{"tc"}, args...)...)
}

func nsExec(ns string, argv ...string) error {
	return execRun("ip", append([]string{"netns", "exec", ns}, argv...)...)
}

func execRun(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// LeakedNamespaces returns namespaces belonging to THIS run that are still
// present (HARNESS-33). Scoping by runID avoids false positives from a
// concurrent run or a stale namespace from an unrelated earlier run.
func LeakedNamespaces() []string {
	out, err := exec.Command("ip", "netns", "list").Output()
	if err != nil {
		return nil
	}
	var leaked []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if strings.HasPrefix(name, nsPrefix()) && strings.HasSuffix(name, nsSuffix()) {
			leaked = append(leaked, name)
		}
	}
	return leaked
}
