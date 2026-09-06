//go:build linux && integration

package integration

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
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
	var once sync.Once
	c.stop = func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait() // reap, so no zombie outlives the test
		})
	}
	t.Cleanup(c.stop)

	// Wait for tcpdump to say it is listening rather than sleeping a guess: a
	// capture that attaches late silently misses the packets it exists to
	// observe, which would turn a real leak into a passing test.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "listening on") {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.stop()
	t.Fatalf("tcpdump on %s never reported it was listening:\n%s", dev, buf.String())
	return nil
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
	cmd  *exec.Cmd
	out  *bytes.Buffer
	once sync.Once
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
		h.once.Do(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait() // reap; Kill alone leaves a zombie
		})
	})
	return h
}

// waitReadyLine blocks until the helper prints its "ready" line, so a sender
// never transmits into a socket that has not been bound yet.
func (h *helperProc) waitReadyLine(t testing.TB, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if strings.Contains(h.out.String(), "ready ") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("helper never signalled readiness within %s:\n%s", within, h.out.String())
}

// wait blocks for the helper and returns its combined output. It is safe
// alongside the cleanup reaper: whichever runs first performs the Wait.
func (h *helperProc) wait() (string, error) {
	var err error
	h.once.Do(func() { err = h.cmd.Wait() })
	return h.out.String(), err
}
