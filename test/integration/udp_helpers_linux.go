//go:build linux && integration

package integration

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// requireBinary skips when an external tool the test depends on is absent, so a
// missing tcpdump reads as "not proven here" rather than as a failure.
func requireBinary(t testing.TB, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed; this check cannot run", name)
	}
}

// capture is a running tcpdump watching one interface for outbound UDP to a
// port. It exists so a test can assert both that a datagram appeared on the
// intended interface and that none appeared on the other one.
type capture struct {
	cmd  *exec.Cmd
	out  *bytes.Buffer
	dev  string
	stop func()
}

// startCapture begins capturing outbound UDP to dstPort on dev inside ns.
func startCapture(t testing.TB, ns, dev string, dstPort int) *capture {
	t.Helper()
	buf := &bytes.Buffer{}
	cmd := exec.Command("ip", "netns", "exec", ns,
		"tcpdump", "-n", "-l", "-i", dev, "--immediate-mode", "-Q", "out",
		fmt.Sprintf("udp dst port %d", dstPort))
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start tcpdump on %s: %v", dev, err)
	}
	c := &capture{cmd: cmd, out: buf, dev: dev}
	c.stop = func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
	t.Cleanup(c.stop)
	// tcpdump needs a moment to attach before traffic starts, or the capture
	// silently misses the very packets it exists to observe.
	time.Sleep(300 * time.Millisecond)
	return c
}

func (c *capture) count(t testing.TB) int {
	t.Helper()
	time.Sleep(300 * time.Millisecond) // let in-flight packets reach tcpdump
	c.stop()
	n := 0
	for _, line := range strings.Split(c.out.String(), "\n") {
		// Data lines carry an "IP a.b.c.d.port > ..." prefix; status lines do not.
		if strings.Contains(line, " IP ") && strings.Contains(line, " > ") {
			n++
		}
	}
	t.Logf("capture on %s saw %d datagram(s)", c.dev, n)
	return n
}

// ipNS runs an ip(8) command inside a namespace.
func ipNS(ns string, args ...string) error { return ipIn(ns, args...) }

// helperProc is a spawned helper whose output the test inspects on exit.
type helperProc struct {
	cmd *exec.Cmd
	out *bytes.Buffer
}

// spawnHelper starts a helper mode in a namespace without waiting for it.
func (top *Topology) spawnHelper(t testing.TB, ns, mode, arg string) *helperProc {
	t.Helper()
	buf := &bytes.Buffer{}
	cmd := helperCmd(ns, mode, arg)
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s helper: %v", mode, err)
	}
	h := &helperProc{cmd: cmd, out: buf}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	// Give the helper time to bind before the sender starts.
	time.Sleep(300 * time.Millisecond)
	return h
}

// wait blocks for the helper and returns its combined output.
func (h *helperProc) wait() (string, error) {
	err := h.cmd.Wait()
	return h.out.String(), err
}
