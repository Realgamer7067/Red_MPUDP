//go:build linux && integration

package integration

import "testing"

// TestTUNInNamespaces covers TUN-26..29: create red0 inside client-ns and
// server-ns, confirm each starts at MTU 1180, and move one cleartext IPv4
// packet across the real device to an in-memory peer. The forwarding code is
// behind the `integration` build tag (TUN-30).
func TestTUNInNamespaces(t *testing.T) {
	skipUnlessPrivileged(t)

	top := NewTopology(t)
	defer top.Close()

	for _, ns := range []nsKey{keyClient, keyServer} {
		if out, err := helperCmd(nsFull(ns), "tun-plaintext", "").CombinedOutput(); err != nil {
			t.Fatalf("%s: tun plaintext check failed: %v\n%s", ns, err, out)
		}
	}
}
