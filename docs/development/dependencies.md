# Dependencies (M02 Phase 0 review)

Direct dependencies are added only when a milestone first needs them. This file
records the review required by SPIKE-01..06 before that happens.

## In use now

### github.com/flynn/noise — Noise Protocol Framework (SPIKE-04, SPIKE-05, SPIKE-07)

| | |
|---|---|
| Version pinned | `v1.1.0` |
| Capability | `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` handshake + transport cipher states |
| License | 3-clause BSD ("Prime Directive, Inc.", `LICENSE`) — compatible |
| Transitive | `golang.org/x/crypto` (blake2s, chacha20poly1305, curve25519), `golang.org/x/sys` |

**Maintenance status:** low-frequency but alive. `v1.1.0` is the most recent
tag; the project is the Noise implementation the RED_MPUDP design references and
is used in production by Flynn and others. No open advisories.

**Status:** direct dependency as of M02; imported by the Phase 0 spike tests in
`internal/noisehandshake/`. `go.sum` is committed; CI keys its module cache on
it (`BOOT-21`).

**IKpsk2 + explicit nonce, no fork required (SPIKE-05):** verified against the
`v1.1.0` source:

- `noise.HandshakeIK` pattern (`patterns.go`) plus `Config.PresharedKey` and
  `Config.PresharedKeyPlacement = 2` produce `IKpsk2` (PSK token appended to the
  second message pattern — `state.go`).
- Cipher suite: `noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly,
  noise.HashBLAKE2s)` → `25519_ChaChaPoly_BLAKE2s`.
- `HandshakeState.WriteMessage` / `ReadMessage` return the two split
  `*CipherState` values; initiator sends with the first and receives with the
  second, responder mirrored — matches design §4.4.
- `(*CipherState).SetNonce(uint64)` sets `n` directly; the next `Encrypt` /
  `Decrypt` uses that `n` as the ChaCha20-Poly1305 nonce and then increments.
  This is exactly the "set the cipher-state nonce to the authenticated packet
  number immediately before each operation" model in design §4.4 / §6.2.
- `(*CipherState).Encrypt(out, ad, plaintext)` /
  `Decrypt(out, ad, ciphertext)` take explicit AAD — the 44-byte transport
  header is passed as `ad`.
- `MaxNonce = 2^64 - 2`; `ErrMaxNonce` past it. The design closes/rekeys well
  before this, so the ceiling is never reached in normal operation.

**Follow-up — DONE (M06).** The `golang.org/x/crypto` + `golang.org/x/sys`
bump landed as a side effect of adding `jsimonetti/rtnetlink` in M06:
`x/crypto v0.0.0-2021… → v0.50.0`, `x/sys v0.0.0-2020… → v0.43.0`,
`x/net → v0.52.0`. The Noise vector test (`internal/noisehandshake`,
`cacophony.txt`) was re-run against `x/crypto v0.50.0` and still passes
byte-for-byte.

### golang.org/x/sys — direct as of M05

`golang.org/x/sys/unix` is used by `test/integration` for the `Capget`
CAP_NET_ADMIN preflight check (HARNESS-03) and by `internal/tun` for
`/dev/net/tun` open + the `TUNSETIFF` ioctl (M06). BSD-3-Clause.
**Version:** `v0.43.0`.

### golang.org/x/crypto — transitive only

Pulled in by `flynn/noise` and the `mdlayher` netlink stack. Not imported
directly. BSD-3-Clause. **Version:** `v0.50.0`.

### github.com/jsimonetti/rtnetlink — structured netlink (M06)

| | |
|---|---|
| Version pinned | `v1.4.2` (with `github.com/mdlayher/netlink v1.7.2` base, indirect) |
| Capability | typed `RTM_SETLINK` (MTU, `IFF_UP`) and `RTM_NEWADDR`/`RTM_GETADDR` for `internal/tun` interface configuration and readback (TUN-11..14); the full route/rule/monitor surface is used from M08 |
| License | MIT (rtnetlink), MIT (mdlayher/netlink, mdlayher/socket) |
| Transitive | `github.com/josharian/native`, `golang.org/x/net`, `golang.org/x/sync`, `github.com/google/go-cmp` |

**Status:** direct dependency as of M06 (was planned for M08). Brought forward
because TUN-11 requires structured netlink values for address configuration and
hand-rolling `RTM_*` messages is error-prone. Re-confirmed against the `v1.4.2`
API: `rtnetlink.Dial(nil)` → `conn.Link.Set` / `conn.Link.Get` /
`conn.Address.New` / `conn.Address.List`.

### github.com/goccy/go-yaml — strict YAML config parsing (M04)

| | |
|---|---|
| Version pinned | `v1.19.2` |
| Capability | `yaml.DisallowUnknownField()` (CONF-06); duplicate map keys rejected by default unless `AllowDuplicateMapKey()` is passed, which it is not (CONF-07); `encoding.TextUnmarshaler` support so `net/netip` types parse directly (CONF-05) |
| License | MIT |
| Transitive | none beyond the standard library and `golang.org/x/*` already present |

**Status:** direct dependency as of M04; imported by `internal/config`. Re-confirmed
against the `v1.19.2` API: `yaml.NewDecoder(r, yaml.DisallowUnknownField())`, a
second `Decode` call is used to reject multi-document input.

## Selected but not yet imported (SPIKE-06)

Recorded now, added at the milestone shown. Versions are pinned when imported;
each choice is re-confirmed against its API at that point.

| Capability | Package | Milestone | Notes / alternative |
|---|---|---|---|
| nftables transactions (named table install / reconcile / remove) | `github.com/google/nftables` | M08, M20 | Netlink-based, supports atomic batches; no shelling out to `nft`. |
| systemd-resolved D-Bus client | `github.com/godbus/dbus/v5` | M20 | Standard Go D-Bus binding; call `org.freedesktop.resolve1` directly. |
| Prometheus text metrics + registry | `github.com/prometheus/client_golang` | M21 | Use a private `prometheus.NewRegistry()`, not the default global. |

No dependency is added for: TUN ioctl, `SO_BINDTODEVICE` / `SO_MARK` /
`IP_MTU_DISCOVER` / `IP_RECVERR` socket options, sysctl reads/writes, or the
mutation journal — all done with `golang.org/x/sys/unix` and the standard
library.

## Policy

- Every direct dependency is pinned to an exact version in `go.mod` and its
  hash recorded in `go.sum`.
- `make tidy` (`go mod tidy`) keeps `go.mod`/`go.sum` minimal; CI fails on a
  dirty `go mod tidy -diff` (added in M22, `plan:VERIFY-11`).
- License review is repeated for transitive dependencies at M22
  (`plan:VERIFY-11`); MIT / BSD / Apache-2.0 / ISC are accepted, copyleft is
  not.
- Security scanning via `govulncheck` in CI (M22, `plan:VERIFY-10`).
