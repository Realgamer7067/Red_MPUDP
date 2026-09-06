// Package identity handles RED_MPUDP key material: parsing and permission
// checking of on-disk key files, generation of new keys, and the HKDF key
// schedule that turns a configured PSK into the two domain-separated keys the
// protocol uses (design §4.3, §4.4).
//
// Error values from this package never contain key bytes (ID-12): a corrupt or
// wrong-length key file is reported by shape, not content.
package identity

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// KeyLen is the length of every RED_MPUDP key: X25519 scalars, X25519 public
// points, and the PSK are all 32 bytes.
const KeyLen = 32

// keyEncoding is unpadded RFC 4648 standard base64 (design §4.3).
var keyEncoding = base64.StdEncoding.WithPadding(base64.NoPadding)

var (
	errEmpty      = errors.New("identity: key file is empty")
	errWhitespace = errors.New("identity: key file has leading or trailing whitespace")
	errPadded     = errors.New("identity: key value uses padded base64; expected unpadded RFC 4648 standard base64")
	errNotBase64  = errors.New("identity: key value is not valid unpadded RFC 4648 standard base64")
	errInteriorNL = errors.New("identity: key file has an interior newline; expected one line and an optional trailing newline")
	errInteriorWS = errors.New("identity: key file has interior whitespace")
)

// ParseKey decodes the canonical key encoding from raw file bytes: one unpadded
// standard-base64 value decoding to exactly 32 bytes, with at most one trailing
// newline and no other whitespace.
func ParseKey(raw []byte) ([KeyLen]byte, error) {
	var out [KeyLen]byte

	if len(raw) == 0 {
		return out, errEmpty
	}

	// Exactly one optional trailing '\n' (with an optional preceding '\r').
	body := raw
	if body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
		if len(body) > 0 && body[len(body)-1] == '\r' {
			body = body[:len(body)-1]
		}
	}
	if len(body) == 0 {
		return out, errEmpty
	}
	for _, b := range body {
		if b == '\n' {
			return out, errInteriorNL
		}
	}
	if isSpace(body[0]) || isSpace(body[len(body)-1]) {
		return out, errWhitespace
	}
	if containsSpace(body) {
		return out, errInteriorWS
	}
	if hasBase64Padding(body) {
		return out, errPadded
	}

	dec, err := keyEncoding.DecodeString(string(body))
	if err != nil {
		return out, errNotBase64
	}
	if len(dec) != KeyLen {
		return out, fmt.Errorf("identity: key decodes to %d bytes, want %d", len(dec), KeyLen)
	}
	copy(out[:], dec)
	return out, nil
}

// LoadPrivateKeyFile reads a private key or PSK file, rejecting it if the file
// is group- or world-accessible in any mode bit (ID-07..10).
func LoadPrivateKeyFile(path string) ([KeyLen]byte, error) {
	var zero [KeyLen]byte
	info, err := os.Stat(path)
	if err != nil {
		return zero, err
	}
	if err := requirePrivatePerms(path, info.Mode()); err != nil {
		return zero, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, err
	}
	return ParseKey(data)
}

// LoadPublicKeyFile reads a public-key file. Public keys may be world-readable
// (ID-11), so no permission check is applied.
func LoadPublicKeyFile(path string) ([KeyLen]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return [KeyLen]byte{}, err
	}
	return ParseKey(data)
}

// requirePrivatePerms rejects any group or other permission bit.
func requirePrivatePerms(path string, mode fs.FileMode) error {
	if mode&0o077 != 0 {
		return fmt.Errorf("identity: %s has mode %04o; private key material must not be group- or world-accessible (want 0600)", path, mode.Perm())
	}
	return nil
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\v' || b == '\f'
}

func containsSpace(b []byte) bool {
	for _, c := range b {
		if isSpace(c) {
			return true
		}
	}
	return false
}

func hasBase64Padding(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] == '='
}
