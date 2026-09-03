# 0001 — v1 transport

**Status:** Accepted (M02 Phase 0 gate signed off 2026-09-03)
**Date:** 2026-09-03
**Deciders:** malharrajpara28@gmail.com
**Supersedes / relates to:** design D2, D12; `open-decisions.md` D-P0-1..D-P0-4
**Design:** recorded in spec revision 4; §9.5 carries the TODO(M17) fairness note

## Context

The design (revision 3) specifies a custom UDP data plane secured by
`Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` (D2), and keeps QUIC DATAGRAM (RFC 9221)
as an alternate that must pass the Phase 0 benchmark before being reconsidered
(D12). M02 must pick exactly one and prove the gate, or stop and revise the
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
| Hot-path allocation | seal 1180 B: **1 alloc / 16 B**; seal+open: 2 / 32 B | `SendDatagram`: **10 allocs / 4.5 KB**; echo: 24 / 8.5 KB |
| Congestion control | ours to build (design §9.5) — see fairness result | mature (Cubic) but **internal**: no pacing hook, not observable enough for per-path replica budgets |
| Loopback echo RTT (best case, no wire delay) | n/a | p50 91 µs / p99 414 µs / **p99.9 761 µs** of pure stack overhead |

### Crypto packet-rate (SPIKE-63)

Target (derived from design §17.4's 10,000 pps, ×4 copies, ×2 ends, ×2
headroom): **≥ 160,000 AEAD ops/s single-core at 1180 B.**

Measured (i7-14650HX, Go 1.27, flynn/noise): **~940,000 seal+open/s/core**
(~1.98 M seal-only/s). **~6× headroom on one core**, before spreading across
path actors / cores. **PASS.** Raw: `test/results/phase0/crypto-bench.txt`.

### Congestion / fairness (SPIKE-62)

Deterministic fluid sim, design §9.5 controller vs one-packet-per-RTT AIMD TCP
on a shared drop-tail bottleneck. Raw:
`test/results/phase0/congestion-sim.txt`.

- **Anti-flood floor (native TCP ≥ 35 %): PASS at every buffer depth.** A greedy
  RED_MPUDP flow never starves TCP — TCP takes 53 % on shallow buffers, *more*
  as the buffer grows.
- **Equality (Jain ≥ 0.90): PASS only for drop-tail buffers ≲ 20 ms** (≈ the
  15 ms queue-delay target). Beyond that the delay-gated additive increase
  stops firing and the controller **yields** to loss-based TCP (Jain ≈ 0.60 at
  a 60 ms buffer). It self-limits; it does not misbehave.
- Controller mechanics (additive increase = MTU/srtt, once-per-RTT halving,
  stale-feedback collapse, two-round delay cut) are all exact (SPIKE-42..45).

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
the whole scheduler design **without** resolving the fairness question.

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
| Selected approach meets the fairness threshold | **CONDITIONAL** — anti-flood floor passes everywhere; Jain ≥ 0.90 only for buffers ≲ 20 ms; the delay-gated controller yields (does not starve) on bloated buffers |

The fairness gate is not cleanly met by the design §9.5 controller **as
written**. **Sign-off decision (2026-09-03): accept the conditional pass and
defer the controller change to M17** — it is recorded as a known limitation in
spec §9.5 (TODO(M17)) and does not block M03–M16. Because QUIC is rejected on
independent grounds, the path forward is to **revise the controller**, not the
transport:

- **Design change (§9.5):** add a bounded loss-driven additive-increase path so
  the controller stays competitive with loss-based TCP on bloated buffers, at a
  bounded, measured latency cost. Keep the delay-gated increase as the primary
  mode.
- **Verify for real at M17** (`plan:CC-FAIR-01..11`) against `tc netem` with the
  release thresholds; if it still cannot meet them, D-P0-1 reopens.

This is within what the M02 gate allows ("stop implementation and revise the
design"): the revision is scoped to §9.5, does not touch the wire format or
security model, and does not block M03–M16.

## Design updates (SPIKE-65)

- **D2, D12: unchanged.** The selection matches the design; no edit.
- **§9.5: `TODO(M17)` note added** (spec revision 4) — a pointer to this record
  and the fairness finding, and the commitment to add a bounded loss-driven
  increase before M17. The normative controller text is unchanged for now.

## Consequences

- M03 (shared foundations) may begin once this record is signed off.
- `experiments/quicdatagram/` is retained as evidence, excluded from the main
  module and CI by its own `go.mod` (SPIKE-66).
- `internal/congestion/phase0sim/` stays as the starting point for the M17
  controller.
- Follow-up (tracked, not blocking): bump `golang.org/x/crypto` / `x/sys` off
  the 2021 revision flynn/noise pins, then re-run `crypto-bench`.
- Open item D-P0-4 remains open, owned by M17.
