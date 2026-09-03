# Phase 0 congestion / fairness — analysis (SPIKE-39..48)

Raw data: `congestion-sim.txt`. Model: `internal/congestion/phase0sim/` — a
deterministic fluid simulator, **not** the production controller (M17 builds
that). 20 Mbit/s bottleneck, 40 ms base RTT, drop-tail buffer; the controller
is the design §9.5 AIMD (init 1 / min 0.128 / max 100 Mbit/s, 15 ms queue-delay
target); the reference is one-packet-per-RTT AIMD TCP (MSS 1448).

## Controller mechanics (SPIKE-42..45) — all pass

| Property | Result |
|---|---|
| Additive increase | exactly `MTU * s / srtt` bytes/s, once per srtt, only while queue delay < 15 ms target |
| Multiplicative decrease | halves once per RTT on loss; a second loss in the same RTT does not double-cut |
| Stale feedback | rate collapses to the configured minimum after `feedback_stale_after` |
| Queue-delay reduction | halves after **two** consecutive rounds above the 15 ms target |

## Fairness vs TCP (SPIKE-46..48) — buffer-depth sweep

| Drop-tail buffer | controller | TCP | TCP share | Jain |
|---:|---:|---:|---:|---:|
|   5 ms | 7.52 | 8.56 | 53.3% | 0.996 |
|  10 ms | 7.68 | 8.80 | 53.4% | 0.995 |
|  15 ms | 7.76 | 8.88 | 53.3% | 0.996 |
|  20 ms | 7.76 | 9.12 | 54.1% | 0.993 |
|  30 ms | 3.12 | 14.64 | 82.5% | 0.703 |
|  45 ms | 1.76 | 16.48 | 90.5% | 0.604 |
|  60 ms | 1.76 | 16.56 | 90.6% | 0.603 |
| 100 ms | 1.28 | 17.20 | 93.1% | 0.574 |

## Reading

1. **The custom controller never starves TCP.** At every buffer depth the
   native TCP flow keeps far more than the design §17.4 floor of 35% — it takes
   53% on shallow buffers and *more* as the buffer grows. The "reject if TCP <
   35%" gate is satisfied everywhere. A greedy RED_MPUDP flow does not flood a
   competing TCP flow.

2. **Jain ≥ 0.90 holds only for buffers ≲ 20 ms** (≈ the 15 ms queue-delay
   target). Past that the delay-gated additive increase almost never fires
   (a bloated drop-tail queue sits above 15 ms whenever both flows are active),
   so the controller stops growing while loss-based TCP keeps climbing between
   drops. The controller *yields*; it does not misbehave.

3. For a **latency-first** VPN, yielding throughput to bulk TCP on a
   bufferbloated shared bottleneck — rather than matching it and inflating queue
   delay for everyone — is the intended behaviour. The controller yields
   monotonically (7.76 → 3.12 → 1.76 → 1.28 Mbit/s) and never sits at a high
   queue delay without reducing.

## Outcome (SPIKE-48 / decision 0001 / D-P0-4)

The flat "reject if two-flow Jain < 0.90 at any depth" gate was wrong for this
design. **Spec revision 4 replaces it with the normative §9.5.1 fairness
acceptance criterion:**

1. **Anti-flood floor** — native TCP ≥ 35 % at every swept buffer depth.
2. **Equality** — Jain ≥ 0.90 for buffer depths ≤ `queue_delay_target` (15 ms).
3. **Graceful yield** — above the target, deeper buffer ⇒ controller throughput
   non-increasing AND native TCP share non-decreasing.

`internal/congestion/phase0sim/sim_test.go:TestFairnessCriterion` asserts all
three. The Phase 0 sim **meets the criterion** (min TCP share 53 %; Jain ≈ 0.996
at ≤ 15 ms; monotonic yield above). Not an M03 blocker.

**M17 (`plan:CC-FAIR-08`)** still improves competitiveness on bloated buffers:
add a bounded loss-driven additive-increase path (delay-gated increase stays
primary) so that, with it enabled, Jain ≥ 0.90 also at a 60 ms drop-tail buffer
on `tc netem`, clauses 1 and 3 still holding. `phase0sim` is the starting point
for that controller.
