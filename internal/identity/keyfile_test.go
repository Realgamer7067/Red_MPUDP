package identity_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/identity"
)

var sampleKey = [32]byte{
	0x9d, 0x1a, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd,
	0xee, 0xff, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e,
}

func sampleUnpadded() string {
	return base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(sampleKey[:])
}

func TestParseKey(t *testing.T) {
	good := sampleUnpadded()
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"bare value", good, false},
		{"one trailing newline", good + "\n", false},
		{"trailing CRLF", good + "\r\n", false},
		{"leading space", " " + good, true},
		{"trailing space", good + " ", true},
		{"interior newline", good[:10] + "\n" + good[10:], true},
		{"two lines", good + "\n" + good, true},
		{"padded base64", base64.StdEncoding.EncodeToString(sampleKey[:]), true},
		{"not base64", "!!!!not base64!!!!", true},
		{"too short", base64.StdEncoding.WithPadding(base64.NoPadding).EncodeToString(sampleKey[:16]), true},
		{"empty", "", true},
		{"only newline", "\n", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := identity.ParseKey([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got key %x", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != sampleKey {
				t.Fatalf("round-trip mismatch: got %x want %x", got, sampleKey)
			}
		})
	}
}

// ID-12: an error from a wrong-length or corrupt key must not embed key bytes.
func TestParseKeyErrorsNeverContainKeyBytes(t *testing.T) {
	secret := strings.Repeat("A", 43) // 43 base64 chars ~ 32 bytes but we mangle it
	inputs := []string{
		secret + "extra", // too long
		"YWJj",           // decodes to 3 bytes
		base64.StdEncoding.EncodeToString(sampleKey[:]), // padded
	}
	for _, in := range inputs {
		_, err := identity.ParseKey([]byte(in))
		if err == nil {
			t.Fatalf("input %q: expected error", in)
		}
		if strings.Contains(err.Error(), in) || strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks input bytes: %q", err.Error())
		}
	}
}

func TestLoadPrivateKeyFilePermissions(t *testing.T) {
	dir := t.TempDir()
	content := []byte(sampleUnpadded() + "\n")

	cases := []struct {
		mode    os.FileMode
		wantErr bool
	}{
		{0o600, false},
		{0o400, false},
		{0o640, true}, // group-readable
		{0o604, true}, // world-readable
		{0o660, true},
		{0o644, true},
	}
	for _, tc := range cases {
		p := filepath.Join(dir, "k")
		_ = os.Remove(p)
		if err := os.WriteFile(p, content, tc.mode); err != nil {
			t.Fatal(err)
		}
		// WriteFile is subject to umask; force the mode.
		if err := os.Chmod(p, tc.mode); err != nil {
			t.Fatal(err)
		}
		_, err := identity.LoadPrivateKeyFile(p)
		if tc.wantErr && err == nil {
			t.Errorf("mode %04o: expected rejection", tc.mode)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("mode %04o: unexpected error %v", tc.mode, err)
		}
	}
}

// ID-11: public-key files may be world-readable.
func TestLoadPublicKeyFileAllowsWorldReadable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pub")
	if err := os.WriteFile(p, []byte(sampleUnpadded()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := identity.LoadPublicKeyFile(p)
	if err != nil {
		t.Fatalf("world-readable public key rejected: %v", err)
	}
	if got != sampleKey {
		t.Fatalf("mismatch: %x", got)
	}
}
