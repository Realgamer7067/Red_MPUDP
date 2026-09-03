# RED_MPUDP v1 traceability

Every v1 success criterion from the design (§1.1 of
`docs/superpowers/specs/2026-09-02-red-mpudp-design.md`, revision 3) is copied
here verbatim as an unchecked requirement (LOCK-06), given a stable identifier
(LOCK-07), mapped to the milestone that implements it (LOCK-08) and to the
test or benchmark that will prove it (LOCK-09).

- **Status** is `unchecked` until the linked evidence exists and passes.
  RELEASE-01 requires every row to reach `checked` with a saved test or
  benchmark artifact.
- **Proof** entries are *plan references* (`plan:<STEP-ID>`) until the
  corresponding test file lands, then they become the real test path/name.
- Milestone IDs are from
  `docs/superpowers/plans/2026-09-03-red-mpudp-implementation-plan.md` §3.

| ID | Requirement (design §1.1) | Implements | Proof | Status |
|----|---------------------------|------------|-------|--------|
| REQ-001 | A Linux client can carry IPv4 TCP, UDP, and ICMP traffic through one Linux exit server. | M13 | `plan:E2E-03`, `plan:E2E-04`, `plan:E2E-05`, `plan:VERIFY-13` | unchecked |
| REQ-002 | Two physical client interfaces can remain active in the same logical session. | M14 | `plan:MULTI-01`, `plan:MULTI-02`, `plan:HS-TEST-09`, `plan:VERIFY-14` | unchecked |
| REQ-003 | Low-rate latency-sensitive traffic is replicated across all healthy eligible paths and deduplicated correctly in both directions. | M14, M18 | `plan:SCHED-TEST-02`, `plan:SCHED-TEST-05`, `plan:MULTI-41`, `plan:VERIFY-14` | unchecked |
| REQ-004 | Killing either path in the deterministic integration test creates no global DATA-sequence gap while the other path remains healthy and has capacity. | M14 | `plan:MULTI-36`, `plan:MULTI-37`, `plan:MULTI-39`, `plan:PERF-17`, `plan:VERIFY-16` | unchecked |
| REQ-005 | A slow secondary path cannot build an unbounded queue or materially inflate the primary path's latency. | M17, M18 | `plan:SCHED-TEST-08`, `plan:SCHED-TEST-09`, `plan:SCHED-TEST-10`, `plan:PERF-20`, `plan:VERIFY-18` | unchecked |
| REQ-006 | Every accepted datagram is authenticated before replay, endpoint, metric, or dedup state is committed. **(Invariant, not a feature — every receive path must hold it.)** | M10, M12 + SEC-01 audit | `plan:REPLAY-13`, `plan:PATH-TEST-05`, `plan:DEDUP` gate, `plan:SEC-01`, `plan:VERIFY-20` | unchecked |
| REQ-007 | Outer UDP fragmentation is avoided, and path-MTU reduction is handled. | M16 | `plan:PMTU-TEST-13`, `plan:PMTU-TEST-14`, `plan:PMTU-TEST-15`, `plan:VERIFY-21` | unchecked |
| REQ-008 | The client has explicit IPv6, DNS, LAN-bypass, and kill-switch behavior; it never silently calls a configuration "full tunnel" while leaking traffic. | M20 | `plan:LEAK-01`..`plan:LEAK-16`, `plan:VERIFY-25` | unchecked |
| REQ-009 | Route, nftables, TUN, and sysctl changes are idempotent and recoverable after a crash or restart. | M05, M08, M20 | `plan:JOURNAL-19`, `plan:JOURNAL-20`, `plan:ROUTE-27`, `plan:ROUTE-29`, `plan:STOP-08`, `plan:STOP-09`, `plan:VERIFY-24` | unchecked |
| REQ-010 | The parser, replay windows, dedup windows, scheduler, and session state machine have unit, fuzz, race, and namespace integration coverage. | M09, M10, M11, M18 (coverage); M22 (final) | `plan:VERIFY-05`..`plan:VERIFY-09`, race CI job (`plan:BOOT-18`) | unchecked |

## Maintenance

- A pull request that changes the wire format, security model, MTU arithmetic,
  or Linux routing behaviour updates this table in the same PR (see the
  pull-request template, LOCK-17).
- When a milestone lands, replace its `plan:<STEP-ID>` proof entries with the
  concrete test path and name, and flip Status to `checked` only after the
  test passes in CI and its artifact is saved.
