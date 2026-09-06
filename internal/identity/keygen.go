package identity

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

// GeneratePrivateKey returns a fresh random X25519 private scalar, clamped by
// the standard library. It fails closed if the system CSPRNG errors (ID-16).
func GeneratePrivateKey() ([KeyLen]byte, error) {
	var out [KeyLen]byte
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return out, fmt.Errorf("identity: generate X25519 key: %w", err)
	}
	copy(out[:], k.Bytes())
	return out, nil
}

// PublicKey derives the X25519 public point for priv.
func PublicKey(priv [KeyLen]byte) ([KeyLen]byte, error) {
	var out [KeyLen]byte
	k, err := ecdh.X25519().NewPrivateKey(priv[:])
	if err != nil {
		return out, errors.New("identity: invalid X25519 private key")
	}
	copy(out[:], k.PublicKey().Bytes())
	return out, nil
}

// GeneratePSK returns 32 fresh random bytes, failing closed on CSPRNG error
// (ID-15, ID-16).
func GeneratePSK() ([KeyLen]byte, error) {
	var out [KeyLen]byte
	if _, err := rand.Read(out[:]); err != nil {
		return out, fmt.Errorf("identity: generate PSK: %w", err)
	}
	return out, nil
}

// Encode returns the canonical file representation of a key: unpadded standard
// base64 followed by a single newline.
func Encode(key [KeyLen]byte) []byte {
	return []byte(keyEncoding.EncodeToString(key[:]) + "\n")
}

// WritePrivateKeyFile writes key to path with mode 0600 using exclusive
// creation. It refuses to overwrite an existing file (ID-17, ID-18).
func WritePrivateKeyFile(path string, key [KeyLen]byte) error {
	return writeExclusive(path, Encode(key), 0o600)
}

// WritePublicKeyFile writes key to path with mode 0644 using exclusive
// creation.
func WritePublicKeyFile(path string, key [KeyLen]byte) error {
	return writeExclusive(path, Encode(key), 0o644)
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("identity: %s already exists; refusing to overwrite key material", path)
		}
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}
