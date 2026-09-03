# Phase 0 crypto throughput — analysis (SPIKE-29..38)

Raw data: `crypto-bench.txt` (6 samples/benchmark, Intel i7-14650HX, Go 1.27,
`flynn/noise v1.1.0`, suite `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s`).

## Results (single goroutine, median of 6)

| Benchmark | ns/op | ops/s/core | allocs/op | B/op |
|---|---:|---:|---:|---:|
| seal+open 64 B   |  ~300 | ~3.3 M | 2 | 32 |
| seal+open 256 B  |  ~400 | ~2.5 M | 2 | 32 |
| seal+open 768 B  |  ~750 | ~1.33 M | 2 | 32 |
| seal+open 1180 B | ~1060 | ~940 k | 2 | 32 |
| seal ×1 (1180 B) |  ~505 | ~1.98 M | 1 | 16 |
| seal ×2 (1180 B) | ~1010 | ~990 k logical pkt/s | 2 | 32 |
| seal ×4 (1180 B) | ~2020 | ~495 k logical pkt/s | 1/copy | 16/copy |

"seal ×N" is one 1180-byte inner packet sealed into N independent path copies,
each under its own key and nonce (design §6.2 forbids reusing a sealed body).
Cost scales linearly with N — no shared setup to amortise, as expected.

## Packet-rate gate

Proposed target (derived from design §17.4's 10,000 packets/s end-to-end
latency test, ×4 paths, both crypto directions, ×2 safety headroom):

> **sustain ≥ 160,000 AEAD operations/s on a single core** for 1180-byte
> payloads (10k logical pps × 4 copies × 2 ends × 2 headroom).

Measured single-core capacity at 1180 B: **~940,000 seal+open/s**
(~1.98 M seal-only/s). That is **~6×** the headroom target on one core, before
using any of the other 23, and each path actor runs on its own goroutine so
real load spreads further.

**SPIKE gate (crypto packet-rate): PASS.** Crypto is not the bottleneck for the
v1 latency workload. Even a 100,000 logical-pps stress (10× the design target)
with 4-copy replication is ~0.8 core-seconds/s of AEAD — comfortably absorbed.

## Allocation note (for M12)

`flynn/noise` allocates 1 object (~16 B) per `Encrypt`/`Decrypt` — an internal
per-call nonce slice, not the ciphertext buffer (that reuses caller capacity).
At the rates above this is minor, but the M12 path actor should wrap the raw
`cipher.AEAD` directly (design allows: `CipherState.Cipher()` exposes it) to
reach 0 alloc/packet on the hot path. Not a Phase 0 blocker.

## Caveat

`flynn/noise v1.1.0` pins `golang.org/x/crypto` at a 2021 revision. The
ChaCha20-Poly1305 implementation there is already the optimised assembly path
on amd64; bumping x/crypto (planned post-decision) is expected to match or
improve these numbers, not regress them. Re-run this benchmark after the bump.
