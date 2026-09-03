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

### D-P0-1 — v1 transport — RESOLVED (2026-09-03)
**Custom UDP + `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` (D2).** QUIC DATAGRAM
rejected on three independent blockers (no pre-send max-size, no per-datagram
delivery feedback, uncontrollable 32-frame blocking queue — the last
demonstrated in `TestSendQueueBlocksWhenFull`) plus ~7–15× per-datagram
overhead — `plan:SPIKE-59`. Decision record:
`docs/decisions/0001-v1-transport.md` (Accepted). D2/D12 unchanged; spec
revision 4 replaced the flat §17.4 Jain gate with the §9.5.1 criterion.
Evidence retained under `experiments/quicdatagram/`.

### D-P0-2 — Explicit-nonce out-of-order safety — RESOLVED (2026-09-03)
`github.com/flynn/noise v1.1.0` exposes `HandshakeIK` +
`PresharedKeyPlacement=2` (= IKpsk2) and `(*CipherState).SetNonce(uint64)` for
per-packet transport nonces, **no fork** (`plan:SPIKE-05`). Verified:
out-of-order nonces `{0,1,2,4097}` delivered `{2,0,4097,1}` each open exactly
once; every header byte is authenticated; an auth failure never wedges the
receiver; per-path cipher states are race-clean (`plan:SPIKE-21..28`). Also
cross-validated against cacophony reference vectors byte-for-byte
(`plan:SPIKE-20`).

### D-P0-3 — Independent-copy crypto packet-rate target — RESOLVED (spike done)
Target set: **≥ 160,000 AEAD ops/s single-core at 1180 B** (10k logical pps ×
4 copies × 2 ends × 2 headroom, from design §17.4). Measured on the dev host
(i7-14650HX, Go 1.27, flynn/noise v1.1.0): **~940k seal+open/s/core**, ~6×
headroom, before spreading across cores/path-actors. `plan:SPIKE-29..38`
complete; raw data + analysis in `test/results/phase0/`. The 4-path cap is
realistic in Go. Formal sign-off folds into the transport decision record
(D-P0-1). Allocation (1 obj/AEAD call in flynn/noise) is an M12 optimisation,
not a blocker.

### D-P0-4 — Congestion controller fairness — RESOLVED for M02; M17 target set
`plan:SPIKE-39..48` complete. Deterministic fluid sim
(`internal/congestion/phase0sim/`, data in `test/results/phase0/`).

**The flat "Jain ≥ 0.90 at every depth" gate was wrong for a latency-first
design and is replaced** (spec revision 4) by the normative **§9.5.1 fairness
acceptance criterion**: (1) native TCP ≥ 35 % at every swept buffer depth,
(2) Jain ≥ 0.90 at/below `queue_delay_target`, (3) above the target, deeper
buffer ⇒ controller throughput non-increasing AND TCP share non-decreasing
(monotonic yield). `TestFairnessCriterion` asserts all three; the Phase 0 sim
**meets** it (min TCP share 53 %; Jain ≈ 0.996 at ≤ 15 ms; throughput
7.76 → 3.12 → 1.76 → 1.28 Mbit/s as the buffer grows). SPIKE-42..45 (additive
increase, once-per-RTT halving, stale-feedback collapse, 2-round delay cut) all
exact.

Jain is ≈ 0.60 on a 60 ms bloated buffer — deliberate yielding, not
misbehaviour. **M17 target** (`plan:CC-FAIR-08`): add a bounded loss-driven
additive-increase path (delay-gated increase stays primary) so that, with it
enabled, **Jain ≥ 0.90 also at a 60 ms drop-tail buffer** on `tc netem` while
§9.5.1 clauses 1 and 3 still hold. If M17 misses that, §9.5 is revised again —
the transport is not reopened. **Open, owned by M17.**

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
