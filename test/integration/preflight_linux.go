//go:build linux && integration

package integration

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

// requiredTools are the external programs the harness shells out to
// (HARNESS-04).
var requiredTools = []string{"ip", "tc", "nft"}

// preflight returns "" when the environment can run privileged harness tests,
// or a single precise reason to skip (HARNESS-02..05).
func preflight() string {
	if _, err := exec.LookPath("ip"); err != nil {
		return "the 'ip' tool is not on PATH"
	}
	if !hasNetAdmin() {
		return "effective CAP_NET_ADMIN is required (run as root or with CAP_NET_ADMIN)"
	}
	for _, tool := range requiredTools {
		if _, err := exec.LookPath(tool); err != nil {
			return "required tool " + tool + " is not on PATH"
		}
	}
	return ""
}

func hasNetAdmin() bool {
	var hdr unix.CapUserHeader
	hdr.Version = unix.LINUX_CAPABILITY_VERSION_3
	hdr.Pid = 0 // self
	var data [2]unix.CapUserData
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return false
	}
	const capNetAdmin = unix.CAP_NET_ADMIN // 12
	return data[capNetAdmin>>5].Effective&(1<<(uint(capNetAdmin)&31)) != 0
}

// skipUnlessPrivileged is called at the top of every privileged test and by
// NewTopology.
func skipUnlessPrivileged(t testing.TB) {
	t.Helper()
	if reason := preflight(); reason != "" {
		t.Skipf("skipping privileged integration test: %s", reason)
	}
}
