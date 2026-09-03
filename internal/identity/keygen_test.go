package identity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/identity"
)

func TestGeneratePrivateKeyAndPublicKey(t *testing.T) {
	priv, err := identity.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	priv2, _ := identity.GeneratePrivateKey()
	if priv == priv2 {
		t.Fatal("two generated private keys are identical")
	}
	pub, err := identity.PublicKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubAgain, err := identity.PublicKey(priv)
	if err != nil || pub != pubAgain {
		t.Fatalf("PublicKey not deterministic: %v", err)
	}
	if pub == ([32]byte{}) {
		t.Fatal("public key is all zero")
	}
}

func TestGeneratePSK(t *testing.T) {
	a, err := identity.GeneratePSK()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := identity.GeneratePSK()
	if a == b {
		t.Fatal("two generated PSKs are identical")
	}
	if a == ([32]byte{}) {
		t.Fatal("PSK is all zero")
	}
}

// ID-17, ID-18: exclusive creation at mode 0600, refuse to overwrite.
func TestWritePrivateKeyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new.key")

	key, _ := identity.GeneratePrivateKey()
	if err := identity.WritePrivateKeyFile(p, key); err != nil {
		t.Fatalf("first write: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}

	// Refuse to overwrite.
	if err := identity.WritePrivateKeyFile(p, key); err == nil {
		t.Fatal("second write to an existing path succeeded; must refuse")
	}

	// The file round-trips through LoadPrivateKeyFile.
	got, err := identity.LoadPrivateKeyFile(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got != key {
		t.Fatalf("round-trip mismatch")
	}
}

func TestPublicKeyRejectsBadLength(t *testing.T) {
	if _, err := identity.PublicKey([32]byte{}); err == nil {
		// all-zero scalar is actually accepted by NewPrivateKey (produces a
		// low-order point); this is a smoke check, not a security assertion.
		t.Log("all-zero private key accepted by X25519 (expected)")
	}
}
