package identity_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/identity"
)

type goldenFile struct {
	PeerID struct {
		ClientPublicKeyHex string `json:"client_public_key_hex"`
		PeerIDHex          string `json:"peer_id_hex"`
	} `json:"peer_id"`
	KeySchedule struct {
		PSKHex        string `json:"psk_hex"`
		PreauthKeyHex string `json:"preauth_key_hex"`
		NoisePSKHex   string `json:"noise_psk_hex"`
	} `json:"key_schedule"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	data, err := os.ReadFile("testdata/golden/key-schedule.json")
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func mustHex32(t *testing.T, s string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		t.Fatalf("bad 32-byte hex %q: %v", s, err)
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

// ID-19, ID-24: peer_id golden vector.
func TestDerivePeerIDGolden(t *testing.T) {
	g := loadGolden(t)
	pub := mustHex32(t, g.PeerID.ClientPublicKeyHex)
	id := identity.DerivePeerID(pub)
	if got := hex.EncodeToString(id[:]); got != g.PeerID.PeerIDHex {
		t.Fatalf("peer_id = %s, want %s", got, g.PeerID.PeerIDHex)
	}
	// Deterministic.
	if identity.DerivePeerID(pub) != id {
		t.Fatal("DerivePeerID not deterministic")
	}
}

// ID-20..24: HKDF key-schedule golden vectors, and the two keys differ.
func TestDeriveScheduleGolden(t *testing.T) {
	g := loadGolden(t)
	psk := mustHex32(t, g.KeySchedule.PSKHex)

	s, err := identity.DeriveSchedule(psk)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(s.PreauthKey[:]); got != g.KeySchedule.PreauthKeyHex {
		t.Fatalf("preauth_key = %s, want %s", got, g.KeySchedule.PreauthKeyHex)
	}
	if got := hex.EncodeToString(s.NoisePSK[:]); got != g.KeySchedule.NoisePSKHex {
		t.Fatalf("noise_psk = %s, want %s", got, g.KeySchedule.NoisePSKHex)
	}
	if s.PreauthKey == s.NoisePSK {
		t.Fatal("preauth and noise keys are identical")
	}

	// Deterministic across calls.
	s2, _ := identity.DeriveSchedule(psk)
	if s2 != s {
		t.Fatal("DeriveSchedule not deterministic")
	}

	// Distinct PSKs give distinct schedules.
	var other [32]byte
	other[0] = 1
	so, _ := identity.DeriveSchedule(other)
	if so.NoisePSK == s.NoisePSK || so.PreauthKey == s.PreauthKey {
		t.Fatal("different PSK produced an overlapping schedule")
	}
}
