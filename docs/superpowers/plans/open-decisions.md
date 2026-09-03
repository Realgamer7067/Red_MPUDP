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

### D-P0-3 — Independent-copy crypto packet-rate target — RESOLVED (spike done)
Target set: **≥ 160,000 AEAD ops/s single-core at 1180 B** (10k logical pps ×
4 copies × 2 ends × 2 headroom, from design §17.4). Measured on the dev host
(i7-14650HX, Go 1.27, flynn/noise v1.1.0): **~940k seal+open/s/core**, ~6×
headroom, before spreading across cores/path-actors. `plan:SPIKE-29..38`
complete; raw data + analysis in `test/results/phase0/`. The 4-path cap is
realistic in Go. Formal sign-off folds into the transport decision record
(D-P0-1). Allocation (1 obj/AEAD call in flynn/noise) is an M12 optimisation,
not a blocker.

### D-P0-4 — Congestion controller fairness — SPIKE DONE, result nuanced
`plan:SPIKE-39..48` complete. Deterministic fluid sim
(`internal/congestion/phase0sim/`, data in `test/results/phase0/`):
- **Safety gate PASS at every buffer depth** — a greedy RED_MPUDP flow never
  drops native TCP below 35 % (TCP keeps 53 % on shallow buffers, more on deep
  ones).
- **Equality gate (Jain ≥ 0.90) PASS only for drop-tail buffers ≲ 20 ms**
  (≈ the 15 ms queue-delay target). On deeper/bloated buffers the delay-gated
  additive increase stops firing and the controller *yields* to loss-based TCP
  (Jain ≈ 0.60 at 60 ms buffer) — it self-limits, it does not misbehave.
- SPIKE-42..45 (additive increase, once-per-RTT halving, stale-feedback
  collapse, 2-round delay cut) all pass exactly.

**Carried to D-P0-1 / the transport decision (`plan:SPIKE-60..66`)** with three
options: (1) accept yielding as correct for a latency VPN and reword the §17.4
Jain gate, (2) add a bounded loss-driven increase path (design change), or
(3) reopen toward QUIC DATAGRAM. This makes the **QUIC spike load-bearing.**
Must be re-checked against real `tc netem` at `plan:CC-FAIR-*` (M17).

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
