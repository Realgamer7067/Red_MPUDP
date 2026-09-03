package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exec(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// CLI-11: secrets as flags are rejected before anything runs.
func TestSecretFlagsRejected(t *testing.T) {
	for _, a := range []string{"--psk=abc", "--private-key", "--key=xyz", "--server-secret", "--preshared-key=k"} {
		code, _, errs := exec("client", a)
		if code != exitUsage {
			t.Errorf("%s: exit = %d, want %d", a, code, exitUsage)
		}
		if !strings.Contains(errs, "file references") {
			t.Errorf("%s: unexpected stderr %q", a, errs)
		}
	}
}

// CLI-12: help output carries no sample secret value.
func TestHelpHasNoSecretValue(t *testing.T) {
	_, out, _ := exec("help")
	for _, line := range strings.Split(out, "\n") {
		for _, w := range strings.Fields(line) {
			if len(w) >= 40 && isBase64ish(w) {
				t.Fatalf("help line looks like it contains a secret: %q", line)
			}
		}
	}
}

func isBase64ish(s string) bool {
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=') {
			return false
		}
	}
	return true
}

// CLI-01, CLI-02, CLI-03: keygen and public-key round trip.
func TestKeygenAndPublicKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "client.key")

	code, out, errs := exec("keygen", keyPath)
	if code != exitOK {
		t.Fatalf("keygen exit = %d (%s)", code, errs)
	}
	if !strings.Contains(out, "public key:") {
		t.Fatalf("keygen output missing public key: %q", out)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %04o", info.Mode().Perm())
	}

	if code, _, _ := exec("keygen"); code != exitUsage { // CLI-02: explicit path
		t.Fatalf("keygen with no path: exit = %d", code)
	}
	if code, _, _ := exec("keygen", keyPath); code == exitOK { // refuse overwrite
		t.Fatal("keygen overwrote an existing file")
	}

	code, pub, errs := exec("public-key", keyPath)
	if code != exitOK {
		t.Fatalf("public-key exit = %d (%s)", code, errs)
	}
	if len(strings.TrimSpace(pub)) != 43 { // unpadded base64 of 32 bytes
		t.Fatalf("public key output = %q", pub)
	}
}

func TestPSK(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.psk")
	if code, _, errs := exec("psk", p); code != exitOK {
		t.Fatalf("psk exit = %d (%s)", code, errs)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("psk mode = %04o", info.Mode().Perm())
	}
}

const goodClientYAML = `server: "203.0.113.10:51820"
identity:
  private_key_file: "a"
  server_public_key_file: "b"
  psk_file: "c"
interfaces:
  - name: "wlan0"
    fwmark: 0x1
    routing_table: 201
    max_pacing_rate_mbps: 100
`

// CLI-05, CLI-06, CLI-07, CLI-10.
func TestCheckConfig(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "client.yaml")
	if err := os.WriteFile(good, []byte(goodClientYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := exec("check-config", "client", good); code != exitOK {
		t.Fatalf("check-config on valid file: exit=%d (%s)", code, errs)
	}

	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("server: \"not-an-addr\"\n"), 0o644)
	if code, _, _ := exec("check-config", "client", bad); code != exitConfig {
		t.Fatalf("check-config on invalid file: exit=%d, want %d", code, exitConfig)
	}
	if code, _, _ := exec("check-config", "client", filepath.Join(dir, "missing.yaml")); code != exitConfig {
		t.Fatalf("check-config on missing file: exit=%d", code)
	}
	if code, _, _ := exec("check-config", "bogus", good); code != exitUsage {
		t.Fatalf("check-config bad role: exit=%d", code)
	}
}

// JOURNAL-21, JOURNAL-22, JOURNAL-23: cleanup parses --state-file and refuses a
// missing or malformed journal.
func TestCleanupRefusesBadJournal(t *testing.T) {
	dir := t.TempDir()

	if code, _, _ := exec("cleanup"); code != exitUsage {
		t.Fatalf("cleanup with no flag: exit=%d", code)
	}
	if code, _, errs := exec("cleanup", "--state-file", filepath.Join(dir, "absent")); code != exitConfig {
		t.Fatalf("cleanup missing journal: exit=%d (%s)", code, errs)
	}
	bad := filepath.Join(dir, "bad.journal")
	os.WriteFile(bad, []byte("{ garbage"), 0o600)
	if code, _, _ := exec("cleanup", "--state-file", bad); code != exitConfig {
		t.Fatalf("cleanup malformed journal: exit=%d", code)
	}
	wrongSchema := filepath.Join(dir, "v9.journal")
	os.WriteFile(wrongSchema, []byte(`{"schema":9,"role":"client","instance_id":"red-mpudp-01"}`), 0o600)
	if code, _, _ := exec("cleanup", "--state-file", wrongSchema); code != exitConfig {
		t.Fatalf("cleanup wrong schema: exit=%d", code)
	}
}

// A well-formed journal with nothing to undo cleans up successfully.
func TestCleanupEmptyJournal(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.journal")
	os.WriteFile(p, []byte(`{"schema":1,"role":"client","instance_id":"red-mpudp-empty01","created_at":"2026-01-01T00:00:00Z"}`), 0o600)
	if code, out, errs := exec("cleanup", "--state-file", p); code != exitOK {
		t.Fatalf("cleanup empty journal: exit=%d out=%q err=%q", code, out, errs)
	}
}

// CLI-08, CLI-09: client/server parse config without starting the data plane.
func TestClientServerPrepare(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "client.yaml")
	os.WriteFile(good, []byte(goodClientYAML), 0o644)
	code, out, errs := exec("client", good)
	if code != exitOK {
		t.Fatalf("client prepare: exit=%d (%s)", code, errs)
	}
	if !strings.Contains(out, "later milestone") {
		t.Fatalf("unexpected client output: %q", out)
	}
}
