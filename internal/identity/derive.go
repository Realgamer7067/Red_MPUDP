package identity

import (
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
)

// Protocol domain-separation strings (design §4.4). Changing any of these is a
// wire-breaking change and requires a spec revision bump.
const (
	keyScheduleSalt = "RED_MPUDP/v1/key-schedule"
	preauthInfo     = "RED_MPUDP/v1/preauth"
	noisePSKInfo    = "RED_MPUDP/v1/noise-psk"
)

// PeerID is the 8-byte plaintext lookup hint carried in the handshake. It is
// the first eight bytes of SHA-256 over the client's 32-byte public key, in
// network byte order (design §4.3).
type PeerID [8]byte

// DerivePeerID computes the PeerID for a client public key.
func DerivePeerID(clientPub [KeyLen]byte) PeerID {
	sum := sha256.Sum256(clientPub[:])
	var id PeerID
	copy(id[:], sum[:8])
	return id
}

// Schedule holds the two domain-separated keys derived from a configured PSK.
type Schedule struct {
	// PreauthKey authenticates the handshake envelope with HMAC-SHA-256
	// truncated to 16 bytes. It is a cheap pre-DH filter, not a replacement for
	// Noise authentication.
	PreauthKey [KeyLen]byte
	// NoisePSK is the value handed to Noise at PSK placement 2.
	NoisePSK [KeyLen]byte
}

// DeriveSchedule runs the HKDF-SHA-256 key schedule (design §4.4):
//
//	salt        = SHA-256("RED_MPUDP/v1/key-schedule")
//	prk         = HKDF-Extract(salt, psk)
//	preauth_key = HKDF-Expand(prk, "RED_MPUDP/v1/preauth", 32)
//	noise_psk   = HKDF-Expand(prk, "RED_MPUDP/v1/noise-psk", 32)
func DeriveSchedule(psk [KeyLen]byte) (Schedule, error) {
	var s Schedule
	saltSum := sha256.Sum256([]byte(keyScheduleSalt))

	prk, err := hkdf.Extract(sha256.New, psk[:], saltSum[:])
	if err != nil {
		return s, err
	}
	preauth, err := hkdf.Expand(sha256.New, prk, preauthInfo, KeyLen)
	if err != nil {
		return s, err
	}
	noisePSK, err := hkdf.Expand(sha256.New, prk, noisePSKInfo, KeyLen)
	if err != nil {
		return s, err
	}
	copy(s.PreauthKey[:], preauth)
	copy(s.NoisePSK[:], noisePSK)

	// ID-23: the two derived keys must differ.
	if subtle.ConstantTimeCompare(s.PreauthKey[:], s.NoisePSK[:]) == 1 {
		return Schedule{}, errors.New("identity: HKDF produced identical preauth and noise keys")
	}
	return s, nil
}
