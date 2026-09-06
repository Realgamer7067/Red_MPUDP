package main

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TUN-31: the release binary carries no plaintext-forwarding switch or
// subcommand. Plaintext forwarding lives only in test/integration behind the
// `integration` build tag.
func TestNoPlaintextForwardingSubcommand(t *testing.T) {
	for _, sub := range []string{"forward", "plaintext", "cleartext", "tun-forward"} {
		code, _, _ := exec(sub)
		if code != exitUsage {
			t.Fatalf("subcommand %q is handled (exit %d); it must not exist", sub, code)
		}
	}
	_, help, _ := exec("help")
	low := strings.ToLower(help)
	for _, w := range []string{"forward", "plaintext", "cleartext"} {
		if strings.Contains(low, w) {
			t.Fatalf("help text mentions %q", w)
		}
	}
}

// A default-tags build of the CLI contains no plaintext-forwarding symbols.
func TestReleaseBinaryHasNoForwardingSymbols(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary; skipped under -short")
	}
	bin := filepath.Join(t.TempDir(), "red-mpudp")
	if out, err := osexec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"plaintextForwarder", "forwardPlaintext", "tun-plaintext", "tunPlaintextCheck"} {
		if strings.Contains(string(data), marker) {
			t.Fatalf("release binary contains plaintext-forwarding symbol %q", marker)
		}
	}
}
