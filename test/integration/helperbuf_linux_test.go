//go:build linux && integration

package integration

import (
	"os/exec"
	"strings"
	"sync"
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
// The proof is deterministic rather than probabilistic: the write that String
// must wait behind is pinned inside the critical section by a per-instance
// hook, so the test asserts the exclusion itself instead of hoping the
// scheduler interleaves two goroutines. It needs no namespace and no
// capabilities, so it executes wherever the integration tag is built, unlike
// the rest of this package.
func TestHelperOutputIsSafeToPollWhileRunning(t *testing.T) {
	entered := make(chan struct{}) // the exec copy-goroutine is inside Write
	release := make(chan struct{}) // ...and may now leave
	var once sync.Once             // fires the barrier on the first write only
	var releaseOnce sync.Once      // closing release is safe from both paths
	releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }

	buf := &syncBuffer{}
	buf.inWrite = func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}

	cmd := exec.Command("sh", "-c", `echo "ready 127.0.0.1:1"; echo tail`)
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start writer: %v", err)
	}
	waited := make(chan struct{})
	t.Cleanup(func() {
		// Never leave the child parked in Write, even on a failure path, or
		// cmd.Wait would block forever.
		once.Do(func() {})
		releaseWriter()
		<-waited
	})
	go func() {
		defer close(waited)
		_ = cmd.Wait()
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the exec copy-goroutine never reached syncBuffer.Write")
	}

	// String must not observe the buffer while that write is in progress.
	got := make(chan string, 1)
	go func() { got <- buf.String() }()
	select {
	case s := <-got:
		t.Fatalf("String returned %q while a write held the buffer; reads are not excluded", s)
	case <-time.After(200 * time.Millisecond):
	}

	releaseWriter()
	select {
	case s := <-got:
		if !strings.Contains(s, "ready ") {
			t.Fatalf("String returned %q after the write completed, want the readiness marker", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("String never returned after the write was released")
	}
}
