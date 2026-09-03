//go:build linux && integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("RED_MPUDP_HELPER"); mode != "" {
		runHelper(mode, os.Getenv("RED_MPUDP_HELPER_ADDR"))
		return
	}
	code := m.Run()

	// HARNESS-33: fail loudly if the suite leaked a namespace.
	if leaked := LeakedNamespaces(); len(leaked) > 0 {
		for _, ns := range leaked {
			_ = exec.Command("ip", "netns", "del", ns).Run()
		}
		os.Stderr.WriteString("integration suite leaked namespaces: " + join(leaked) + "\n")
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

// TestMainHelperNoop is the -test.run target the helper re-exec uses; the real
// work happens in TestMain before m.Run.
func TestMainHelperNoop(t *testing.T) {}

// TestTopologyLifecycle exercises HARNESS-06..17, HARNESS-23..32: build the
// topology, prove client<->server<->internet connectivity, apply impairment,
// take a link down and up, then tear everything down and confirm no leak.
func TestTopologyLifecycle(t *testing.T) {
	skipUnlessPrivileged(t)

	for round := 0; round < 2; round++ { // repeatable (gate clause 1)
		func() {
			top := NewTopology(t)
			defer top.Close()

			if err := top.StartEchoTargets(); err != nil {
				t.Fatalf("start targets: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			// Reach the UDP echo target from client-ns via path A.
			if err := clientCanReachInternet(ctx, top); err != nil {
				t.Fatalf("round %d: connectivity: %v", round, err)
			}

			// Impairment must apply and clear without error.
			if err := top.SetDelay(PathAClientSide, 20*time.Millisecond); err != nil {
				t.Fatalf("set delay: %v", err)
			}
			if err := top.SetLoss(PathAServerSide, 10); err != nil {
				t.Fatalf("set loss: %v", err)
			}
			if err := top.ClearImpairment(PathAClientSide); err != nil {
				t.Fatalf("clear impairment: %v", err)
			}

			// Link down/up on path B.
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

// TestFailedTestLeavesNoNamespace covers gate clause 2. What it actually
// proves: a t.Fatalf after `defer top.Close()` still runs Close (Fatalf calls
// runtime.Goexit, which runs deferred funcs), so the deferred-cleanup path
// leaves no namespace behind. What it does NOT prove and still needs the
// privileged run: a Fatalf *inside* NewTopology before its own defer is set —
// that path is handled by NewTopology calling top.Close() before every
// t.Fatalf, and by TestTopologyLifecycle's per-round leak assertion. Do not
// check the gate box on this test alone.
func TestFailedTestLeavesNoNamespace(t *testing.T) {
	skipUnlessPrivileged(t)

	sub := func(t *testing.T) {
		top := NewTopology(t)
		defer top.Close()
		t.Fatalf("deliberate failure")
	}
	t.Run("deliberate", func(t *testing.T) {
		// Run the failing body but swallow its failure via a nested test.
		ok := t.Run("inner", sub)
		if ok {
			t.Fatal("expected the inner test to fail")
		}
	})

	if leaked := LeakedNamespaces(); len(leaked) > 0 {
		t.Fatalf("failed test leaked namespaces: %v", leaked)
	}
}

// clientCanReachInternet re-execs the test binary inside client-ns in
// probe-tcp mode: it dials the TCP-echo target in internet-ns end to end
// through the server relay. This is a stronger check than ICMP and needs no
// tool outside preflight's requiredTools set.
func clientCanReachInternet(ctx context.Context, top *Topology) error {
	c := nsName(nsClient, top.suffix)
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Second)
	}
	var lastErr error
	for time.Now().Before(deadline) {
		cmd := exec.Command("ip", "netns", "exec", c, os.Args[0], "-test.run=TestMainHelperNoop")
		cmd.Env = append(os.Environ(), "RED_MPUDP_HELPER=probe-tcp", "RED_MPUDP_HELPER_ADDR="+uplinkInternet+":7")
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = &probeError{string(out), err}
		time.Sleep(100 * time.Millisecond)
	}
	return lastErr
}

type probeError struct {
	out string
	err error
}

func (e *probeError) Error() string { return e.err.Error() + ": " + e.out }
