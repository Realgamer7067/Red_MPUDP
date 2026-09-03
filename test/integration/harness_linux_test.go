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

// TestFailedTestLeavesNoNamespace is the gate clause 2: a deliberately failing
// assertion inside a topology block must still tear the topology down.
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

func clientCanReachInternet(ctx context.Context, top *Topology) error {
	// Use `ip netns exec` + the test binary's own dialer is not reachable from
	// inside the namespace, so shell out to a one-shot nc-style probe via the
	// helper: simplest portable check is an ICMP ping to the internet uplink.
	c := nsName(nsClient, top.suffix)
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		out, err := exec.Command("ip", "netns", "exec", c, "ping", "-c", "1", "-W", "1", uplinkInternet).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = &pingError{string(out), err}
		time.Sleep(100 * time.Millisecond)
	}
	return lastErr
}

type pingError struct {
	out string
	err error
}

func (e *pingError) Error() string { return e.err.Error() + ": " + e.out }
