# 0001 — v1 transport

**Status:** Accepted (M02 Phase 0 gate signed off 2026-09-03)
**Date:** 2026-09-03
**Deciders:** malharrajpara28@gmail.com
**Supersedes / relates to:** design D2, D12; `open-decisions.md` D-P0-1..D-P0-4
**Design:** spec revision 4 — decision recorded; the flat "Jain ≥ 0.90" fairness
gate replaced by the normative **§9.5.1 fairness acceptance criterion**

## Context

The design already selects a custom UDP data plane secured by
`Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` (D2, since revision 3) and keeps QUIC
DATAGRAM (RFC 9221) only as an *alternate* that D12 permits reconsidering **only
if** it passes the Phase 0 benchmark. So M02 does not choose between two equals:
it **confirms D2** and decides whether the D12 alternate displaces it. It must
also validate the Phase 0 gate or stop and revise the relevant part of the
design.

All spike code, benchmarks, and raw output are committed:
`internal/noisehandshake/`, `internal/congestion/phase0sim/`,
`experiments/quicdatagram/`, `test/results/phase0/`.

## Evidence

### Comparison (SPIKE-61)

| Dimension | custom UDP + Noise (flynn/noise v1.1.0) | QUIC DATAGRAM (quic-go v0.62.0) |
|---|---|---|
| Handshake for our exact suite | `IKpsk2_25519_ChaChaPoly_BLAKE2s` — validated against cacophony reference vectors, byte-for-byte (SPIKE-20) | n/a (QUIC has its own handshake) |
| Private fork required | **No** — `HandshakeIK` + `PresharedKeyPlacement=2` + `CipherState.SetNonce` are all public (SPIKE-05) | **Yes / large app-layer** — see blockers below (SPIKE-59) |
| Explicit out-of-order transport nonces | `SetNonce(n)` per packet; every valid reorder opens once; auth failure never wedges the receiver (SPIKE-21..28) | packet numbers internal; DATAGRAM frames carry no app sequence |
| Max datagram size known **before** send (design §10) | we own the header math; PMTU probing is ours (M16) | **only via `DatagramTooLargeError` after a rejected send** — blocker |
| Per-copy delivery / loss feedback (design §8) | our `PATH_REPORT` carries it | **none exposed** — blocker for the scheduler |
| Send queue: packet+byte bounds, per-class priority, deadlines, tail-drop, expired-replica drop (design §9.3) | we build exactly this (M18) | **one fixed 32-frame blocking FIFO** — blocker |
| 4 independently keyed path copies | 4 `CipherState` pairs, ~cheap | 4 separate QUIC connections (4 handshakes, 4 CC instances) |
| Hot-path allocation | seal 1180 B: **1 alloc / 16 B**; seal+open: 2 / 32 B | `SendDatagram`: **10 allocs / ~4.5 KB** (7–12 µs/op); echo: 24 allocs / ~8.5 KB (94–117 µs/op) |
| Congestion control | ours to build (design §9.5) — see fairness result | mature (Cubic) but **internal**: no pacing hook, not observable enough for per-path replica budgets |
| Loopback echo RTT at a sustained 10,000 pps (best case, no wire delay) | n/a | p50 ~98 µs / p99 ~560 µs / **p99.9 ~1.3 ms** of pure stack overhead |

### Crypto packet-rate (SPIKE-63)

Target (derived from design §17.4's 10,000 pps, ×4 copies, ×2 ends, ×2
headroom): **≥ 160,000 AEAD ops/s single-core at 1180 B.**

Measured (i7-14650HX, Go 1.27, flynn/noise): **~940,000 seal+open/s/core**
(~1.98 M seal-only/s). **~6× headroom on one core**, before spreading across
path actors / cores. **PASS.** Raw: `test/results/phase0/crypto-bench.txt`.

### Congestion / fairness (SPIKE-62)

Deterministic fluid sim (`internal/congestion/phase0sim/`), the design §9.5
controller vs one-packet-per-RTT AIMD TCP on a shared drop-tail bottleneck,
swept across buffer depths 5–100 ms. Raw:
`test/results/phase0/congestion-sim.txt`.

Evaluated against the **§9.5.1 fairness acceptance criterion** (see below), whose
three clauses `TestFairnessCriterion` asserts and which **passes**:

- **Clause 1 — anti-flood floor:** native TCP keeps ≥ 35 % at every swept depth
  (measured 53 % on shallow buffers, rising to 93 % at 100 ms). RED_MPUDP never
  starves TCP.
- **Clause 2 — equality near the operating point:** Jain ≥ 0.90 for buffers ≤
  the 15 ms `queue_delay_target` (measured ≈ 0.996).
- **Clause 3 — graceful yield:** above the target, deeper buffer ⇒ controller
  throughput non-increasing (7.76 → 3.12 → 1.76 → 1.28 Mbit/s) and TCP share
  non-decreasing. The controller yields monotonically; it does not oscillate or
  sit at a high queue delay without reducing.
- Controller mechanics (additive increase = MTU/srtt, once-per-RTT halving,
  stale-feedback collapse, two-round delay cut) are all exact (SPIKE-42..45).

Jain is **not** ≥ 0.90 on bloated buffers (≈ 0.60 at 60 ms) — by design: a
latency-first flow should yield to bulk TCP there rather than match it and
inflate queue delay for everyone. The flat "Jain ≥ 0.90 at all depths" gate was
wrong for this design and is replaced by §9.5.1. M17 still narrows the gap (see
Consequences).

### QUIC blockers (SPIKE-59)

Three independent hard blockers, each forcing us to build, on top of QUIC, the
machinery we would build for custom UDP anyway — while paying QUIC's
per-datagram overhead and giving up the explicit packet-number model the
replay / dedup / scheduler design depends on:

1. no pre-send max-datagram-size query;
2. no per-datagram delivery / loss feedback;
3. uncontrollable 32-frame blocking send queue.

QUIC's one real advantage — mature congestion control — is not exposed enough
to drive design §9.5's per-path replica budgets, so adopting QUIC would replace
the whole replay / dedup / scheduler design for no offsetting gain: the §9.5.1
fairness criterion is already met by the custom controller (SPIKE-48).

## Decision (SPIKE-64)

**Adopt custom UDP + `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` (design D2) as the
v1 transport.** QUIC DATAGRAM stays deferred (design D12), evidence retained
under `experiments/quicdatagram/`.

## Gate status (design §6 / plan M02 gate)

| Gate | Result |
|---|---|
| Explicit nonces work safely out of order | **PASS** (SPIKE-21..28) |
| Independent path copies meet the packet-rate target | **PASS** — ~6× headroom (SPIKE-29..38) |
| Selected approach requires no private security-library fork | **PASS** — flynn/noise unforked (SPIKE-05, SPIKE-20) |
| Selected approach meets the fairness threshold | **PASS** — the §9.5.1 fairness acceptance criterion (all three clauses asserted by `TestFairnessCriterion`) |

**Sign-off decision (2026-09-03).** The flat "reject if Jain < 0.90 at any
depth" gate was wrong for a latency-first design and is **replaced**, not
waived, by the normative §9.5.1 criterion (spec revision 4). The Phase 0 sim
meets §9.5.1 in full. Separately, M17 still improves competitiveness on bloated
buffers — this is a scoped §9.5 addition with its own concrete release target,
not a deferred failure:

- **Design change (§9.5):** add a bounded loss-driven additive-increase path so
  the controller stays competitive with loss-based TCP on bloated buffers, at a
  bounded, measured latency cost. Keep the delay-gated increase as the primary
  mode.
- **M17 release target** (`plan:CC-FAIR-08`): with the loss-driven path enabled,
  **Jain ≥ 0.90 also at a 60 ms drop-tail buffer** on `tc netem`, while §9.5.1
  clauses 1 and 3 continue to hold. If M17 cannot meet that, §9.5 is revised
  again — the transport is **not** reopened (QUIC rejected on SPIKE-59 grounds).

This is within what the M02 gate allows ("stop implementation and revise the
design"): the revision is scoped to §9.5, does not touch the wire format or
security model, and does not block M03–M16.

## Design updates (SPIKE-65)

- **D2, D12: unchanged.** The selection matches the design.
- **§17.4 fairness gate: replaced** (spec revision 4). The flat "native TCP ≥
  35% and two-flow Jain ≥ 0.90 after warm-up" is now the **§9.5.1 fairness
  acceptance criterion**: (1) anti-flood floor ≥ 35% at every swept buffer
  depth, (2) Jain ≥ 0.90 at/below `queue_delay_target`, (3) monotonic yield
  above it. §17.4 and the Phase 0 exit criterion point to §9.5.1.
- **§9.5: `TODO(M17)` added** — the bounded loss-driven increase and its
  concrete `CC-FAIR` release target. The normative controller algorithm text is
  otherwise unchanged.

## Consequences

- M03 (shared foundations) may begin once this record is signed off.
- `experiments/quicdatagram/` is retained as evidence, excluded from the main
  module and CI by its own `go.mod` (SPIKE-66).
- `internal/congestion/phase0sim/` stays as the starting point for the M17
  controller.
- Follow-up (tracked, not blocking): bump `golang.org/x/crypto` / `x/sys` off
  the 2021 revision flynn/noise pins, then re-run `crypto-bench`.
- Open item D-P0-4 remains open, owned by M17.
