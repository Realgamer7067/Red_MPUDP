//go:build linux && integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("RED_MPUDP_HELPER"); mode != "" {
		runHelper(mode, os.Getenv("RED_MPUDP_HELPER_ADDR"))
		return
	}
	code := m.Run()

	// HARNESS-33: fail loudly if THIS run leaked a namespace, and clean it.
	if leaked := LeakedNamespaces(); len(leaked) > 0 {
		for _, ns := range leaked {
			_ = exec.Command("ip", "netns", "del", ns).Run()
		}
		fmt.Fprintf(os.Stderr, "integration run %s leaked namespaces: %s\n", runID, strings.Join(leaked, ", "))
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// TestMainHelperNoop is the -test.run target the helper re-exec uses; the real
// work happens in TestMain before m.Run.
func TestMainHelperNoop(t *testing.T) {}

// TestTopologyLifecycle exercises HARNESS-06..17, HARNESS-23..32: build the
// topology twice, prove end-to-end client<->internet connectivity each round,
// apply and clear impairment, take a link down and up, tear down, and confirm
// this run leaked no namespace. Covers gate clause 1 (repeatable create /
// impair / destroy).
func TestTopologyLifecycle(t *testing.T) {
	skipUnlessPrivileged(t)

	for round := 0; round < 2; round++ {
		func() {
			top := NewTopology(t)
			defer top.Close()

			if err := top.StartEchoTargets(); err != nil {
				t.Fatalf("round %d: start targets: %v", round, err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if err := clientCanReachInternet(ctx); err != nil {
				t.Fatalf("round %d: connectivity: %v", round, err)
			}

			if err := top.SetDelay(PathAClientSide, 20*time.Millisecond); err != nil {
				t.Fatalf("set delay: %v", err)
			}
			if err := top.SetLoss(PathAServerSide, 10); err != nil {
				t.Fatalf("set loss: %v", err)
			}
			if err := top.SetReorder(PathBServerSide, 5); err != nil {
				t.Fatalf("set reorder: %v", err)
			}
			if err := top.ClearImpairment(PathAClientSide); err != nil {
				t.Fatalf("clear impairment: %v", err)
			}

			if err := top.LinkDown(PathBClientSide); err != nil {
				t.Fatalf("link down: %v", err)
			}
			if err := top.LinkUp(PathBClientSide); err != nil {
				t.Fatalf("link up: %v", err)
			}
		}()

		if leaked := LeakedNamespaces(); len(leaked) > 0 {
			t.Fatalf("round %d leaked namespaces: %v", round, leaked)
		}
	}
}

// TestFailedTestLeavesNoNamespace covers gate clause 2. It runs a scenario that
// deliberately fails an assertion *while a topology is live* as a subprocess
// (so the failure does not mark this test failed), asserts the subprocess exits
// nonzero, and asserts that no namespace uniquely named for that run survives.
func TestFailedTestLeavesNoNamespace(t *testing.T) {
	skipUnlessPrivileged(t)

	childRun := "fail" + runID
	cmd := exec.Command(os.Args[0], "-test.run=TestDeliberateFailWithLiveTopology$", "-test.v")
	cmd.Env = append(os.Environ(),
		"RED_MPUDP_RUN_DELIBERATE_FAIL=1",
		"RED_MPUDP_RUN_ID="+childRun,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("deliberate-fail subprocess exited 0; want nonzero.\n%s", out)
	}
	if !strings.Contains(string(out), "deliberate failure with a live topology") {
		t.Fatalf("subprocess did not reach the deliberate failure:\n%s", out)
	}

	// No namespace named for the child run may remain.
	list, _ := exec.Command("ip", "netns", "list").Output()
	for _, line := range strings.Split(string(list), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && strings.HasSuffix(f[0], "-"+childRun) {
			_ = exec.Command("ip", "netns", "del", f[0]).Run()
			t.Fatalf("deliberate-fail subprocess leaked namespace %s", f[0])
		}
	}
}

// TestDeliberateFailWithLiveTopology is only run as the subprocess spawned by
// TestFailedTestLeavesNoNamespace. It builds a topology and fails while it is
// live; the deferred Close must still remove every namespace.
func TestDeliberateFailWithLiveTopology(t *testing.T) {
	if os.Getenv("RED_MPUDP_RUN_DELIBERATE_FAIL") == "" {
		t.Skip("subprocess-only scenario for TestFailedTestLeavesNoNamespace")
	}
	skipUnlessPrivileged(t)
	top := NewTopology(t)
	defer top.Close()
	t.Fatalf("deliberate failure with a live topology")
}

// TestRecoverInNamespace covers JOURNAL-24 and the journal gate's end-to-end
// clause: mutation-journal recovery runs entirely inside client-ns, removes the
// route it owns, and leaves an unrelated route in the same table untouched.
func TestRecoverInNamespace(t *testing.T) {
	skipUnlessPrivileged(t)

	top := NewTopology(t)
	defer top.Close()

	cmd := helperCmd(nsFull(keyClient), "journal-recover", "")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("in-namespace journal recovery failed: %v\n%s", err, out)
	}
}

// clientCanReachInternet re-execs the test binary inside client-ns in probe-tcp
// mode: it dials the TCP-echo target in internet-ns end to end through the
// server relay. Stronger than an ICMP check and needs no tool outside
// preflight's requiredTools set.
func clientCanReachInternet(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Second)
	}
	addr := fmt.Sprintf("%s:%d", uplinkInternet, echoPort)
	var lastErr error
	for time.Now().Before(deadline) {
		out, err := helperCmd(nsFull(keyClient), "probe-tcp", addr).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		time.Sleep(100 * time.Millisecond)
	}
	return lastErr
}
