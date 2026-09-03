# Noise test vectors

## cacophony.txt

Published test vectors from **cacophony**, the Haskell Noise implementation the
Noise Protocol Framework authors maintain as a reference.

- Source: `https://raw.githubusercontent.com/centromere/cacophony/master/vectors/cacophony.txt`
- SHA-256: see `SHA256SUMS`
- 944 vectors covering every standard pattern (and PSK / deferred variants) over
  Curve25519 and Curve448 with AES-GCM / ChaCha20-Poly1305 and
  SHA-256 / SHA-512 / BLAKE2b / BLAKE2s.

Vendored (not fetched at test time) so `go test` is hermetic and offline.
`./fetch.sh` re-downloads and verifies the file against `SHA256SUMS`.

## How it is used

`internal/noisehandshake/vectors_test.go` (SPIKE-20) drives
`github.com/flynn/noise` through an allowlist of these vectors — including the
exact RED_MPUDP v1 suite `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` — and asserts
that every handshake and transport message ciphertext, and the handshake hash,
match cacophony byte for byte. This cross-validates flynn/noise against an
independent implementation.

## Refreshing

```
./fetch.sh                 # fails if the upstream file no longer matches SHA256SUMS
```

If upstream legitimately changed, review the diff, then regenerate:

```
sha256sum cacophony.txt > SHA256SUMS
```

and re-run `go test ./internal/noisehandshake/`.
