//go:build linux && integration

package integration

import "testing"

// TestPreflight documents the environment. It never fails: it reports whether
// the privileged suite can run (HARNESS-01, HARNESS-05).
func TestPreflight(t *testing.T) {
	if reason := preflight(); reason != "" {
		t.Skipf("privileged integration suite unavailable: %s", reason)
	}
	t.Log("privileged integration prerequisites satisfied: Linux, CAP_NET_ADMIN, ip/tc/nft")
}
