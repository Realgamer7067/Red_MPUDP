//go:build linux && integration

package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHelperOutputIsSafeToPollWhileRunning covers the collector these helpers
// depend on. startCapture and waitReadyLine both poll a child's output for a
// marker while that child is still writing, and os/exec performs those writes
// from its own copying goroutine. A plain bytes.Buffer there is a data race,
// which under -race aborts the run and without -race can return a torn string
// and miss the readiness marker the whole design rests on.
//
// This test needs no namespace and no capabilities, so it executes wherever
// the integration tag is built, unlike the rest of this package.
func TestHelperOutputIsSafeToPollWhileRunning(t *testing.T) {
	buf := &syncBuffer{}
	// Write continuously for longer than the polling window below, so reads and
	// writes genuinely overlap. A child that emits once and exits could finish
	// before the first poll, leaving nothing concurrent to detect.
	cmd := exec.Command("sh", "-c", `i=0; while [ $i -lt 4000 ]; do echo "line $i"; i=$((i+1)); done; echo "ready 127.0.0.1:1"`)
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start writer: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })

	deadline := time.Now().Add(5 * time.Second)
	saw := false
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "ready ") {
			saw = true
			break
		}
	}
	if !saw {
		t.Fatalf("readiness marker never observed:\n%s", buf.String())
	}
}
