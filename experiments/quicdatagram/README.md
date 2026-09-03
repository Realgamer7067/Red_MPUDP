# QUIC DATAGRAM spike (SPIKE-49..59)

Isolated module (`experiments/quicdatagram/go.mod`) so `quic-go` never enters
the main module. Evaluated `github.com/quic-go/quic-go v0.62.0`. Deleted once
the transport decision (SPIKE-66) is recorded.

Raw run: `test/results/phase0/quic-datagram.txt`.

## What works

| Requirement | Mechanism | Verdict |
|---|---|---|
| Caller-owned UDP socket | `quic.Transport{Conn: net.PacketConn}` + `Dial` / `Listen` | ✅ clean |
| Interface bind (`SO_BINDTODEVICE`, `SO_MARK`) | applied on the caller's conn via `net.ListenConfig.Control` (needs `CAP_NET_ADMIN`) | ✅ same path M07 uses |
| Unreliable datagrams, no stream fallback | `Conn.SendDatagram` / `Conn.ReceiveDatagram` | ✅ |
| `ReceiveDatagram` cancellation | honours `context` | ✅ |
| Datagram support negotiation | `ConnectionState().SupportsDatagrams.{Local,Remote}` | ✅ |

## What does not meet RED_MPUDP requirements (SPIKE-59)

| Requirement (design ref) | quic-go v0.62 | Verdict |
|---|---|---|
| Know the current max datagram size **before** sending (§10 — never send oversized to discover the limit) | only via `*DatagramTooLargeError` **after** a rejected send; no getter | ❌ blocker |
| Per-datagram / per-copy delivery + loss feedback (§8 PATH_REPORT, winner/rescue rate, per-path loss) | none exposed; tracked internally for CC only | ❌ blocker |
| Send queue bounded by packets **and** bytes, with per-class priority, deadlines, tail-drop, expired-replica drop (§9.3) | one fixed **32-frame** FIFO; `SendDatagram` **blocks** when full | ❌ blocker |
| Explicit app-visible packet numbers for the replay/dedup windows (§6.2, §7) | QUIC packet numbers are internal; DATAGRAM frames carry no app sequence | ➖ we add our own header inside the datagram anyway |
| Pace every DATA copy through an observable per-direction controller (§9.5) | QUIC CC (Cubic) is internal; no pacing hook, minimal observability | ⚠️ not enough for per-path replica budgets |
| 4 independently keyed path copies | = 4 separate QUIC connections: 4 handshakes, 4 CC instances, 4× timers/state | ⚠️ heavy |
| Low allocation on the hot path | **10 allocs / ~4.5 KB per `SendDatagram`**; full echo 24 allocs / 8.5 KB | ❌ vs 2 allocs / 32 B for the Noise transport |

## Performance (loopback, i7-14650HX, best case — no wire delay)

| Metric | QUIC DATAGRAM | Noise transport (SPIKE-29..38) |
|---|---:|---:|
| send 1200 B | 6.7 µs/op, 10 allocs, 4.5 KB | seal 1180 B: 0.5 µs, 1 alloc, 16 B |
| send+echo+recv 1200 B | 56 µs/op, 24 allocs, 8.5 KB | seal+open 1180 B: 1.06 µs, 2 allocs, 32 B |
| echo RTT p50 / p99 / p99.9 | 91 µs / 414 µs / 761 µs | n/a (no round trip in the crypto bench) |

(The latency harness sustains ~3.5k pps because of Go timer granularity at a
100 µs tick, not a QUIC limit; the RTT distribution is still representative of
per-datagram processing cost. p99.9 ≈ 0.76 ms of pure stack overhead on
loopback is notable.)

## Conclusion

QUIC DATAGRAM via quic-go **does not meet the RED_MPUDP data-plane requirements
without a fork or a large application layer on top**. Each of the three blockers
forces machinery (pre-send size discovery, an app-level delivery-ACK protocol, a
real send-queue layer) that reproduces what custom-UDP needs anyway — while
adding QUIC's per-datagram overhead (~7× time, ~5× allocs) and giving up the
explicit packet-number model the replay / dedup / scheduler design is built on.

QUIC's genuine advantage — a mature congestion controller — is not exposed
enough to drive the per-path replica budgets of design §9.5, so switching to
QUIC would **not** resolve the D-P0-4 fairness question; it would replace the
whole scheduler design.

**Recommendation: custom UDP + Noise remains the v1 transport (design D2/D12
upheld).** Address D-P0-4 by adding a bounded loss-driven increase path to the
§9.5 controller and verifying against real `tc netem` at M17 (`plan:CC-FAIR-*`).
