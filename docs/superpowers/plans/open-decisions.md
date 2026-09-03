# RED_MPUDP v1 open decisions

Tracks design questions that affect the wire format, security model, MTU
arithmetic, or Linux routing behaviour. Each is either **resolved** (recorded
here, and the design updated if needed) or **assigned to the M02 Phase 0
stop/go decision**. The M00 gate requires every such question to be in one of
those two states.

---

## Resolved at M00

### D-M00-1 — Project name and wire magic (LOCK-03)
Renamed RED_MCUDP → RED_MPUDP. The rename covers prose, the binary name
(`red-mpudp`), the Noise prologue (`RED_MPUDP/v1`), the HKDF label strings, the
config/runtime paths, and the **4-byte wire magic** (`RMCU` → `RMPU`, design
revision 3, commit `904bd12`). Module path: `github.com/Realgamer7067/Red_MPUDP`.
No golden vectors existed yet, so this costs nothing downstream.

### D-M00-2 — Production TUN MTU start value (LOCK-11)
Production starts at the design's **fixed initial TUN MTU of 1180** and does
*not* probe toward a larger preferred MTU before committing the session value.
The first path confirms the outer size required by 1180 (1268 B) in both
directions; only then is the tunnel default route installed (design §10,
`plan:PMTU-56`). Upward probing toward the configured maximum (≤ 1400) happens
*after* the session is up and is rate-limited. This matches the source design —
**no design change required** (LOCK-12).

### D-M00-3 — Canonical config locations (LOCK-14)
- Client: `/etc/red-mpudp/client.yaml`, key material under `/etc/red-mpudp/`.
- Server: `/etc/red-mpudp/server.yaml`, peer material under
  `/etc/red-mpudp/peers/`.
- Runtime state / mutation journal: `/run/red-mpudp/` (mode 0600).
- systemd `RuntimeDirectory=red-mpudp`.
Already reflected in the design §11.5 and §14; example files land at
`configs/{client,server}.example.yaml` in M21 (`plan:DOC-01`, `plan:DOC-02`).

### D-M00-4 — Release binary shape (LOCK-15)
**One binary, `red-mpudp`, with subcommands** (`version`, `keygen`,
`public-key`, `psk`, `check-config`, `client`, `server`, `cleanup`). No split
client/server binaries in v1.

### D-M00-5 — v1 non-goals confirmed (LOCK-16)
Out of scope for v1, unchanged from design §1.3 / §19:
- IPv6 **inner** tunnelling (IPv6 on the client is block-by-default with an
  explicit, leak-flagged `passthrough`).
- Multiple exit servers / topology-aware failure-domain selection.
- Traffic-shape obfuscation / censorship circumvention / fake-TLS framing.
- Throughput bonding / bandwidth aggregation / coupled multipath congestion
  control.

### D-M00-6 — Minimum kernel and distro matrix (LOCK-13)
Minimum kernel **5.15**; release matrix in
`docs/development/kernel-support.md`. Ubuntu 22.04 (5.15) is the gating image.

---

## Assigned to the M02 Phase 0 stop/go decision

### D-P0-1 — v1 transport: custom UDP + Noise vs QUIC DATAGRAM (design D2 vs D12)
The design specifies `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` over plain UDP
(D2) and keeps QUIC DATAGRAM (RFC 9221) as an alternate that must first pass
the Phase 0 benchmark (D12). `plan:SPIKE-49`..`plan:SPIKE-66` produce
`docs/decisions/0001-v1-transport.md`. If the selection changes D2 or D12 the
design is updated (`plan:SPIKE-65`).

### D-P0-2 — Explicit-nonce out-of-order safety with the chosen Noise library
Whether the selected Go Noise package exposes IKpsk2 **and** explicit
transport-nonce control (`SetNonce` per packet) without a private fork
(`plan:SPIKE-05`, `plan:SPIKE-21`..`plan:SPIKE-28`). A failure here reopens
D-P0-1.

### D-P0-3 — Independent-copy crypto packet-rate target
Whether sealing/opening 1, 2, and 4 independent path copies at representative
sizes meets the packet-rate target on real hardware
(`plan:SPIKE-29`..`plan:SPIKE-38`). Sets whether the 4-path cap is realistic in
Go or needs batching / a lower cap.

### D-P0-4 — Congestion controller fairness threshold
Whether the per-direction AIMD controller lets a native TCP flow keep ≥ 35 %
of a shared bottleneck with Jain fairness ≥ 0.90
(`plan:SPIKE-39`..`plan:SPIKE-48`, re-checked `plan:CC-FAIR-*`). Failure
reopens the transport decision in favour of an established congestion-controlled
datagram transport.

### D-P0-5 — Frozen default values
Queue sizes, replica budgets, pacing rates, probe/report intervals, and the
dedup-window default are **starting points** in the design (§9.3 note). Their
released values are frozen from measured evidence at `plan:RELEASE-04`, with the
measuring environment recorded (`plan:RELEASE-05`). Not a blocker for M01–M21.

---

## Notes

- No item above blocks M00 or M01.
- D-P0-1 blocks M03 and everything after it: implementation of the data plane
  must not begin until the transport is selected.
