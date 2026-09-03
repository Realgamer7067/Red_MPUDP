# RED_MPUDP V1 — Granular Implementation Plan

**Status:** Ready for execution after the Phase 0 decision gate
**Date:** 2026-09-03
**Source design:** [2026-09-02-red-mpudp-design.md](../specs/2026-09-02-red-mpudp-design.md)
**Target:** Linux client and Linux exit server, IPv4 inner and outer traffic

## 1. How to execute this plan

This plan deliberately breaks the implementation into small, ordered changes.
Each checkbox should produce one observable behavior, one focused test, or one
bounded piece of infrastructure. Do not combine unrelated checkboxes merely to
reduce commit count.

For every checkbox that changes behavior:

1. Add or adjust the smallest focused test.
2. Run only that test and confirm it fails for the expected reason.
3. Implement the smallest production change that satisfies the test.
4. Run the focused test again.
5. Run all tests in the affected package.
6. Run `gofmt` on changed Go files.
7. Run `go vet` on the affected package.
8. Commit at the checkpoint shown for the milestone.

Rules for the entire implementation:

- Keep the tree buildable at every checkpoint.
- Never expose a plaintext production mode.
- Never send unpaced DATA, including during intermediate milestones.
- Never mutate host networking before configuration validation and journal
  creation succeed.
- Never commit replay, dedup, liveness, endpoint, or metric state before AEAD
  authentication succeeds.
- Never reuse a key/nonce pair.
- Never intentionally emit a fragmented outer UDP datagram.
- Never proceed past a milestone gate while any required check is failing.
- Keep privileged integration tests isolated in network namespaces.
- Preserve benchmark outputs and test parameters as build artifacts.

## 2. Fixed implementation assumptions

- The default Go module path in this plan is
  `github.com/Realgamer7067/Red_MPUDP`. Change it only before step BOOT-04 if
  the repository will use another canonical remote path.
- The binary name is `red-mpudp`.
- The server's physical WAN MTU may remain 1500.
- The initial client and server TUN MTU is 1180.
- V1 encapsulation overhead is 88 bytes: 44-byte authenticated header,
  16-byte AEAD tag, 8-byte UDP header, and 20-byte outer IPv4 header.
- An inner packet of 1180 bytes therefore produces a 1268-byte outer packet.
- The v1 base outer PMTU is 1200, corresponding to an inner MTU of 1112.
- The configured inner MTU range is 1112 through 1400.
- An inner MTU of 1400 produces a 1488-byte outer packet.
- All multi-byte wire integers are unsigned and big-endian.
- The implementation uses `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` unless the
  Phase 0 gate selects QUIC DATAGRAM instead.

## 3. Milestone dependency order

```text
M00 design lock
 -> M01 repository bootstrap
 -> M02 Phase 0 feasibility gate
 -> M03 shared foundations
 -> M04 configuration and identity
 -> M05 namespace harness and mutation journal
 -> M06 TUN implementation
 -> M07 UDP transport
 -> M08 policy routing and nftables primitives
 -> M09 wire codecs
 -> M10 replay, dedup, and IPv4 validation
 -> M11 authenticated handshake
 -> M12 single-path encrypted data plane
 -> M13 single-path VPN forwarding
 -> M14 multipath replication
 -> M15 directional health and feedback
 -> M16 PMTU
 -> M17 congestion controller and pacers
 -> M18 adaptive scheduler and queues
 -> M19 roaming, rekey, and lifecycle
 -> M20 full-tunnel hardening
 -> M21 observability and operations
 -> M22 verification and release
```

## 4. M00 — Design lock and traceability

### Objective

Remove implementation ambiguity before creating Go packages.

### Steps

- [x] **LOCK-01:** Confirm the source design file is the normative protocol
  document for v1.
- [x] **LOCK-02:** Record the Git commit containing the design revision in
  `docs/superpowers/plans/design-baseline.txt`.
- [x] **LOCK-03:** Confirm whether the canonical module path will be
  `github.com/Realgamer7067/Red_MPUDP`.
- [x] **LOCK-04:** Record the selected Go version in
  `docs/development/toolchain.md`.
- [x] **LOCK-05:** Record the minimum supported Linux kernel after checking the
  required TUN, nftables, policy-rule, socket-mark, and error-queue features.
- [x] **LOCK-06:** Copy every v1 success criterion into
  `docs/superpowers/plans/v1-traceability.md` as an unchecked requirement.
- [x] **LOCK-07:** Give every success criterion a stable identifier from
  `REQ-001` upward.
- [x] **LOCK-08:** Map each requirement to the milestone that implements it.
- [x] **LOCK-09:** Map each requirement to the test that will eventually prove
  it.
- [x] **LOCK-10:** Record unresolved design questions in
  `docs/superpowers/plans/open-decisions.md`.
- [x] **LOCK-11:** Resolve whether production configuration starts at TUN MTU
  1180 or probes toward a preferred MTU before committing the session value.
- [x] **LOCK-12:** If the MTU decision differs from the source design, update
  the design before implementing PMTU.
- [x] **LOCK-13:** Resolve the exact minimum kernel and distribution matrix for
  release testing.
- [x] **LOCK-14:** Resolve the canonical config locations for client and server.
- [x] **LOCK-15:** Resolve whether a single binary with subcommands remains the
  release shape.
- [x] **LOCK-16:** Confirm that IPv6 tunnelling, multi-server operation, traffic
  obfuscation, and throughput bonding remain outside v1.
- [x] **LOCK-17:** Add a rule to the pull-request template requiring traceability
  updates for protocol changes.

### Gate

- [x] Every design question that changes the wire format, security model, MTU
  arithmetic, or Linux routing behavior is resolved or explicitly assigned to
  the Phase 0 stop/go decision.

### Checkpoint

```text
docs: lock RED_MPUDP v1 implementation baseline
```

## 5. M01 — Repository and toolchain bootstrap

### Objective

Create a minimal, reproducible Go project without implementing VPN behavior.

### Steps

- [x] **BOOT-01:** Add `.gitignore` entries for Go binaries, coverage files,
  fuzz corpora generated at runtime, profiles, benchmark output, and local key
  material.
- [x] **BOOT-02:** Add `.editorconfig` with UTF-8, LF, final newline, and Go tab
  rules.
- [x] **BOOT-03:** Verify the chosen Go toolchain is installed.
- [x] **BOOT-04:** Run `go mod init github.com/Realgamer7067/Red_MPUDP`.
- [x] **BOOT-05:** Create `cmd/red-mpudp/main.go` with a process entry point that
  returns a nonzero code for an unknown subcommand.
- [x] **BOOT-06:** Create `internal/buildinfo/buildinfo.go` for version, commit,
  and build-date values.
- [x] **BOOT-07:** Add a `version` subcommand.
- [x] **BOOT-08:** Add a unit test for deterministic `version` output when build
  metadata is injected.
- [x] **BOOT-09:** Create `Makefile` target `fmt`.
- [x] **BOOT-10:** Create `Makefile` target `vet`.
- [x] **BOOT-11:** Create `Makefile` target `test-unit`.
- [x] **BOOT-12:** Create `Makefile` target `test-race`.
- [x] **BOOT-13:** Create `Makefile` target `test-integration` with an explicit
  Linux/root preflight.
- [x] **BOOT-14:** Create `Makefile` target `test-fuzz-smoke` with bounded fuzz
  duration.
- [x] **BOOT-15:** Create `Makefile` target `bench`.
- [x] **BOOT-16:** Create `Makefile` target `build` with reproducible version
  flags.
- [x] **BOOT-17:** Add `go vet` and unit-test CI jobs.
- [x] **BOOT-18:** Add a race-test CI job.
- [x] **BOOT-19:** Add a build-artifact CI job for Linux amd64.
- [x] **BOOT-20:** Add a separate privileged integration workflow that cannot
  run on untrusted pull-request code with repository secrets.
- [x] **BOOT-21:** Add dependency-cache keys containing the Go version and
  `go.sum` hash.
- [x] **BOOT-22:** Add `CONTRIBUTING.md` with the test commands and privilege
  boundaries.
- [x] **BOOT-23:** Add `SECURITY.md` with a private vulnerability-reporting
  process and a warning not to attach keys or plaintext packet captures.
- [x] **BOOT-24:** Run `go mod tidy`.
- [x] **BOOT-25:** Run every non-privileged Make target.

### Gate

- [x] A fresh checkout builds and passes unit/race checks using only documented
  commands.

### Checkpoint

```text
build: bootstrap Go module and CI
```

## 6. M02 — Phase 0 feasibility and transport decision

### Objective

Prove the riskiest assumptions before building Linux VPN plumbing around them.

### Dependency review

- [x] **SPIKE-01:** Create `docs/development/dependencies.md`.
- [x] **SPIKE-02:** List every direct dependency and the capability it supplies.
- [x] **SPIKE-03:** Record the license of every direct dependency.
- [x] **SPIKE-04:** Record the maintenance status and most recent reviewed
  release of the candidate Noise package.
- [x] **SPIKE-05:** Confirm the Noise package exposes IKpsk2 and explicit
  transport nonce control without a private fork.
- [x] **SPIKE-06:** Record the selected YAML, netlink, nftables, D-Bus, and
  Prometheus packages without importing them yet.
- [x] **SPIKE-07:** Pin the Noise dependency.
- [x] **SPIKE-08:** Check in the relevant official Noise test vectors or a script
  that fetches and hash-verifies them.

### Noise handshake spike

- [x] **SPIKE-09:** Create `internal/noisehandshake/pattern_test.go`.
- [x] **SPIKE-10:** Construct deterministic initiator and responder static keys
  for tests.
- [x] **SPIKE-11:** Complete one IKpsk2 handshake in memory.
- [x] **SPIKE-12:** Assert both peers derive matching initiator-to-responder
  cipher states.
- [x] **SPIKE-13:** Assert both peers derive matching responder-to-initiator
  cipher states.
- [x] **SPIKE-14:** Assert the two directions do not share a key.
- [x] **SPIKE-15:** Assert a wrong server static key fails.
- [x] **SPIKE-16:** Assert a wrong client static key fails server authorization.
- [x] **SPIKE-17:** Assert a wrong PSK fails.
- [x] **SPIKE-18:** Assert a wrong prologue fails.
- [x] **SPIKE-19:** Assert transcript tampering fails.
- [x] **SPIKE-20:** Run the selected implementation against the official Noise
  vectors.

### Explicit nonce spike

- [x] **SPIKE-21:** Create `internal/noisehandshake/nonce_test.go`.
- [x] **SPIKE-22:** Encrypt packets with transport nonces 0, 1, 2, and 4097.
- [x] **SPIKE-23:** Decrypt those packets in the order 2, 0, 4097, and 1.
- [x] **SPIKE-24:** Assert every valid out-of-order packet opens exactly once.
- [x] **SPIKE-25:** Assert a repeated nonce/ciphertext is rejected by the replay
  layer used in the spike.
- [x] **SPIKE-26:** Assert changing any authenticated header byte breaks AEAD.
- [x] **SPIKE-27:** Assert an authentication failure does not prevent a later
  valid lower-nonce packet from opening after `SetNonce` is called again.
- [x] **SPIKE-28:** Run the explicit-nonce tests under the race detector.

### Crypto throughput spike

- [ ] **SPIKE-29:** Create `internal/noisehandshake/transport_bench_test.go`.
- [ ] **SPIKE-30:** Benchmark seal/open for 64-byte plaintext.
- [ ] **SPIKE-31:** Benchmark seal/open for 256-byte plaintext.
- [ ] **SPIKE-32:** Benchmark seal/open for 768-byte plaintext.
- [ ] **SPIKE-33:** Benchmark seal/open for 1180-byte plaintext.
- [ ] **SPIKE-34:** Benchmark one independently sealed path copy.
- [ ] **SPIKE-35:** Benchmark two independently sealed path copies.
- [ ] **SPIKE-36:** Benchmark four independently sealed path copies.
- [ ] **SPIKE-37:** Record allocations per packet for every benchmark.
- [ ] **SPIKE-38:** Save CPU model, Go version, sample count, and benchmark output
  under `test/results/phase0/`.

### Congestion-control spike

- [ ] **SPIKE-39:** Create a deterministic rate-controller simulator under
  `internal/congestion/`.
- [ ] **SPIKE-40:** Represent pacing rate in integer bytes per second.
- [ ] **SPIKE-41:** Implement a fake monotonic clock for the simulator.
- [ ] **SPIKE-42:** Add one-MTU-per-RTT additive-increase simulation.
- [ ] **SPIKE-43:** Add once-per-RTT multiplicative-decrease simulation.
- [ ] **SPIKE-44:** Add feedback-staleness simulation.
- [ ] **SPIKE-45:** Add queue-delay-triggered reduction simulation.
- [ ] **SPIKE-46:** Run greedy simulated UDP against one TCP-friendly reference
  flow.
- [ ] **SPIKE-47:** Calculate Jain's fairness index from the simulated rates.
- [ ] **SPIKE-48:** Reject the controller design if the reference flow receives
  less than 35% or fairness falls below 0.90 after warm-up.

### QUIC DATAGRAM comparison

- [ ] **SPIKE-49:** Create an isolated `experiments/quicdatagram/` module.
- [ ] **SPIKE-50:** Pin the candidate QUIC implementation in that module only.
- [ ] **SPIKE-51:** Open a QUIC connection using a caller-owned UDP socket.
- [ ] **SPIKE-52:** Bind that UDP socket to a selected Linux interface.
- [ ] **SPIKE-53:** Send unreliable datagrams without stream fallback.
- [ ] **SPIKE-54:** Measure supported maximum datagram-size visibility.
- [ ] **SPIKE-55:** Measure delivery/loss feedback visibility.
- [ ] **SPIKE-56:** Measure queue ownership and cancellation behavior.
- [ ] **SPIKE-57:** Benchmark p50/p95/p99 latency at the target packet rate.
- [ ] **SPIKE-58:** Benchmark throughput and allocations.
- [ ] **SPIKE-59:** Record whether public APIs meet every RED_MPUDP requirement
  without a fork.

### Decision

- [ ] **SPIKE-60:** Create `docs/decisions/0001-v1-transport.md`.
- [ ] **SPIKE-61:** Put Noise/custom-UDP evidence and QUIC evidence in the same
  comparison table.
- [ ] **SPIKE-62:** Record the congestion/fairness result.
- [ ] **SPIKE-63:** Record the crypto packet-rate result.
- [ ] **SPIKE-64:** Select exactly one v1 transport.
- [ ] **SPIKE-65:** Update the source design if the selection changes D2 or D12.
- [ ] **SPIKE-66:** Remove the unselected experiment from normal build and test
  paths while retaining its decision evidence.

### Gate

- [ ] Explicit nonces work safely out of order.
- [ ] Independent path copies meet the packet-rate target.
- [ ] The selected approach meets the fairness threshold.
- [ ] The selected approach requires no private security-library fork.
- [ ] If any gate fails, stop implementation and revise the design.

### Checkpoint

```text
docs: record RED_MPUDP phase-zero transport decision
```

## 7. M03 — Shared deterministic foundations

### Objective

Build the small utilities required to test state machines without real time or
unbounded memory.

### Clock

- [ ] **BASE-01:** Create `internal/clock/clock.go` with `Now`, timer, and ticker
  abstractions backed by monotonic time.
- [ ] **BASE-02:** Create `internal/testclock/clock.go`.
- [ ] **BASE-03:** Add deterministic timer advancement.
- [ ] **BASE-04:** Add deterministic ticker advancement.
- [ ] **BASE-05:** Define ordering for timers firing at the same instant.
- [ ] **BASE-06:** Test timer cancellation.
- [ ] **BASE-07:** Test ticker stop.
- [ ] **BASE-08:** Test that callbacks cannot run while the fake clock lock is
  held.

### Bounded data structures

- [ ] **BASE-09:** Create a generic fixed-capacity ring under
  `internal/bounded/`.
- [ ] **BASE-10:** Test empty-ring behavior.
- [ ] **BASE-11:** Test full-ring behavior.
- [ ] **BASE-12:** Test wraparound behavior.
- [ ] **BASE-13:** Add a bounded byte-counted queue primitive.
- [ ] **BASE-14:** Reject an item larger than the byte limit.
- [ ] **BASE-15:** Reject an item when the packet limit is reached.
- [ ] **BASE-16:** Track queue insertion monotonic time.
- [ ] **BASE-17:** Test exact byte accounting on enqueue/dequeue/drop.

### Buffers and errors

- [ ] **BASE-18:** Create `internal/packetbuf/pool.go` with fixed size classes
  sufficient for the maximum v1 datagram.
- [ ] **BASE-19:** Define one-owner transfer semantics in package documentation.
- [ ] **BASE-20:** Add debug-only double-release detection.
- [ ] **BASE-21:** Test release on every modeled error path.
- [ ] **BASE-22:** Create stable bounded reason-code enums for drops, handshake
  failures, closes, and health transitions.
- [ ] **BASE-23:** Test that reason codes have stable string forms.
- [ ] **BASE-24:** Prohibit attacker-controlled text from becoming a reason-code
  value.

### Gate

- [ ] `go test -race` passes for all foundation packages.
- [ ] Queue and ring tests use no wall-clock sleeps.

### Checkpoint

```text
feat: add deterministic clocks and bounded primitives
```

## 8. M04 — Configuration, key material, and CLI

### Objective

Parse and validate all operator input before privileged or network activity.

### Configuration schema

- [ ] **CONF-01:** Create `internal/config/client.go` with fields from the client
  YAML example.
- [ ] **CONF-02:** Create `internal/config/server.go` with fields from the server
  YAML example.
- [ ] **CONF-03:** Represent durations with an explicit YAML duration type.
- [ ] **CONF-04:** Represent marks and routing-table identifiers with bounded
  integer types.
- [ ] **CONF-05:** Represent all addresses with `netip` types after parsing.
- [ ] **CONF-06:** Reject unknown YAML fields.
- [ ] **CONF-07:** Reject duplicate YAML keys.
- [ ] **CONF-08:** Reject a client server endpoint that is not a literal IPv4
  address and port.
- [ ] **CONF-09:** Reject unspecified, multicast, broadcast, and zero server
  addresses.
- [ ] **CONF-10:** Apply the documented client defaults.
- [ ] **CONF-11:** Apply the documented server defaults.
- [ ] **CONF-12:** Validate TUN MTU range 1112 through 1400.
- [ ] **CONF-13:** Validate maximum paths range 1 through 4.
- [ ] **CONF-14:** Validate dedup-window range 4096 through 1048576.
- [ ] **CONF-15:** Require dedup-window size to be a power of two.
- [ ] **CONF-16:** Validate probe interval range 100 ms through 5 seconds.
- [ ] **CONF-17:** Validate positive pacing rates and min/initial/max ordering.
- [ ] **CONF-18:** Validate queue packet and byte limits.
- [ ] **CONF-19:** Validate interface names without truncation.
- [ ] **CONF-20:** Reject duplicate interface names.
- [ ] **CONF-21:** Reject duplicate firewall marks.
- [ ] **CONF-22:** Reject duplicate route-table IDs.
- [ ] **CONF-23:** Reject collision between the tunnel table and path tables.
- [ ] **CONF-24:** Reject overlapping rule-priority ranges.
- [ ] **CONF-25:** Reject an invalid TUN subnet or server address.
- [ ] **CONF-26:** Reject peer tunnel addresses outside the configured subnet.
- [ ] **CONF-27:** Reject duplicate peer tunnel addresses.
- [ ] **CONF-28:** Reject duplicate peer names.
- [ ] **CONF-29:** Reject metrics listeners outside loopback without explicit
  opt-in.
- [ ] **CONF-30:** Test that validation has no network or filesystem mutation.

### Key file handling

- [ ] **ID-01:** Create `internal/identity/keyfile.go`.
- [ ] **ID-02:** Decode unpadded RFC 4648 standard base64.
- [ ] **ID-03:** Accept one optional trailing newline.
- [ ] **ID-04:** Reject leading/trailing spaces.
- [ ] **ID-05:** Reject padded base64.
- [ ] **ID-06:** Reject decoded lengths other than 32 bytes.
- [ ] **ID-07:** Reject group-readable private-key files.
- [ ] **ID-08:** Reject world-readable private-key files.
- [ ] **ID-09:** Reject group-readable PSK files.
- [ ] **ID-10:** Reject world-readable PSK files.
- [ ] **ID-11:** Permit public-key files to be world-readable.
- [ ] **ID-12:** Ensure key parsing errors never contain key bytes.

### Key generation and derivation

- [ ] **ID-13:** Create cryptographically random X25519 private-key generation.
- [ ] **ID-14:** Derive the matching X25519 public key.
- [ ] **ID-15:** Create cryptographically random 32-byte PSK generation.
- [ ] **ID-16:** Fail closed on random-source errors.
- [ ] **ID-17:** Write new private material with mode 0600 using exclusive
  creation.
- [ ] **ID-18:** Refuse to overwrite an existing key file.
- [ ] **ID-19:** Derive `peer_id` from the first eight SHA-256 bytes in network
  byte order.
- [ ] **ID-20:** Derive the HKDF salt from the protocol key-schedule string.
- [ ] **ID-21:** Derive `preauth_key` with the specified HKDF info string.
- [ ] **ID-22:** Derive `noise_psk` with the specified HKDF info string.
- [ ] **ID-23:** Assert the two derived keys differ.
- [ ] **ID-24:** Add deterministic peer-ID and HKDF golden vectors.

### CLI

- [ ] **CLI-01:** Add `keygen` subcommand.
- [ ] **CLI-02:** Require an explicit output path for `keygen`.
- [ ] **CLI-03:** Add `public-key` subcommand.
- [ ] **CLI-04:** Add `psk` subcommand.
- [ ] **CLI-05:** Add `check-config client` subcommand.
- [ ] **CLI-06:** Add `check-config server` subcommand.
- [ ] **CLI-07:** Ensure config-check commands perform no network mutation.
- [ ] **CLI-08:** Add `client` subcommand argument parsing without starting the
  data plane.
- [ ] **CLI-09:** Add `server` subcommand argument parsing without starting the
  data plane.
- [ ] **CLI-10:** Add stable nonzero exit codes for usage, configuration,
  permission, and runtime failures.
- [ ] **CLI-11:** Test that secrets supplied directly as CLI flags are rejected.
- [ ] **CLI-12:** Test that help output contains no sample secret value.

### Gate

- [ ] Valid example client and server configs load deterministically.
- [ ] Every invalid boundary has a focused table-driven test.
- [ ] Config and identity tests pass under the race detector.

### Checkpoint

```text
feat: add strict configuration and identity tooling
```

## 9. M05 — Integration namespace harness and mutation journal

### Objective

Create a deterministic, recoverable place to exercise privileged networking.

### Namespace harness

- [ ] **HARNESS-01:** Create `test/integration/preflight_linux_test.go`.
- [ ] **HARNESS-02:** Detect Linux before running privileged tests.
- [ ] **HARNESS-03:** Detect effective `CAP_NET_ADMIN`.
- [ ] **HARNESS-04:** Detect availability of `ip`, `tc`, and `nft` test tools.
- [ ] **HARNESS-05:** Skip with one precise reason when prerequisites are absent.
- [ ] **HARNESS-06:** Create unique namespace names per test process.
- [ ] **HARNESS-07:** Create `client-ns`.
- [ ] **HARNESS-08:** Create `server-ns`.
- [ ] **HARNESS-09:** Create `internet-ns`.
- [ ] **HARNESS-10:** Create path-A veth pair.
- [ ] **HARNESS-11:** Move path-A endpoints into client/server namespaces.
- [ ] **HARNESS-12:** Create path-B veth pair.
- [ ] **HARNESS-13:** Move path-B endpoints into client/server namespaces.
- [ ] **HARNESS-14:** Create server-to-internet veth pair.
- [ ] **HARNESS-15:** Assign deterministic IPv4 subnets to all three links.
- [ ] **HARNESS-16:** Bring loopback and veth links up.
- [ ] **HARNESS-17:** Install namespace-local routes.
- [ ] **HARNESS-18:** Start ICMP reachability target in `internet-ns`.
- [ ] **HARNESS-19:** Start a UDP echo target in `internet-ns`.
- [ ] **HARNESS-20:** Start a TCP echo target in `internet-ns`.
- [ ] **HARNESS-21:** Start a deterministic DNS target in `internet-ns`.
- [ ] **HARNESS-22:** Capture subprocess stdout/stderr per test.
- [ ] **HARNESS-23:** Add deterministic `tc netem` delay control per direction.
- [ ] **HARNESS-24:** Add deterministic `tc netem` loss control per direction.
- [ ] **HARNESS-25:** Add deterministic `tc netem` duplication control per
  direction.
- [ ] **HARNESS-26:** Add deterministic `tc netem` reorder control per direction.
- [ ] **HARNESS-27:** Add deterministic rate limiting per direction.
- [ ] **HARNESS-28:** Add link-down and link-up helpers.
- [ ] **HARNESS-29:** Add address-change helper.
- [ ] **HARNESS-30:** Add gateway-change helper.
- [ ] **HARNESS-31:** Delete every created namespace during normal cleanup.
- [ ] **HARNESS-32:** Delete every created namespace after a failed assertion.
- [ ] **HARNESS-33:** Detect and report leaked namespaces after the suite.
- [ ] **HARNESS-34:** Serialize only tests that share global host resources.

### Mutation journal

- [ ] **JOURNAL-01:** Create `internal/journal/schema.go` with schema version,
  role, instance ID, and owned-resource records.
- [ ] **JOURNAL-02:** Exclude all key, PSK, join-token, and packet fields from
  the journal schema.
- [ ] **JOURNAL-03:** Create the runtime directory with restrictive ownership.
- [ ] **JOURNAL-04:** Write journals to a temporary file in the same directory.
- [ ] **JOURNAL-05:** Set journal mode 0600 before writing content.
- [ ] **JOURNAL-06:** Fsync the temporary journal.
- [ ] **JOURNAL-07:** Atomically rename the temporary journal into place.
- [ ] **JOURNAL-08:** Fsync the containing directory.
- [ ] **JOURNAL-09:** Reject unsupported journal schema versions.
- [ ] **JOURNAL-10:** Reject malformed resource identifiers.
- [ ] **JOURNAL-11:** Reject a role/instance mismatch during recovery.
- [ ] **JOURNAL-12:** Record prior sysctl values before changing them.
- [ ] **JOURNAL-13:** Record prior resolver state before changing it.
- [ ] **JOURNAL-14:** Record exact owned route/rule/table identifiers.
- [ ] **JOURNAL-15:** Restore a sysctl only when its current value still equals
  the value installed by RED_MPUDP.
- [ ] **JOURNAL-16:** Preserve operator-modified sysctls and report a conflict.
- [ ] **JOURNAL-17:** Remove only routes and rules with exact journal ownership.
- [ ] **JOURNAL-18:** Remove only the exact named nftables table owned by the
  instance.
- [ ] **JOURNAL-19:** Make recovery idempotent.
- [ ] **JOURNAL-20:** Make a second recovery invocation a no-op success.
- [ ] **JOURNAL-21:** Add `cleanup --state-file` CLI parsing.
- [ ] **JOURNAL-22:** Refuse cleanup when the journal is missing.
- [ ] **JOURNAL-23:** Refuse cleanup when the journal is malformed.
- [ ] **JOURNAL-24:** Test recovery entirely inside a namespace.

### Gate

- [ ] The topology can be created, impaired, and destroyed repeatedly.
- [ ] A deliberately failed test leaves no namespace behind.
- [ ] Journal recovery never removes an unrelated route, rule, nftables table,
  resolver setting, or sysctl change.

### Checkpoint

```text
test: add isolated network harness and mutation journal
```

## 10. M06 — Linux TUN implementation

### Objective

Read and write complete IPv4 packets through a safely owned TUN interface.

### API and lifecycle

- [ ] **TUN-01:** Create `internal/tun/tun.go` with a platform-neutral interface.
- [ ] **TUN-02:** Add a non-Linux implementation returning a stable unsupported
  error.
- [ ] **TUN-03:** Create `internal/tun/tun_linux.go` with Linux build tags.
- [ ] **TUN-04:** Open `/dev/net/tun` with close-on-exec behavior.
- [ ] **TUN-05:** Request `IFF_TUN | IFF_NO_PI`.
- [ ] **TUN-06:** Validate the requested interface name before ioctl.
- [ ] **TUN-07:** Return the actual kernel-assigned name.
- [ ] **TUN-08:** Ensure closing the object closes the file descriptor once.
- [ ] **TUN-09:** Ensure a partial constructor failure closes the descriptor.
- [ ] **TUN-10:** Keep the interface non-persistent in v1.

### Configuration

- [ ] **TUN-11:** Set the configured IPv4 address using structured netlink
  values.
- [ ] **TUN-12:** Set the negotiated MTU.
- [ ] **TUN-13:** Bring the interface up.
- [ ] **TUN-14:** Read back and verify address, MTU, flags, and ifindex.
- [ ] **TUN-15:** Reject an MTU outside 1112 through 1400 before netlink mutation.
- [ ] **TUN-16:** Add a method to reduce MTU after PMTU negotiation.
- [ ] **TUN-17:** Reject an attempted live increase unless the session has an
  authenticated committed value.

### Packet I/O

- [ ] **TUN-18:** Read one complete packet into a caller-owned buffer.
- [ ] **TUN-19:** Distinguish context cancellation from permanent descriptor
  failure.
- [ ] **TUN-20:** Reject a read larger than the configured packet buffer.
- [ ] **TUN-21:** Write one complete packet from a caller-owned buffer.
- [ ] **TUN-22:** Treat a short TUN write as an error.
- [ ] **TUN-23:** Return every pooled buffer on read failure.
- [ ] **TUN-24:** Return every pooled buffer on write failure.
- [ ] **TUN-25:** Add packet and byte counters without unbounded labels.

### Integration

- [ ] **TUN-26:** Create `red0` inside `client-ns`.
- [ ] **TUN-27:** Create `red0` inside `server-ns`.
- [ ] **TUN-28:** Verify each interface starts at MTU 1180.
- [ ] **TUN-29:** Pass one plaintext test-only IPv4 packet between a TUN reader
  and an in-memory peer.
- [ ] **TUN-30:** Guard plaintext forwarding behind an integration-only build
  tag.
- [ ] **TUN-31:** Assert the release binary contains no plaintext forwarding
  switch or subcommand.

### Gate

- [ ] TUN unit tests pass without root through mocks.
- [ ] TUN namespace tests pass with root.
- [ ] Descriptor and buffer leak checks pass on every constructor/error path.

### Checkpoint

```text
feat: add Linux TUN packet I/O
```

## 11. M07 — UDP transport and socket metadata

### Objective

Provide one interface-bound client socket per path and one safe shared server
socket.

### Transport API

- [ ] **UDP-01:** Create `internal/transport/datagram.go` with `Endpoint`,
  `ReceiveMeta`, `PathError`, and `DatagramIO`.
- [ ] **UDP-02:** Document buffer ownership for every interface method.
- [ ] **UDP-03:** Define stable errors for closed, truncated, unsupported, and
  oversized operations.
- [ ] **UDP-04:** Create an in-memory DatagramIO pair for unit tests.
- [ ] **UDP-05:** Add reorder injection to the in-memory transport.
- [ ] **UDP-06:** Add loss injection to the in-memory transport.
- [ ] **UDP-07:** Add duplication injection to the in-memory transport.
- [ ] **UDP-08:** Add bounded receive capacity to the in-memory transport.

### Client socket

- [ ] **UDP-09:** Create `internal/transport/udp/client_linux.go`.
- [ ] **UDP-10:** Resolve the configured interface to a stable ifindex.
- [ ] **UDP-11:** Reject an interface that disappears before socket setup.
- [ ] **UDP-12:** Create an IPv4 UDP socket with close-on-exec and nonblocking
  flags.
- [ ] **UDP-13:** Apply `SO_BINDTODEVICE`.
- [ ] **UDP-14:** Apply the configured `SO_MARK`.
- [ ] **UDP-15:** Bind the selected local IPv4 source address.
- [ ] **UDP-16:** Connect the socket to the literal server endpoint.
- [ ] **UDP-17:** Set `IP_MTU_DISCOVER` to do-not-fragment behavior.
- [ ] **UDP-18:** Enable extended error reception.
- [ ] **UDP-19:** Set requested receive buffer size.
- [ ] **UDP-20:** Set requested send buffer size.
- [ ] **UDP-21:** Read back effective buffer sizes and expose them in a bounded
  diagnostic structure.

### Server socket

- [ ] **UDP-22:** Create `internal/transport/udp/server_linux.go`.
- [ ] **UDP-23:** Bind one IPv4 UDP socket to the configured listen address.
- [ ] **UDP-24:** Enable packet-info ancillary data.
- [ ] **UDP-25:** Enable extended error reception.
- [ ] **UDP-26:** Set do-not-fragment behavior.
- [ ] **UDP-27:** Record local destination address and receive ifindex.
- [ ] **UDP-28:** Send a datagram to a caller-selected remote endpoint.

### Receive and error paths

- [ ] **UDP-29:** Implement receive using `recvmsg` semantics.
- [ ] **UDP-30:** Populate source address from the message header.
- [ ] **UDP-31:** Populate local address and ifindex from ancillary data.
- [ ] **UDP-32:** Surface `MSG_TRUNC` as `ReceiveMeta.Truncated`.
- [ ] **UDP-33:** Drop truncated datagrams before wire parsing.
- [ ] **UDP-34:** Read asynchronous error-queue messages.
- [ ] **UDP-35:** Extract the reported MTU when present.
- [ ] **UDP-36:** Extract the quoted peer tuple when present.
- [ ] **UDP-37:** Ignore an error that cannot be mapped to a known socket/path.
- [ ] **UDP-38:** Surface synchronous `EMSGSIZE` without retrying an oversized
  datagram.
- [ ] **UDP-39:** Make close unblock read and error-queue goroutines.
- [ ] **UDP-40:** Make repeated close calls safe.

### Integration and benchmarks

- [ ] **UDP-41:** Send one datagram over path A in the namespace harness.
- [ ] **UDP-42:** Assert path-A packets leave only the path-A interface.
- [ ] **UDP-43:** Send one datagram over path B.
- [ ] **UDP-44:** Assert path-B packets leave only the path-B interface.
- [ ] **UDP-45:** Inject an oversized datagram and assert truncation detection.
- [ ] **UDP-46:** Lower a path MTU and assert `EMSGSIZE` or error-queue feedback.
- [ ] **UDP-47:** Benchmark receive allocations.
- [ ] **UDP-48:** Benchmark send allocations.
- [ ] **UDP-49:** Record baseline packets per second for one socket.

### Gate

- [ ] Interface binding and marking are proven independently for both paths.
- [ ] Oversized/truncated packets cannot be mistaken for valid packets.
- [ ] Closing transport leaves no goroutine blocked.

### Checkpoint

```text
feat: add interface-bound UDP transport
```

## 12. M08 — Policy routing, sysctls, and nftables primitives

### Objective

Make the outer packets deterministic on multi-WAN Linux hosts while preserving
operator-owned networking state.

### Interface discovery

- [ ] **ROUTE-01:** Create `internal/routing/discovery_linux.go`.
- [ ] **ROUTE-02:** Resolve configured interface names to ifindices.
- [ ] **ROUTE-03:** List usable IPv4 addresses for each path interface.
- [ ] **ROUTE-04:** Reject tentative, duplicate, multicast, and link-local source
  addresses unless explicitly supported by a later design revision.
- [ ] **ROUTE-05:** Select the configured source address when more than one is
  usable.
- [ ] **ROUTE-06:** Discover the interface's connected prefixes.
- [ ] **ROUTE-07:** Discover the effective IPv4 gateway.
- [ ] **ROUTE-08:** Discover the interface's effective MTU.
- [ ] **ROUTE-09:** Reject a path whose physical MTU is below the 1200-byte base
  outer requirement.
- [ ] **ROUTE-10:** Snapshot discovery results before mutation.

### Policy tables and rules

- [ ] **ROUTE-11:** Create `internal/routing/manager_linux.go`.
- [ ] **ROUTE-12:** Represent desired routes and rules as typed values.
- [ ] **ROUTE-13:** Detect a route-table collision before applying changes.
- [ ] **ROUTE-14:** Detect a rule-priority collision before applying changes.
- [ ] **ROUTE-15:** Create path-A routing table with its connected route.
- [ ] **ROUTE-16:** Add path-A default route through its gateway.
- [ ] **ROUTE-17:** Add path-A fwmark rule.
- [ ] **ROUTE-18:** Create path-B routing table with its connected route.
- [ ] **ROUTE-19:** Add path-B default route through its gateway.
- [ ] **ROUTE-20:** Add path-B fwmark rule.
- [ ] **ROUTE-21:** Create the tunnel routing table.
- [ ] **ROUTE-22:** Add the tunnel-table default route through `red0` only after
  session readiness.
- [ ] **ROUTE-23:** Add narrowly matched IPv4 DHCP client exemptions.
- [ ] **ROUTE-24:** Add connected-LAN exemptions only when `allow_lan` is true.
- [ ] **ROUTE-25:** Verify RED_MPUDP never replaces the main-table default route.
- [ ] **ROUTE-26:** Read back every installed rule and route.
- [ ] **ROUTE-27:** Make a second apply operation idempotent.
- [ ] **ROUTE-28:** Remove only exact instance-owned rules and routes.
- [ ] **ROUTE-29:** Make a second remove operation idempotent.
- [ ] **ROUTE-30:** Roll back partially applied state in reverse order.

### Reverse-path filtering and marks

- [ ] **SYSCTL-01:** Read per-path `rp_filter` values.
- [ ] **SYSCTL-02:** Record prior values in the mutation journal.
- [ ] **SYSCTL-03:** Change strict mode 1 to loose mode 2 only when required.
- [ ] **SYSCTL-04:** Read `net.ipv4.conf.all.src_valid_mark`.
- [ ] **SYSCTL-05:** Enable `src_valid_mark` when required.
- [ ] **SYSCTL-06:** Verify the effective values after writing.
- [ ] **SYSCTL-07:** Abort tunnel-route activation when a required write fails.
- [ ] **SYSCTL-08:** Restore values conditionally through the journal.

### Link and route watcher

- [ ] **WATCH-01:** Subscribe to link updates.
- [ ] **WATCH-02:** Subscribe to IPv4 address updates.
- [ ] **WATCH-03:** Subscribe to route updates.
- [ ] **WATCH-04:** Convert each kernel update into an immutable typed event.
- [ ] **WATCH-05:** Coalesce duplicate events for the same interface generation.
- [ ] **WATCH-06:** Detect interface deletion.
- [ ] **WATCH-07:** Detect source-address replacement.
- [ ] **WATCH-08:** Detect gateway replacement.
- [ ] **WATCH-09:** Detect physical MTU reduction.
- [ ] **WATCH-10:** Ensure the watcher never mutates path/session state directly.
- [ ] **WATCH-11:** Make watcher shutdown unblock every subscription goroutine.

### nftables manager

- [ ] **NFT-01:** Create `internal/firewall/manager_linux.go`.
- [ ] **NFT-02:** Derive a bounded stable table name from role and instance ID.
- [ ] **NFT-03:** Reject a table name outside nftables identifier limits.
- [ ] **NFT-04:** Build nftables changes as one transaction.
- [ ] **NFT-05:** Detect an existing table not owned by this instance.
- [ ] **NFT-06:** Refuse to replace a foreign table with the same name.
- [ ] **NFT-07:** Reconcile an exact stale table owned by this instance.
- [ ] **NFT-08:** Remove only the exact owned table.
- [ ] **NFT-09:** Make table installation idempotent.
- [ ] **NFT-10:** Make table removal idempotent.

### Server forwarding/NAT primitives

- [ ] **NAT-01:** Read `net.ipv4.ip_forward`.
- [ ] **NAT-02:** Journal the prior forwarding value.
- [ ] **NAT-03:** Enable forwarding only when `manage_nat` requires it.
- [ ] **NAT-04:** Create a forward rule from the configured TUN subnet to the
  configured WAN interface.
- [ ] **NAT-05:** Create an established/related return rule.
- [ ] **NAT-06:** Create a masquerade rule scoped to the TUN subnet and WAN
  interface.
- [ ] **NAT-07:** Reject an empty or nonexistent WAN interface.
- [ ] **NAT-08:** Verify unrelated nftables tables remain byte-for-byte
  unchanged in the namespace test.
- [ ] **NAT-09:** Restore forwarding conditionally on cleanup.

### Integration

- [ ] **ROUTE-31:** Verify marked path-A traffic uses only path A after a tunnel
  default is installed.
- [ ] **ROUTE-32:** Verify marked path-B traffic uses only path B.
- [ ] **ROUTE-33:** Verify unmarked application traffic selects `red0`.
- [ ] **ROUTE-34:** Verify outer UDP never recurses into `red0`.
- [ ] **ROUTE-35:** Verify DHCP exemption routing without granting general LAN
  bypass.
- [ ] **ROUTE-36:** Verify enabled LAN bypass uses the main table.
- [ ] **ROUTE-37:** Verify disabled LAN bypass cannot escape through the main
  table once the kill switch exists.
- [ ] **ROUTE-38:** Change path-A gateway and verify one typed watcher event.
- [ ] **ROUTE-39:** Force an apply failure halfway through and verify rollback.

### Gate

- [ ] Two marked sockets keep deterministic egress after the TUN route exists.
- [ ] No unrelated host networking object changes.
- [ ] All mutation paths are journaled and recoverable.

### Checkpoint

```text
feat: add recoverable Linux policy routing and NAT primitives
```

## 13. M09 — Exact wire codecs and protocol vectors

### Objective

Implement allocation-bounded parsing and exact binary encodings before session
logic depends on them.

### Constants and errors

- [ ] **WIRE-01:** Create `internal/wire/constants.go`.
- [ ] **WIRE-02:** Define magic bytes `RMPU`.
- [ ] **WIRE-03:** Define handshake version byte `0x01`.
- [ ] **WIRE-04:** Define transport version nibble `1`.
- [ ] **WIRE-05:** Define handshake type values INIT 1, RESPONSE 2, RETRY 3.
- [ ] **WIRE-06:** Define transport type values DATA 0 through CLOSE 8 exactly
  as specified.
- [ ] **WIRE-07:** Define handshake maximum Noise-message length 512.
- [ ] **WIRE-08:** Define fixed transport-header length 44.
- [ ] **WIRE-09:** Define AEAD-tag length 16.
- [ ] **WIRE-10:** Define transport overhead 60.
- [ ] **WIRE-11:** Define total IPv4/UDP encapsulation overhead 88.
- [ ] **WIRE-12:** Add compile-time or test assertions for every constant
  relationship.
- [ ] **WIRE-13:** Create stable parse-error categories without embedding packet
  bytes.

### Handshake envelope

- [ ] **WIRE-14:** Create `internal/wire/handshake.go`.
- [ ] **WIRE-15:** Define the fixed handshake prefix fields.
- [ ] **WIRE-16:** Encode the fixed prefix in big-endian order.
- [ ] **WIRE-17:** Append the bounded Noise message without extra capacity
  growth.
- [ ] **WIRE-18:** Append the 16-byte preauthentication tag.
- [ ] **WIRE-19:** Reject packets shorter than the minimum envelope.
- [ ] **WIRE-20:** Reject wrong magic.
- [ ] **WIRE-21:** Reject wrong handshake version.
- [ ] **WIRE-22:** Reject unknown handshake type.
- [ ] **WIRE-23:** Reject nonzero handshake flags.
- [ ] **WIRE-24:** Reject declared Noise length above 512.
- [ ] **WIRE-25:** Reject declared Noise length beyond received bytes.
- [ ] **WIRE-26:** Reject trailing bytes after the tag.
- [ ] **WIRE-27:** Accept either an all-zero or nonzero 16-byte cookie field on
  INIT so the same codec can represent first and retried attempts.
- [ ] **WIRE-28:** Enforce zero cookie on RESPONSE.
- [ ] **WIRE-29:** Enforce nonzero cookie and zero Noise length on RETRY.
- [ ] **WIRE-29A:** Leave the decision to require a zero or valid nonzero INIT
  cookie to the handshake state machine, where source address and retry state
  are available.

### Noise payload codecs

- [ ] **WIRE-30:** Create `internal/wire/noise_payload.go`.
- [ ] **WIRE-31:** Encode OPEN at its exact fixed length.
- [ ] **WIRE-32:** Decode OPEN and reject trailing bytes.
- [ ] **WIRE-33:** Encode JOIN at its exact fixed length.
- [ ] **WIRE-34:** Decode JOIN and reject trailing bytes.
- [ ] **WIRE-35:** Enforce zero `replaces_path_token` for a new path.
- [ ] **WIRE-36:** Encode OPEN_ACK at its exact fixed length.
- [ ] **WIRE-37:** Decode OPEN_ACK and reject trailing bytes.
- [ ] **WIRE-38:** Encode JOIN_ACK at its exact fixed length.
- [ ] **WIRE-39:** Decode JOIN_ACK and reject trailing bytes.
- [ ] **WIRE-40:** Enforce zero fields after a nonzero ACK result.
- [ ] **WIRE-41:** Reject unknown operation values.
- [ ] **WIRE-42:** Reject unknown ACK result values.
- [ ] **WIRE-43:** Enforce zero v1 feature fields.
- [ ] **WIRE-44:** Enforce `base_outer_mtu == 1200` in v1 ACKs.

### Transport header

- [ ] **WIRE-45:** Create `internal/wire/transport.go`.
- [ ] **WIRE-46:** Encode the combined version/type byte.
- [ ] **WIRE-47:** Encode all fixed header fields in big-endian order.
- [ ] **WIRE-48:** Return the exact 44-byte header slice for AEAD AAD.
- [ ] **WIRE-49:** Parse fields without allocating.
- [ ] **WIRE-50:** Reject packets shorter than header plus AEAD tag.
- [ ] **WIRE-51:** Reject wrong transport version.
- [ ] **WIRE-52:** Reject reserved transport types 9 through 15.
- [ ] **WIRE-53:** Reject nonzero transport flags.
- [ ] **WIRE-54:** Reject header length other than 44.
- [ ] **WIRE-55:** Reject zero session ID.
- [ ] **WIRE-56:** Reject zero path token.
- [ ] **WIRE-57:** Enforce nonzero logical sequence for DATA.
- [ ] **WIRE-58:** Enforce nonzero path sequence for DATA.
- [ ] **WIRE-59:** Enforce zero path sequence for control packets.
- [ ] **WIRE-60:** Enforce control-specific logical-sequence rules.

### Control payloads

- [ ] **WIRE-61:** Create `internal/wire/control.go`.
- [ ] **WIRE-62:** Encode PATH_REPORT at exactly 88 bytes.
- [ ] **WIRE-63:** Decode PATH_REPORT at exactly 88 bytes.
- [ ] **WIRE-64:** Implement the specified little-bit-numbering rule inside the
  32-byte received bitmap.
- [ ] **WIRE-65:** Reject a PATH_REPORT with unknown report flags.
- [ ] **WIRE-66:** Reject a PATH_REPORT with zero reported path token.
- [ ] **WIRE-67:** Encode PMTU_PROBE to an exact requested outer size.
- [ ] **WIRE-68:** Reject PMTU_PROBE requested sizes below 1200.
- [ ] **WIRE-69:** Reject PMTU_PROBE when the size field disagrees with received
  UDP payload length plus 28.
- [ ] **WIRE-70:** Encode PMTU_ACK at exactly 2 bytes.
- [ ] **WIRE-71:** Decode PMTU_ACK at exactly 2 bytes.
- [ ] **WIRE-72:** Encode CLOSE at exactly 4 bytes.
- [ ] **WIRE-73:** Decode CLOSE at exactly 4 bytes.
- [ ] **WIRE-74:** Reject unknown CLOSE scope.
- [ ] **WIRE-75:** Reject unknown CLOSE reason.
- [ ] **WIRE-76:** Enforce empty plaintext for PING.
- [ ] **WIRE-77:** Enforce empty plaintext for PONG.
- [ ] **WIRE-78:** Enforce empty plaintext for PATH_CHALLENGE.
- [ ] **WIRE-79:** Enforce empty plaintext for PATH_RESPONSE.

### Authentication helpers and vectors

- [ ] **WIRE-80:** Compute handshake HMAC over every envelope byte preceding the
  tag.
- [ ] **WIRE-81:** Truncate the HMAC to exactly 16 bytes.
- [ ] **WIRE-82:** Verify tags with constant-time comparison.
- [ ] **WIRE-83:** Reject a tag before allocating Noise handshake state.
- [ ] **WIRE-84:** Add handshake-envelope golden vectors.
- [ ] **WIRE-85:** Add OPEN/JOIN/ACK golden vectors.
- [ ] **WIRE-86:** Add transport-header golden vectors.
- [ ] **WIRE-87:** Add every fixed control-payload golden vector.
- [ ] **WIRE-88:** Add representative ciphertext/AAD golden vectors.
- [ ] **WIRE-89:** Store vector generation inputs in a non-production test-only
  package.

### Fuzzing and allocation checks

- [ ] **WIRE-90:** Fuzz handshake-envelope parsing.
- [ ] **WIRE-91:** Fuzz Noise-payload parsing.
- [ ] **WIRE-92:** Fuzz transport-header parsing.
- [ ] **WIRE-93:** Fuzz each control payload parser.
- [ ] **WIRE-94:** Seed fuzzers with all golden vectors.
- [ ] **WIRE-95:** Assert malformed packets allocate within a fixed budget.
- [ ] **WIRE-96:** Assert an unknown session packet requires no payload
  allocation in the later dispatch benchmark.

### Gate

- [ ] Every wire structure has an exact encoded-length assertion.
- [ ] Every parser rejects truncation, trailing bytes, and reserved values.
- [ ] Golden, fuzz-smoke, unit, and race tests pass.

### Checkpoint

```text
feat: implement exact RED_MPUDP v1 wire codecs
```

## 14. M10 — Replay, deduplication, race tracking, and IPv4 validation

### Objective

Build pure state machines for packet acceptance before adding concurrent path
actors.

### Outer replay window

- [ ] **REPLAY-01:** Create `internal/replay/window.go`.
- [ ] **REPLAY-02:** Use a fixed 4096-bit bitmap.
- [ ] **REPLAY-03:** Represent an empty window without treating packet zero as a
  replay.
- [ ] **REPLAY-04:** Add a non-mutating precheck operation.
- [ ] **REPLAY-05:** Return candidate-new for a number above the current highest.
- [ ] **REPLAY-06:** Return candidate-reordered inside the window.
- [ ] **REPLAY-07:** Return duplicate for a set bitmap position.
- [ ] **REPLAY-08:** Return too-old outside the window.
- [ ] **REPLAY-09:** Add a separate commit operation.
- [ ] **REPLAY-10:** Advance and clear bitmap words correctly on a small jump.
- [ ] **REPLAY-11:** Reset the bitmap correctly on a jump of at least 4096.
- [ ] **REPLAY-12:** Commit reordered numbers without changing the highest.
- [ ] **REPLAY-13:** Prove a rejected authentication path never calls commit.
- [ ] **REPLAY-14:** Test boundaries 0, 1, 4095, 4096, and MaxUint64.
- [ ] **REPLAY-15:** Fuzz the window against a simple set-based reference model.

### Global DATA dedup window

- [ ] **DEDUP-01:** Create `internal/dedup/window.go`.
- [ ] **DEDUP-02:** Allocate the configured power-of-two bitmap once.
- [ ] **DEDUP-03:** Represent an empty DATA window.
- [ ] **DEDUP-04:** Accept the first logical sequence.
- [ ] **DEDUP-05:** Reject a repeated logical sequence as duplicate.
- [ ] **DEDUP-06:** Accept reordered data within the window exactly once.
- [ ] **DEDUP-07:** Reject a sequence older than the window.
- [ ] **DEDUP-08:** Clear overwritten bits correctly on wraparound of the ring
  index without wrapping the sequence number.
- [ ] **DEDUP-09:** Reset efficiently on a jump larger than the window.
- [ ] **DEDUP-10:** Reject MaxUint64 exhaustion and request session replacement.
- [ ] **DEDUP-11:** Test window sizes 4096, 65536, and 1048576.
- [ ] **DEDUP-12:** Calculate memory before session admission.
- [ ] **DEDUP-13:** Refuse session admission above configured memory limits.
- [ ] **DEDUP-14:** Fuzz against a simple set-based reference model.

### Race metadata

- [ ] **RACE-01:** Create `internal/dedup/race.go`.
- [ ] **RACE-02:** Store first-arrival path token per logical sequence.
- [ ] **RACE-03:** Store first-arrival monotonic time.
- [ ] **RACE-04:** Store a bounded received-path bitset.
- [ ] **RACE-05:** Record later-copy arrival delta.
- [ ] **RACE-06:** Calculate the observation horizon from current path RTTs.
- [ ] **RACE-07:** Clamp the horizon to 250 ms through 2 seconds.
- [ ] **RACE-08:** Finalize a unique rescue only after the horizon.
- [ ] **RACE-09:** Mark an evicted unfinalized record as observation unknown.
- [ ] **RACE-10:** Never claim an unknown record as a rescue.
- [ ] **RACE-11:** Test late copies before and after finalization.
- [ ] **RACE-12:** Test ring eviction during a large sequence jump.

### Per-path DATA loss tracking

- [ ] **LOSS-01:** Create `internal/path/loss.go`.
- [ ] **LOSS-02:** Track a 4096-entry path-sequence receive window.
- [ ] **LOSS-03:** Mark reordered arrivals without declaring immediate loss.
- [ ] **LOSS-04:** Finalize a gap when it leaves the window.
- [ ] **LOSS-05:** Finalize a gap after `max(3*srtt, 250ms)`.
- [ ] **LOSS-06:** Count an authenticated DATA copy before global dedup.
- [ ] **LOSS-07:** Count complete outer bytes consistently.
- [ ] **LOSS-08:** Produce the most recent 256-bit report bitmap.
- [ ] **LOSS-09:** Test recovery of a gap before finalization.
- [ ] **LOSS-10:** Test finalized loss counters remain monotonic.

### IPv4 parser and policy

- [ ] **IPV4-01:** Create `internal/packet/ipv4.go`.
- [ ] **IPV4-02:** Reject fewer than 20 bytes.
- [ ] **IPV4-03:** Reject version other than 4.
- [ ] **IPV4-04:** Reject IHL below 5.
- [ ] **IPV4-05:** Reject IHL beyond the received payload.
- [ ] **IPV4-06:** Require total length to equal the DATA plaintext length.
- [ ] **IPV4-07:** Validate the IPv4 header checksum.
- [ ] **IPV4-08:** Validate fragment offset and flag consistency.
- [ ] **IPV4-09:** Expose source and destination as `netip.Addr`.
- [ ] **IPV4-10:** Expose protocol and total length without heap allocation.
- [ ] **IPV4-11:** Identify non-initial fragments safely.
- [ ] **IPV4-12:** Reject multicast, broadcast, unspecified, and invalid source
  addresses from client peers.
- [ ] **IPV4-13:** Require server-uplink source to equal the peer's assigned
  tunnel address.
- [ ] **IPV4-14:** Reject server-uplink destinations inside the tunnel subnet.
- [ ] **IPV4-15:** Require client-downlink destination to equal the client's
  assigned tunnel address.
- [ ] **IPV4-16:** Fuzz the parser independently.
- [ ] **IPV4-17:** Seed the fuzzer with valid ICMP, UDP, TCP, options, and
  fragmented packets.

### Gate

- [ ] Authentication-failure tests prove replay and dedup state stays unchanged.
- [ ] Dedup and replay fuzz models agree for the full smoke duration.
- [ ] IPv4 validation rejects spoofed peer traffic before TUN injection.

### Checkpoint

```text
feat: add replay dedup race and IPv4 state machines
```

## 15. M11 — Authenticated OPEN/JOIN handshake

### Objective

Create bounded authenticated sessions and independent cipher states for every
path incarnation.

### Peer registry

- [ ] **HS-01:** Create `internal/session/peers.go`.
- [ ] **HS-02:** Load each configured peer's complete static public key.
- [ ] **HS-03:** Load each peer's distinct PSK.
- [ ] **HS-04:** Derive each peer's lookup ID.
- [ ] **HS-05:** Reject duplicate peer IDs even when full keys differ.
- [ ] **HS-06:** Map full authenticated static keys back to configured peers.
- [ ] **HS-07:** Keep peer maps immutable after successful startup.
- [ ] **HS-08:** Expose no PSK or derived key through formatting methods.

### Handshake ingress ordering

- [ ] **HS-09:** Create `internal/noisehandshake/server.go`.
- [ ] **HS-10:** Parse only the bounded handshake envelope before peer lookup.
- [ ] **HS-11:** Drop an unknown peer ID without responding.
- [ ] **HS-12:** Derive the peer's preauthentication key after lookup.
- [ ] **HS-13:** Verify the preauthentication tag before Noise allocation or DH.
- [ ] **HS-14:** Drop an invalid tag without responding.
- [ ] **HS-15:** Apply a global handshake-work token bucket.
- [ ] **HS-16:** Apply an IPv4 source-prefix handshake-work token bucket.
- [ ] **HS-17:** Put accepted work into a bounded worker queue.
- [ ] **HS-18:** Drop new work predictably when the worker queue is full.
- [ ] **HS-19:** Bound concurrent handshake workers.
- [ ] **HS-20:** Bound total pending handshakes.
- [ ] **HS-21:** Bound pending handshakes per source prefix.
- [ ] **HS-22:** Expire pending state after five seconds using the injected clock.

### Stateless retry cookie

- [ ] **COOKIE-01:** Create `internal/noisehandshake/cookie.go`.
- [ ] **COOKIE-02:** Generate a random server cookie secret at startup.
- [ ] **COOKIE-03:** Rotate the secret every two minutes using monotonic time.
- [ ] **COOKIE-04:** Retain exactly the immediately previous secret.
- [ ] **COOKIE-05:** Hash invariant INIT fields and the Noise message while
  excluding cookie and preauthentication-tag fields.
- [ ] **COOKIE-06:** Include source IPv4 address and UDP port in cookie input.
- [ ] **COOKIE-07:** Include peer ID and handshake ID in cookie input.
- [ ] **COOKIE-08:** Include the invariant INIT hash in cookie input.
- [ ] **COOKIE-09:** Truncate cookie HMAC to 16 bytes.
- [ ] **COOKIE-10:** Verify cookies in constant time.
- [ ] **COOKIE-11:** Accept a cookie made with the current secret.
- [ ] **COOKIE-12:** Accept a cookie made with the previous secret.
- [ ] **COOKIE-13:** Reject older cookies.
- [ ] **COOKIE-14:** Reject a cookie after source-address change.
- [ ] **COOKIE-15:** Reject a cookie after source-port change.
- [ ] **COOKIE-16:** Reject a cookie attached to different INIT bytes.
- [ ] **COOKIE-17:** Generate a RETRY no larger than the received INIT.
- [ ] **COOKIE-18:** Authenticate RETRY with the peer preauthentication key.
- [ ] **COOKIE-19:** Require retry by default in production server mode.
- [ ] **COOKIE-20:** Keep no server handshake state when sending RETRY.
- [ ] **COOKIE-21:** Treat an all-zero INIT cookie as absent.
- [ ] **COOKIE-22:** Send RETRY for an absent cookie when retry is required.
- [ ] **COOKIE-23:** Verify every nonzero INIT cookie before allocating Noise or
  pending-handshake state.
- [ ] **COOKIE-24:** Drop an invalid nonzero cookie without responding.
- [ ] **COOKIE-25:** Continue to Noise processing only after a required cookie
  validates.

### Completed-handshake cache and nonce replay

- [ ] **CACHE-01:** Create `internal/noisehandshake/cache.go`.
- [ ] **CACHE-02:** Key completed responses by peer ID, handshake ID, and exact
  INIT hash.
- [ ] **CACHE-03:** Cache the exact response bytes for five seconds.
- [ ] **CACHE-04:** Return identical response bytes for an identical retransmit.
- [ ] **CACHE-05:** Reject reuse of the same peer/handshake ID with different
  INIT bytes.
- [ ] **CACHE-06:** Expire cached response bytes deterministically.
- [ ] **CACHE-07:** Bound cache entries globally.
- [ ] **CACHE-08:** Bound cache entries per peer.
- [ ] **CACHE-09:** Create the 4096-entry per-peer OPEN nonce cache.
- [ ] **CACHE-10:** Retain OPEN nonces for 24 hours.
- [ ] **CACHE-11:** Reject repeated client session nonces after decryption.
- [ ] **CACHE-12:** Process an exact completed-cache hit before nonce-replay
  rejection.
- [ ] **CACHE-13:** Reject repeated client path nonces for a session's lifetime.

### Server OPEN

- [ ] **OPEN-01:** Parse and authenticate the Noise IKpsk2 initiator message.
- [ ] **OPEN-02:** Verify the full authenticated client static key matches the
  peer selected by peer ID.
- [ ] **OPEN-03:** Decode OPEN only after Noise authentication succeeds.
- [ ] **OPEN-04:** Validate requested TUN MTU.
- [ ] **OPEN-05:** Select TUN MTU no higher than client or server limits.
- [ ] **OPEN-06:** Select probe interval as the greater configured/requested
  value within 100 ms through 5 seconds.
- [ ] **OPEN-07:** Generate a nonzero random session ID.
- [ ] **OPEN-08:** Collision-check the session ID against active and provisional
  sessions.
- [ ] **OPEN-09:** Generate a random 32-byte join token.
- [ ] **OPEN-10:** Generate a nonzero random path token.
- [ ] **OPEN-11:** Collision-check the path token within the new session.
- [ ] **OPEN-12:** Obtain the server-assigned client tunnel address from peer
  configuration.
- [ ] **OPEN-13:** Reject OPEN when global session capacity is exhausted.
- [ ] **OPEN-14:** Reject OPEN when peer session capacity is exhausted and one
  provisional replacement already exists.
- [ ] **OPEN-15:** Permit exactly one provisional replacement session per peer.
- [ ] **OPEN-16:** Prevent provisional sessions from routing DATA.
- [ ] **OPEN-17:** Expire an unvalidated provisional session after ten seconds.
- [ ] **OPEN-18:** Encode OPEN_ACK inside the Noise responder message.
- [ ] **OPEN-19:** Put 1200 in `base_outer_mtu`.
- [ ] **OPEN-20:** Authenticate the RESPONSE envelope with the preauthentication
  key.
- [ ] **OPEN-21:** Ensure RESPONSE bytes do not exceed amplification limits.
- [ ] **OPEN-22:** Install initiator-to-responder receive cipher state on the
  server path.
- [ ] **OPEN-23:** Install responder-to-initiator send cipher state on the server
  path.

### Client OPEN

- [ ] **CLIENT-HS-01:** Create `internal/noisehandshake/client.go`.
- [ ] **CLIENT-HS-02:** Generate a random nonzero handshake ID.
- [ ] **CLIENT-HS-03:** Generate a random 128-bit client session nonce.
- [ ] **CLIENT-HS-04:** Build one Noise initiator message and retain its exact
  bytes for retransmission.
- [ ] **CLIENT-HS-05:** Send the initial INIT with a zero cookie.
- [ ] **CLIENT-HS-06:** Verify RETRY peer ID, handshake ID, shape, and
  preauthentication tag.
- [ ] **CLIENT-HS-07:** Copy an accepted retry cookie into INIT.
- [ ] **CLIENT-HS-08:** Recompute the INIT preauthentication tag after adding the
  cookie.
- [ ] **CLIENT-HS-09:** Do not regenerate the Noise message after RETRY.
- [ ] **CLIENT-HS-10:** Start retransmission at 250 ms with deterministic jitter
  injection for tests.
- [ ] **CLIENT-HS-11:** Double retransmission delay up to two seconds.
- [ ] **CLIENT-HS-12:** Stop after five attempts.
- [ ] **CLIENT-HS-13:** Keep one overall attempt deadline that RETRY cannot
  extend.
- [ ] **CLIENT-HS-14:** Verify RESPONSE preauthentication before Noise response
  processing.
- [ ] **CLIENT-HS-15:** Pin the configured server static public key through the
  Noise pattern.
- [ ] **CLIENT-HS-16:** Decode and validate OPEN_ACK.
- [ ] **CLIENT-HS-17:** Reject a session ID or path token of zero.
- [ ] **CLIENT-HS-18:** Reject an assigned tunnel address outside the expected
  configuration policy.
- [ ] **CLIENT-HS-19:** Install initiator-to-responder send cipher state.
- [ ] **CLIENT-HS-20:** Install responder-to-initiator receive cipher state.
- [ ] **CLIENT-HS-21:** Erase handshake-only state on completion or failure as
  far as the Go runtime permits.

### JOIN and replacement

- [ ] **JOIN-01:** Generate a new handshake ID and ephemeral key for every JOIN.
- [ ] **JOIN-02:** Generate a random 128-bit client path nonce.
- [ ] **JOIN-03:** Put current session ID and join token inside encrypted JOIN.
- [ ] **JOIN-04:** Use zero replacement token when adding below the four-path
  cap.
- [ ] **JOIN-05:** Use the old path token when replacing an incarnation.
- [ ] **JOIN-06:** Authenticate the full client static key again through Noise.
- [ ] **JOIN-07:** Look up the referenced logical session.
- [ ] **JOIN-08:** Verify the authenticated peer owns the session.
- [ ] **JOIN-09:** Compare the join token in constant time.
- [ ] **JOIN-10:** Reject a repeated client path nonce.
- [ ] **JOIN-11:** Enforce at most four scheduler-eligible paths.
- [ ] **JOIN-12:** Permit one validating replacement over the cap only when its
  old token belongs to that session.
- [ ] **JOIN-13:** Generate and collision-check a new nonzero path token.
- [ ] **JOIN-14:** Encode JOIN_ACK.
- [ ] **JOIN-15:** Install new independent send/receive cipher states.
- [ ] **JOIN-16:** Assert no derived transport key matches another incarnation.
- [ ] **JOIN-17:** Keep the new path in VALIDATING until later liveness/PMTU
  checks succeed.

### Tests

- [ ] **HS-TEST-01:** Complete OPEN over the in-memory lossy transport.
- [ ] **HS-TEST-02:** Drop the first RETRY and verify retransmission.
- [ ] **HS-TEST-03:** Drop the first RESPONSE and verify byte-identical cached
  response replay.
- [ ] **HS-TEST-04:** Reorder duplicate INIT packets without advancing Noise
  state twice.
- [ ] **HS-TEST-05:** Flood invalid peer IDs and assert bounded allocations.
- [ ] **HS-TEST-06:** Flood invalid preauthentication tags and assert no DH work.
- [ ] **HS-TEST-07:** Exhaust global pending capacity and verify BUSY behavior
  only for authenticated peers.
- [ ] **HS-TEST-08:** Replay a completed OPEN nonce outside the response-cache
  window and verify rejection.
- [ ] **HS-TEST-09:** Complete JOIN over path B.
- [ ] **HS-TEST-10:** Attempt JOIN with another peer's session ID.
- [ ] **HS-TEST-11:** Attempt JOIN with a wrong join token.
- [ ] **HS-TEST-12:** Attempt a fifth non-replacement path.
- [ ] **HS-TEST-13:** Complete a valid replacement at the four-path cap.
- [ ] **HS-TEST-14:** Run OPEN/JOIN state tests under the race detector.

### Gate

- [ ] OPEN and JOIN survive loss, duplication, and reordering.
- [ ] Invalid unauthenticated traffic cannot allocate durable session/path
  state.
- [ ] Each path incarnation has unique directional keys and fresh nonce zero.

### Checkpoint

```text
feat: implement bounded Noise OPEN and JOIN handshakes
```

## 16. M12 — Single-path encrypted path actor

### Objective

Create the sole owner of one path's socket, cipher states, counters, replay
window, and bounded output queues.

### Actor model

- [ ] **PATH-01:** Create `internal/path/state.go` with NEW, HANDSHAKING,
  VALIDATING, HEALTHY, DEGRADED, DRAINING, DEAD, and CLOSED states.
- [ ] **PATH-02:** Define allowed state transitions as data, not scattered
  conditionals.
- [ ] **PATH-03:** Test every allowed transition.
- [ ] **PATH-04:** Reject every disallowed transition.
- [ ] **PATH-05:** Create `internal/path/actor.go`.
- [ ] **PATH-06:** Give the actor exclusive ownership of its DatagramIO.
- [ ] **PATH-07:** Give the actor exclusive ownership of both Noise cipher
  states.
- [ ] **PATH-08:** Give the actor exclusive ownership of outer send counter.
- [ ] **PATH-09:** Give the actor exclusive ownership of outer replay state.
- [ ] **PATH-10:** Give the actor exclusive ownership of path DATA send counter.
- [ ] **PATH-11:** Give the actor exclusive ownership of PMTU and health state.
- [ ] **PATH-12:** Accept commands through a bounded queue.
- [ ] **PATH-13:** Accept timer events through a separate bounded control queue.
- [ ] **PATH-14:** Publish immutable snapshots.
- [ ] **PATH-15:** Never expose mutable actor-owned objects in a snapshot.
- [ ] **PATH-16:** Shut down all actor goroutines from one cancellation path.

### Initial queues and safety pacer

- [ ] **PATH-17:** Create a bounded control queue.
- [ ] **PATH-18:** Create a bounded DATA queue.
- [ ] **PATH-19:** Reject queue input after DRAINING begins.
- [ ] **PATH-20:** Attach a monotonic enqueue deadline to every item.
- [ ] **PATH-21:** Drop expired DATA before encryption.
- [ ] **PATH-22:** Return dropped buffers exactly once.
- [ ] **PATH-23:** Add the temporary fixed 1 Mbit/s DATA pacer.
- [ ] **PATH-24:** Charge the pacer using complete outer packet bytes.
- [ ] **PATH-25:** Pace PMTU probes through the same temporary DATA budget.
- [ ] **PATH-26:** Rate-limit control output with a separate fixed initial
  budget.
- [ ] **PATH-27:** Prove no actor send path bypasses one of those budgets.

### DATA send path

- [ ] **PATH-28:** Accept an immutable logical sequence and owned inner-packet
  buffer from the session engine.
- [ ] **PATH-29:** Assign path sequence only when dequeuing for transmission.
- [ ] **PATH-30:** Assign outer packet number only when dequeuing for
  transmission.
- [ ] **PATH-31:** Start the first outer packet number at zero.
- [ ] **PATH-32:** Start the first path DATA sequence at one.
- [ ] **PATH-33:** Reject transmission before counter wraparound.
- [ ] **PATH-34:** Encode the 44-byte header.
- [ ] **PATH-35:** Set the send cipher nonce to the outer packet number.
- [ ] **PATH-36:** Seal with the complete header as AAD.
- [ ] **PATH-37:** Send exactly one resulting UDP datagram.
- [ ] **PATH-38:** Retry a temporary socket-block condition with the same sealed
  bytes and nonce only until the item deadline.
- [ ] **PATH-39:** Never reseal different plaintext under a consumed nonce.
- [ ] **PATH-40:** Count a permanent send failure separately from a local queue
  drop.
- [ ] **PATH-41:** Return plaintext and ciphertext buffers exactly once.

### Receive path and authentication ordering

- [ ] **PATH-42:** Reject truncated receive metadata before parsing.
- [ ] **PATH-43:** Perform fixed shape/version/type/length checks.
- [ ] **PATH-44:** Look up session and path without state mutation.
- [ ] **PATH-45:** Run non-mutating outer replay precheck.
- [ ] **PATH-46:** Set receive cipher nonce from the parsed outer packet number.
- [ ] **PATH-47:** Open ciphertext with the complete header as AAD.
- [ ] **PATH-48:** Return immediately on authentication failure.
- [ ] **PATH-49:** Assert authentication failure leaves replay state unchanged.
- [ ] **PATH-50:** Commit outer replay only after successful authentication.
- [ ] **PATH-51:** Refresh receive liveness only after replay commit.
- [ ] **PATH-52:** Publish authenticated DATA to the session engine.
- [ ] **PATH-53:** Publish authenticated control packets to typed handlers.
- [ ] **PATH-54:** Drop unknown or closed path tokens before payload allocation.

### Tests

- [ ] **PATH-TEST-01:** Exchange encrypted DATA over the in-memory DatagramIO.
- [ ] **PATH-TEST-02:** Exchange encrypted DATA over namespace path A.
- [ ] **PATH-TEST-03:** Deliver outer packets out of order inside the replay
  window.
- [ ] **PATH-TEST-04:** Replay an accepted outer packet.
- [ ] **PATH-TEST-05:** Forge a high outer number with an invalid tag and verify
  no replay-window advance.
- [ ] **PATH-TEST-06:** Tamper each header field and verify authentication
  failure.
- [ ] **PATH-TEST-07:** Fill the command queue and verify bounded failure.
- [ ] **PATH-TEST-08:** Cancel while socket receive is blocked.
- [ ] **PATH-TEST-09:** Cancel while pacing is delayed.
- [ ] **PATH-TEST-10:** Run all actor tests under the race detector.

### Gate

- [ ] One path exchanges authenticated DATA without unbounded queues,
  concurrent cipher use, nonce reuse, or preauthentication state mutation.

### Checkpoint

```text
feat: add single-owner encrypted path actor
```

## 17. M13 — Single-path VPN packet forwarding

### Objective

Connect client/server TUN devices through one encrypted path and the exit
server's forwarding/NAT path.

### Client session engine

- [ ] **SESSION-01:** Create `internal/session/client.go`.
- [ ] **SESSION-02:** Implement DISCONNECTED, OPENING, ACTIVE, DRAINING, FAILED,
  and CLOSED session states.
- [ ] **SESSION-03:** Test every allowed session transition.
- [ ] **SESSION-04:** Reject every disallowed session transition.
- [ ] **SESSION-05:** Own the uplink global DATA sequence in the session engine.
- [ ] **SESSION-06:** Start uplink DATA sequence at one.
- [ ] **SESSION-07:** Refuse sequence wraparound.
- [ ] **SESSION-08:** Keep the default route absent while OPENING.
- [ ] **SESSION-09:** Attach the first authenticated path in VALIDATING state.
- [ ] **SESSION-10:** Activate only after that path passes the current basic
  validation gate.
- [ ] **SESSION-11:** Read owned packets from the TUN ingress queue.
- [ ] **SESSION-12:** Validate locally read IPv4 packet shape.
- [ ] **SESSION-13:** Assign one global sequence per inner packet.
- [ ] **SESSION-14:** Transfer the packet to the single path actor.
- [ ] **SESSION-15:** Apply client downlink dedup before TUN write.
- [ ] **SESSION-16:** Validate downlink destination before TUN write.
- [ ] **SESSION-17:** Send accepted downlink packets through one bounded TUN
  writer queue.

### Server session table and shards

- [ ] **SERVER-01:** Create `internal/session/server.go`.
- [ ] **SERVER-02:** Maintain `session_id -> session shard` lookup.
- [ ] **SERVER-03:** Maintain `assigned_tunnel_ipv4 -> session_id` lookup.
- [ ] **SERVER-04:** Update both maps atomically on activation.
- [ ] **SERVER-05:** Remove both maps atomically on close.
- [ ] **SERVER-06:** Reject duplicate assigned tunnel addresses at runtime.
- [ ] **SERVER-07:** Shard established packet processing by session ID.
- [ ] **SERVER-08:** Give one shard exclusive ownership of each session's dedup
  and downlink sequence state.
- [ ] **SERVER-09:** Bound every shard input queue.
- [ ] **SERVER-10:** Drop unknown-session transport packets before allocation.
- [ ] **SERVER-11:** Own one downlink global DATA sequence per session.
- [ ] **SERVER-12:** Start downlink DATA sequence at one.

### Uplink forwarding

- [ ] **FORWARD-01:** Receive authenticated DATA from the path actor.
- [ ] **FORWARD-02:** Update authenticated path-copy metrics before global
  dedup.
- [ ] **FORWARD-03:** Apply server uplink global dedup.
- [ ] **FORWARD-04:** Drop duplicate or too-old logical sequences.
- [ ] **FORWARD-05:** Validate the complete inner IPv4 header.
- [ ] **FORWARD-06:** Enforce peer source tunnel address.
- [ ] **FORWARD-07:** Reject peer-to-peer tunnel-subnet destinations.
- [ ] **FORWARD-08:** Write the accepted first copy to server `red0`.
- [ ] **FORWARD-09:** Return every dropped or written buffer exactly once.

### Downlink forwarding

- [ ] **FORWARD-10:** Read one packet from server `red0`.
- [ ] **FORWARD-11:** Parse its inner destination without allocation.
- [ ] **FORWARD-12:** Find the session by assigned destination address.
- [ ] **FORWARD-13:** Drop unknown tunnel destinations.
- [ ] **FORWARD-14:** Assign the session's next downlink logical sequence.
- [ ] **FORWARD-15:** Transfer the packet to its single path actor.
- [ ] **FORWARD-16:** Receive/decrypt it on the client.
- [ ] **FORWARD-17:** Validate the assigned client destination.
- [ ] **FORWARD-18:** Write it to client `red0`.

### Session lifecycle

- [ ] **LIFE-01:** Track last fresh authenticated receive time.
- [ ] **LIFE-02:** Expire an idle server session after 120 seconds.
- [ ] **LIFE-03:** Encode advisory path CLOSE.
- [ ] **LIFE-04:** Encode advisory session CLOSE.
- [ ] **LIFE-05:** Treat lost CLOSE as harmless.
- [ ] **LIFE-06:** Remove provisional sessions that never validate.
- [ ] **LIFE-07:** Reopen with exponential backoff after all paths fail.
- [ ] **LIFE-08:** Add deterministic jitter to reconnect backoff tests.
- [ ] **LIFE-09:** Reset global sequences only for a new logical session.

### Namespace end-to-end tests

- [ ] **E2E-01:** Start one server and one path-A client in namespaces.
- [ ] **E2E-02:** Enable server forwarding and scoped NAT.
- [ ] **E2E-03:** Ping the internet namespace through the encrypted tunnel.
- [ ] **E2E-04:** Exchange UDP echo traffic.
- [ ] **E2E-05:** Exchange TCP echo traffic.
- [ ] **E2E-06:** Resolve one test DNS name through a manually routed test
  resolver without enabling production DNS management yet.
- [ ] **E2E-07:** Attempt a spoofed client source address.
- [ ] **E2E-08:** Attempt an unauthorized tunnel-subnet destination.
- [ ] **E2E-09:** Restart the server and verify client session recovery.
- [ ] **E2E-10:** Restart the client and verify stale server session expiry.
- [ ] **E2E-11:** Verify the temporary fixed pacer caps DATA at 1 Mbit/s.

### Gate

- [ ] ICMP, UDP, and TCP cross one authenticated path in both directions.
- [ ] Source spoofing and unknown downlink destinations are dropped.
- [ ] This milestone remains namespace-only and is not packaged as a production
  full-tunnel release.

### Checkpoint

```text
feat: forward single-path encrypted IPv4 traffic
```

## 18. M14 — Multipath replication and global deduplication

### Objective

Race independently sealed copies over two interfaces and deliver only the first
authenticated logical packet.

### Path attachment

- [ ] **MULTI-01:** Open the first logical session over path A.
- [ ] **MULTI-02:** JOIN path B using a fresh Noise handshake.
- [ ] **MULTI-03:** Store both paths in one immutable session path snapshot.
- [ ] **MULTI-04:** Keep each path's socket, cipher states, counters, and replay
  state independent.
- [ ] **MULTI-05:** Reject attaching the same path token twice.
- [ ] **MULTI-06:** Reject more than four active scheduler-eligible paths.
- [ ] **MULTI-07:** Remove a dead path from new scheduling snapshots without
  destroying the snapshot currently in use.

### Uplink replication

- [ ] **MULTI-08:** Assign one client global logical sequence before selecting
  paths.
- [ ] **MULTI-09:** Create one owned queue item per selected path.
- [ ] **MULTI-10:** Preserve the same logical sequence in every copy.
- [ ] **MULTI-11:** Assign independent path sequences at each actor.
- [ ] **MULTI-12:** Assign independent outer packet numbers at each actor.
- [ ] **MULTI-13:** Seal each copy under its path's own send key.
- [ ] **MULTI-14:** Ensure dropping one queued replica does not release another
  path's buffer.
- [ ] **MULTI-15:** Ensure failure of path B cannot block path A send progress.

### Server first-copy handling

- [ ] **MULTI-16:** Process path-A copy metrics before global dedup.
- [ ] **MULTI-17:** Process path-B copy metrics before global dedup.
- [ ] **MULTI-18:** Deliver the first fresh logical sequence to server TUN.
- [ ] **MULTI-19:** Drop the later duplicate before server TUN.
- [ ] **MULTI-20:** Retain later-copy race metadata.
- [ ] **MULTI-21:** Accept path copies arriving in either order.
- [ ] **MULTI-22:** Accept reordered distinct logical sequences without waiting
  for gaps.

### Downlink replication

- [ ] **MULTI-23:** Assign one server global logical sequence before selecting
  paths.
- [ ] **MULTI-24:** Queue independently owned downlink copies to path A and path
  B.
- [ ] **MULTI-25:** Send path A to its validated client endpoint.
- [ ] **MULTI-26:** Send path B to its validated client endpoint.
- [ ] **MULTI-27:** Deliver the first client-side logical copy to TUN.
- [ ] **MULTI-28:** Drop the later client-side duplicate before TUN.
- [ ] **MULTI-29:** Retain later-copy client race metadata.

### Failure tests

- [ ] **MULTI-30:** Apply different fixed delays to path A and path B.
- [ ] **MULTI-31:** Verify the lower-delay copy normally wins.
- [ ] **MULTI-32:** Add loss only to path A uplink.
- [ ] **MULTI-33:** Verify path B rescues uplink logical packets.
- [ ] **MULTI-34:** Add loss only to path B downlink.
- [ ] **MULTI-35:** Verify path A rescues downlink logical packets.
- [ ] **MULTI-36:** Cut path A during controlled replicated traffic.
- [ ] **MULTI-37:** Assert no global DATA sequence gap while path B has capacity.
- [ ] **MULTI-38:** Restore path A and attach it as a fresh incarnation.
- [ ] **MULTI-39:** Cut path B and repeat the no-gap assertion.
- [ ] **MULTI-40:** Apply duplication and reorder on both paths simultaneously.
- [ ] **MULTI-41:** Assert exactly one TUN injection per logical sequence.
- [ ] **MULTI-42:** Run multipath tests under the race detector where rootless
  in-memory variants permit it.

### Gate

- [ ] Uplink and downlink both race two independently keyed copies.
- [ ] First-copy delivery is correct regardless of arrival path.
- [ ] A deterministic single-path cut creates no logical gap under eligible
  replicated load.

### Checkpoint

```text
feat: add bidirectional multipath racing and deduplication
```

## 19. M15 — Directional health, reports, and path scoring

### Objective

Measure each direction independently and carry feedback over any surviving
path.

### PING/PONG and RTT

- [ ] **HEALTH-01:** Create `internal/path/health.go`.
- [ ] **HEALTH-02:** Generate cryptographically random nonzero PING IDs.
- [ ] **HEALTH-03:** Keep one bounded outstanding-probe map per path.
- [ ] **HEALTH-04:** Send PING at the negotiated interval.
- [ ] **HEALTH-05:** Keep probes active while DATA is flowing.
- [ ] **HEALTH-06:** Reply with PONG on the same path.
- [ ] **HEALTH-07:** Echo the PING logical identifier exactly.
- [ ] **HEALTH-08:** Rate-limit PONG generation through the control budget.
- [ ] **HEALTH-09:** Produce one RTT sample for the first matching PONG.
- [ ] **HEALTH-10:** Ignore a duplicate PONG for RTT sampling.
- [ ] **HEALTH-11:** Ignore an unknown or expired PONG identifier.
- [ ] **HEALTH-12:** Use only local monotonic timestamps.
- [ ] **HEALTH-13:** Implement RFC 6298 SRTT smoothing constants.
- [ ] **HEALTH-14:** Implement RFC 6298 RTTVAR smoothing constants.
- [ ] **HEALTH-15:** Maintain a rolling 60-second minimum RTT.
- [ ] **HEALTH-16:** Calculate queue delay as `max(0, srtt-min_rtt)`.
- [ ] **HEALTH-17:** Calculate probe timeout clamped from 250 ms through two
  seconds.

### PATH_REPORT generation

- [ ] **REPORT-01:** Create `internal/path/report.go`.
- [ ] **REPORT-02:** Maintain cumulative authenticated DATA packet count.
- [ ] **REPORT-03:** Maintain cumulative complete outer byte count.
- [ ] **REPORT-04:** Maintain cumulative finalized loss count.
- [ ] **REPORT-05:** Maintain cumulative first-arrival count.
- [ ] **REPORT-06:** Maintain cumulative finalized unique-rescue count.
- [ ] **REPORT-07:** Export the recent 256-bit receive bitmap.
- [ ] **REPORT-08:** Include the reported path token.
- [ ] **REPORT-09:** Include locally confirmed send outer MTU.
- [ ] **REPORT-10:** Set the send-MTU-validated flag only after validation.
- [ ] **REPORT-11:** Start report sequence at one per reported path.
- [ ] **REPORT-12:** Increment report sequence once per new report snapshot.
- [ ] **REPORT-13:** Trigger a report after 32 received DATA packets.
- [ ] **REPORT-14:** Trigger a report after 20 ms under DATA load.
- [ ] **REPORT-15:** Trigger an idle report every 500 ms.
- [ ] **REPORT-16:** Trigger an immediate report on MTU/validation change.
- [ ] **REPORT-17:** Coalesce pending reports for the same reported path.

### Cross-path report delivery

- [ ] **REPORT-18:** Let the session engine choose a carrier path independently
  of the reported path.
- [ ] **REPORT-19:** Prefer a healthy control-capable carrier.
- [ ] **REPORT-20:** Use an alternate carrier when the reported path is
  asymmetric or stale.
- [ ] **REPORT-21:** Preserve report sequence and payload when duplicating a
  report over another carrier.
- [ ] **REPORT-22:** Seal each carrier copy with that carrier's own key/nonce.
- [ ] **REPORT-23:** Verify the reported path token belongs to the carrier's
  logical session after authentication.
- [ ] **REPORT-24:** Reject a report referring to another session.
- [ ] **REPORT-25:** Ignore a report sequence older than the latest processed for
  its target path.
- [ ] **REPORT-26:** Ignore a duplicate report sequence.
- [ ] **REPORT-27:** Calculate delivery rate from counter deltas and local
  elapsed time.
- [ ] **REPORT-28:** Never compare remote wall-clock timestamps.

### Directional states

- [ ] **HEALTH-18:** Track receive-health state from packets arriving on the
  target path.
- [ ] **HEALTH-19:** Track send-health state from peer reports about the target
  path.
- [ ] **HEALTH-20:** Calculate feedback-stale timeout from SRTT and report
  interval.
- [ ] **HEALTH-21:** Mark one direction DEGRADED on stale evidence.
- [ ] **HEALTH-22:** Mark one direction DEGRADED on configured loss threshold.
- [ ] **HEALTH-23:** Mark one direction DEGRADED on queue-delay threshold.
- [ ] **HEALTH-24:** Mark one direction DEGRADED on repeated send errors.
- [ ] **HEALTH-25:** Mark one direction DEAD after the configured consecutive
  evidence timeouts.
- [ ] **HEALTH-26:** Keep a one-way path usable in its working direction.
- [ ] **HEALTH-27:** Mark the whole path DEAD on socket/interface/Noise failure.
- [ ] **HEALTH-28:** Mark the whole path DEAD when both directions are dead.
- [ ] **HEALTH-29:** Derive aggregate operator-facing state from both directions.

### Score and hysteresis

- [ ] **SCORE-01:** Implement the specified SRTT/RTTVAR/loss/queue score formula.
- [ ] **SCORE-02:** Apply the 5 ms active-path hysteresis bonus.
- [ ] **SCORE-03:** Apply a bounded stale-feedback penalty.
- [ ] **SCORE-04:** Hold a healthy primary for at least one second.
- [ ] **SCORE-05:** Require another path to win by more than the hysteresis bonus
  for two evaluations.
- [ ] **SCORE-06:** Bypass dwell when the current primary becomes ineligible.
- [ ] **SCORE-07:** Use immutable health snapshots for scoring.
- [ ] **SCORE-08:** Add deterministic score-boundary tests.

### Integration tests

- [ ] **HEALTH-TEST-01:** Break only path-A uplink.
- [ ] **HEALTH-TEST-02:** Deliver path-A downlink feedback over path B.
- [ ] **HEALTH-TEST-03:** Verify path-A directions enter different states.
- [ ] **HEALTH-TEST-04:** Break only path-B downlink.
- [ ] **HEALTH-TEST-05:** Deliver path-B uplink feedback over path A.
- [ ] **HEALTH-TEST-06:** Verify the functioning one-way direction remains
  usable.
- [ ] **HEALTH-TEST-07:** Reorder path sequences inside the loss window.
- [ ] **HEALTH-TEST-08:** Verify recovered gaps are not counted as loss.
- [ ] **HEALTH-TEST-09:** Age an unrecovered gap and verify one loss increment.
- [ ] **HEALTH-TEST-10:** Verify primary hysteresis prevents score flapping.

### Gate

- [ ] Asymmetric failures do not incorrectly discard the working direction.
- [ ] Reports can describe path A while arriving over path B.
- [ ] RTT, loss, delivery, win, and rescue metrics use authenticated input only.

### Checkpoint

```text
feat: add directional path health and cross-path feedback
```

## 20. M16 — Bidirectional PMTU discovery and MTU enforcement

### Objective

Keep every outer datagram within the confirmed per-direction path MTU while the
server's physical interface may remain at MTU 1500.

### MTU arithmetic

- [ ] **PMTU-01:** Create `internal/path/pmtu.go`.
- [ ] **PMTU-02:** Define base outer PMTU as 1200.
- [ ] **PMTU-03:** Define base inner MTU as `1200-88`, yielding 1112.
- [ ] **PMTU-04:** Define initial inner MTU as 1180.
- [ ] **PMTU-05:** Assert initial required outer size is `1180+88`, yielding
  1268.
- [ ] **PMTU-06:** Define maximum v1 inner MTU as 1400.
- [ ] **PMTU-07:** Assert maximum configured outer size is `1400+88`, yielding
  1488.
- [ ] **PMTU-08:** Assert a 1500-byte physical IP MTU has 232 bytes of headroom
  at the 1180 default.
- [ ] **PMTU-09:** Assert a 1500-byte physical IP MTU has 12 bytes of headroom at
  inner MTU 1400.
- [ ] **PMTU-10:** Reject an attempt to use inner MTU 1500 because it would
  require a 1588-byte outer path.
- [ ] **PMTU-11:** Keep Ethernet header/FCS bytes out of IP-MTU arithmetic.

### Per-direction PMTU state

- [ ] **PMTU-12:** Store PMTU state independently for each send direction.
- [ ] **PMTU-13:** Start with no confirmed size after a fresh path handshake.
- [ ] **PMTU-14:** Store base, lower bound, upper probe bound, current candidate,
  and confirmed size.
- [ ] **PMTU-15:** Keep exactly one outstanding PMTU probe per direction.
- [ ] **PMTU-16:** Generate a random nonzero probe ID.
- [ ] **PMTU-17:** Bind outstanding probe ID to candidate size and path token.
- [ ] **PMTU-18:** Clear outstanding state on ACK, timeout, or path close.
- [ ] **PMTU-19:** Reset all PMTU confidence for a new incarnation.

### Probe generation and validation

- [ ] **PMTU-20:** Build a PMTU_PROBE whose complete outer size equals the
  candidate.
- [ ] **PMTU-21:** Put candidate outer size in the first two plaintext bytes.
- [ ] **PMTU-22:** Fill remaining probe plaintext with random padding.
- [ ] **PMTU-23:** Pace the complete probe through the DATA pacer.
- [ ] **PMTU-24:** Charge the complete candidate outer size to pacing.
- [ ] **PMTU-25:** Receive and authenticate PMTU_PROBE before size processing.
- [ ] **PMTU-26:** Compare declared size to received UDP payload length plus 28.
- [ ] **PMTU-27:** Reject a mismatched declared/received size.
- [ ] **PMTU-28:** Echo probe ID in PMTU_ACK logical sequence.
- [ ] **PMTU-29:** Echo confirmed outer size in the 2-byte ACK payload.
- [ ] **PMTU-30:** Authenticate PMTU_ACK before state lookup.
- [ ] **PMTU-31:** Require ACK path token to match the outstanding probe path.
- [ ] **PMTU-32:** Require ACK ID and size to match the outstanding probe.
- [ ] **PMTU-33:** Ignore duplicate, late, or unknown ACKs.

### Search algorithm

- [ ] **PMTU-34:** Probe base outer size 1200 first.
- [ ] **PMTU-35:** Give each candidate three attempts.
- [ ] **PMTU-36:** Use the path probe timeout for each attempt.
- [ ] **PMTU-37:** Fail path validation when base size cannot be confirmed.
- [ ] **PMTU-38:** Probe negotiated inner MTU plus 88 after base succeeds.
- [ ] **PMTU-39:** Mark the direction validated when required size succeeds.
- [ ] **PMTU-40:** Publish confirmed send MTU and validation flag immediately.
- [ ] **PMTU-41:** If required size fails, bisect between confirmed lower bound
  and failed upper bound.
- [ ] **PMTU-42:** Round each bisection candidate down to an 8-byte boundary.
- [ ] **PMTU-43:** Stop search when no higher aligned candidate remains.
- [ ] **PMTU-44:** Revalidate current size every 60 seconds.
- [ ] **PMTU-45:** Limit upward probing after a reduction to once per ten
  minutes.

### Error hints and black-hole recovery

- [ ] **PMTU-46:** Map synchronous `EMSGSIZE` to the exact sending path.
- [ ] **PMTU-47:** Block the failed size immediately after `EMSGSIZE`.
- [ ] **PMTU-48:** Accept an ICMP PTB hint only when its quoted tuple maps to the
  path.
- [ ] **PMTU-49:** Never raise committed PMTU from ICMP alone.
- [ ] **PMTU-50:** Use PTB only to lower the next probe ceiling and block unsafe
  DATA temporarily.
- [ ] **PMTU-51:** Bucket finalized DATA losses by outer packet size.
- [ ] **PMTU-52:** Trigger revalidation after three losses concentrated in the
  largest bucket.
- [ ] **PMTU-53:** Probe a smaller confirmed size during suspected black hole.
- [ ] **PMTU-54:** Lower committed size when current-size probes fail and the
  smaller probe succeeds.
- [ ] **PMTU-55:** Mark oversized queued items ineligible immediately after a
  reduction.

### Session MTU activation and fallback

- [ ] **PMTU-56:** Keep the tunnel default route absent until the first path
  validates required size in both directions.
- [ ] **PMTU-57:** Read peer confirmed-send MTU from authenticated PATH_REPORT.
- [ ] **PMTU-58:** Calculate the smaller of local and peer directional limits.
- [ ] **PMTU-59:** If both directions support negotiated inner MTU plus 88,
  activate the path.
- [ ] **PMTU-60:** If either direction supports only a smaller safe size, close
  the provisional session.
- [ ] **PMTU-61:** Calculate lower requested inner MTU as confirmed outer size
  minus 88.
- [ ] **PMTU-62:** Clamp the lower request to 1112 through the configured maximum.
- [ ] **PMTU-63:** Reopen the session with that lower requested MTU.
- [ ] **PMTU-64:** Apply the negotiated value to client `red0` before tunnel-route
  activation.
- [ ] **PMTU-65:** Install a server per-peer host route carrying negotiated MTU.
- [ ] **PMTU-66:** Verify server `red0` may remain at its configured maximum while
  per-peer routes enforce smaller values.
- [ ] **PMTU-67:** Let the kernel fragment inner IPv4 only when the inner packet
  permits it.
- [ ] **PMTU-68:** Verify the kernel generates inner ICMP fragmentation-needed
  for DF traffic above the peer route MTU.

### Multipath MTU eligibility

- [ ] **PMTU-69:** Calculate per-path maximum DATA plaintext as confirmed outer
  size minus 88.
- [ ] **PMTU-70:** Exclude a path copy when the complete inner packet exceeds
  that value.
- [ ] **PMTU-71:** Keep a smaller secondary path probe/control-capable.
- [ ] **PMTU-72:** Increment `replica_mtu_ineligible` for an omitted oversized
  replica.
- [ ] **PMTU-73:** Keep the session MTU when at least one required path set can
  carry it.
- [ ] **PMTU-74:** Pause DATA in bounded queues when every path falls below the
  session MTU.
- [ ] **PMTU-75:** Keep the kill switch enforced during lower-MTU recovery.
- [ ] **PMTU-76:** Revalidate or reopen at a lower MTU before resuming DATA.

### PMTU integration matrix

- [ ] **PMTU-TEST-01:** Test physical path MTU 1500 with inner MTU 1180 and outer
  packets of 1268.
- [ ] **PMTU-TEST-02:** Test physical path MTU 1500 with inner MTU 1400 and outer
  packets of 1488.
- [ ] **PMTU-TEST-03:** Test a path that supports exactly outer size 1200.
- [ ] **PMTU-TEST-04:** Test required-size failure followed by lower-MTU reopen.
- [ ] **PMTU-TEST-05:** Test different uplink and downlink PMTUs.
- [ ] **PMTU-TEST-06:** Test path A larger than path B.
- [ ] **PMTU-TEST-07:** Verify small packets still replicate over both paths.
- [ ] **PMTU-TEST-08:** Verify oversized copies use only eligible paths.
- [ ] **PMTU-TEST-09:** Inject a correctly quoted PTB.
- [ ] **PMTU-TEST-10:** Inject a forged/unmapped PTB.
- [ ] **PMTU-TEST-11:** Filter all PTB packets and trigger black-hole recovery.
- [ ] **PMTU-TEST-12:** Reduce path MTU while traffic is active.
- [ ] **PMTU-TEST-13:** Verify no captured outer packet exceeds the confirmed
  PMTU.
- [ ] **PMTU-TEST-14:** Verify the IPv4 DF bit prevents outer fragmentation.
- [ ] **PMTU-TEST-15:** Verify no outer IPv4 fragments appear in packet capture.

### Gate

- [ ] Every DATA path enforces `inner_length + 88 <= confirmed_outer_mtu`.
- [ ] The 1500 server physical MTU is never confused with the TUN MTU.
- [ ] Base failure, asymmetric PMTU, PTB, and black-hole cases are deterministic.
- [ ] No integration capture contains an outer fragment.

### Checkpoint

```text
feat: add bidirectional PMTU discovery and enforcement
```

## 21. M17 — Per-direction congestion controller and pacers

### Objective

Replace the temporary fixed pacer with a feedback-driven, bounded controller
that accounts for every DATA copy and PMTU probe.

### Controller state and arithmetic

- [ ] **CC-01:** Create `internal/congestion/controller.go`.
- [ ] **CC-02:** Represent rate internally as integer bytes per second.
- [ ] **CC-03:** Convert configured Mbit/s and Kbit/s without binary/decimal unit
  ambiguity.
- [ ] **CC-04:** Reject conversion overflow.
- [ ] **CC-05:** Keep one controller per path sending direction.
- [ ] **CC-06:** Initialize at configured 1 Mbit/s default.
- [ ] **CC-07:** Clamp initialization to configured minimum and maximum.
- [ ] **CC-08:** Apply per-interface maximum as a stricter cap than the global
  maximum.
- [ ] **CC-09:** Reset controller state on every new path incarnation.
- [ ] **CC-10:** Never inherit an old incarnation's optimistic rate.
- [ ] **CC-11:** Store last increase and decrease monotonic times.
- [ ] **CC-12:** Store the last processed report sequence.
- [ ] **CC-13:** Ignore duplicate or regressing reports.

### Feedback processing

- [ ] **CC-14:** Calculate newly received packets from cumulative counter deltas.
- [ ] **CC-15:** Calculate newly received outer bytes from cumulative deltas.
- [ ] **CC-16:** Calculate newly finalized loss from cumulative deltas.
- [ ] **CC-17:** Reject counter regression without underflow.
- [ ] **CC-18:** Treat a reset as a new incarnation rather than a negative delta.
- [ ] **CC-19:** Calculate delivery rate from local elapsed monotonic time.
- [ ] **CC-20:** Mark feedback stale at
  `max(3*srtt, 3*20ms, 250ms)`.
- [ ] **CC-21:** Keep feedback for path A valid when its report arrives over path
  B.

### Additive increase

- [ ] **CC-22:** Require fresh feedback before increasing rate.
- [ ] **CC-23:** Require no newly finalized loss before increasing rate.
- [ ] **CC-24:** Require queue delay below the configured target.
- [ ] **CC-25:** Limit increase to once per RTT.
- [ ] **CC-26:** Calculate increment as
  `confirmed_outer_mtu / srtt` bytes per second.
- [ ] **CC-27:** Use checked integer arithmetic for the increment.
- [ ] **CC-28:** Clamp the result to the effective maximum rate.
- [ ] **CC-29:** Do not increase when SRTT is unknown.

### Multiplicative decrease

- [ ] **CC-30:** Halve the pacing rate when newly finalized loss is observed.
- [ ] **CC-31:** Apply at most one loss reduction per RTT.
- [ ] **CC-32:** Count consecutive feedback rounds above queue-delay target.
- [ ] **CC-33:** Halve the rate after two consecutive high-delay rounds.
- [ ] **CC-34:** Reset the high-delay counter after a healthy round.
- [ ] **CC-35:** Reduce to the minimum data rate when feedback becomes stale.
- [ ] **CC-36:** Clamp every reduction at the configured minimum.
- [ ] **CC-37:** Emit one bounded reason code for each rate change.

### DATA pacer

- [ ] **PACER-01:** Create `internal/congestion/pacer.go`.
- [ ] **PACER-02:** Inject the monotonic clock.
- [ ] **PACER-03:** Track available byte credit without floating-point math.
- [ ] **PACER-04:** Cap accumulated credit to a documented maximum burst.
- [ ] **PACER-05:** Start without a line-rate startup burst.
- [ ] **PACER-06:** Calculate the next send eligibility time for a complete outer
  size.
- [ ] **PACER-07:** Charge every primary DATA copy.
- [ ] **PACER-08:** Charge every replica DATA copy.
- [ ] **PACER-09:** Charge every PMTU probe.
- [ ] **PACER-10:** Do not charge a locally dropped item that was never sent.
- [ ] **PACER-11:** Do not refund a datagram after a permanent send error.
- [ ] **PACER-12:** Wake promptly when the controller lowers the rate.
- [ ] **PACER-13:** Prevent queued credit from creating a burst after suspend.
- [ ] **PACER-14:** Make cancellation unblock a pacing wait.

### Control budget

- [ ] **CONTROL-CC-01:** Create a separate control token bucket per sending
  direction.
- [ ] **CONTROL-CC-02:** Set its rate to the greater of 64 Kbit/s and 10% of
  current DATA rate.
- [ ] **CONTROL-CC-03:** Cap control rate at 1 Mbit/s.
- [ ] **CONTROL-CC-04:** Charge complete outer control-packet bytes.
- [ ] **CONTROL-CC-05:** Coalesce PATH_REPORT when the control bucket is empty.
- [ ] **CONTROL-CC-06:** Rate-limit PONG responses when the control bucket is
  empty.
- [ ] **CONTROL-CC-07:** Preserve one pending CLOSE without allowing an unbounded
  close queue.
- [ ] **CONTROL-CC-08:** Prevent control traffic from consuming DATA credit.
- [ ] **CONTROL-CC-09:** Prevent DATA traffic from consuming reserved control
  credit.

### Actor integration

- [ ] **CC-38:** Replace the temporary 1 Mbit/s fixed pacer in the path actor.
- [ ] **CC-39:** Feed authenticated PATH_REPORT deltas to the correct target
  controller.
- [ ] **CC-40:** Feed local queue-delay samples to the controller.
- [ ] **CC-41:** Publish current, minimum, maximum, and delivered rates in the
  immutable path snapshot.
- [ ] **CC-42:** Mark the send direction DEGRADED on stale controller feedback.
- [ ] **CC-43:** Continue bounded probes at the minimum/control rates.
- [ ] **CC-44:** Assert no production DATA write can bypass the pacer.

### Deterministic tests

- [ ] **CC-TEST-01:** Test initialization and all clamps.
- [ ] **CC-TEST-02:** Test one additive increase after one RTT.
- [ ] **CC-TEST-03:** Test no second increase inside the same RTT.
- [ ] **CC-TEST-04:** Test one loss halves the rate.
- [ ] **CC-TEST-05:** Test multiple losses in one RTT cause one decrease.
- [ ] **CC-TEST-06:** Test two high-delay rounds halve the rate.
- [ ] **CC-TEST-07:** Test stale feedback reduces to minimum.
- [ ] **CC-TEST-08:** Test fresh feedback permits recovery.
- [ ] **CC-TEST-09:** Test report counter regression.
- [ ] **CC-TEST-10:** Test integer boundaries at minimum and maximum MTU/rate.
- [ ] **CC-TEST-11:** Test pacer output timestamps at fixed rate.
- [ ] **CC-TEST-12:** Test maximum burst after idle.
- [ ] **CC-TEST-13:** Test no burst after a large fake-clock jump.
- [ ] **CC-TEST-14:** Test control report coalescing.
- [ ] **CC-TEST-15:** Test cancellation while DATA waits for credit.

### Fairness integration

- [ ] **CC-FAIR-01:** Create one constrained bottleneck in the namespace harness.
- [ ] **CC-FAIR-02:** Start one long-lived native TCP flow through that
  bottleneck.
- [ ] **CC-FAIR-03:** Start one greedy inner UDP flow through RED_MPUDP.
- [ ] **CC-FAIR-04:** Discard a documented warm-up interval.
- [ ] **CC-FAIR-05:** Measure both flow throughputs over the same interval.
- [ ] **CC-FAIR-06:** Calculate Jain's two-flow fairness index.
- [ ] **CC-FAIR-07:** Assert native TCP retains at least 35% of bottleneck
  throughput.
- [ ] **CC-FAIR-08:** Assert Jain's fairness index is at least 0.90.
- [ ] **CC-FAIR-09:** Repeat with loss.
- [ ] **CC-FAIR-10:** Repeat with added queue delay.
- [ ] **CC-FAIR-11:** Save parameters and results as machine-readable artifacts.

### Gate

- [ ] No DATA or PMTU probe bypasses congestion accounting.
- [ ] Controller unit tests contain no real sleeps.
- [ ] TCP retention and Jain fairness release thresholds pass.
- [ ] If the fairness gate fails, stop and reopen the transport decision.

### Checkpoint

```text
feat: add per-direction congestion control and pacing
```

## 22. M18 — Adaptive scheduler, traffic classes, and queue safety

### Objective

Fully replicate latency-sensitive/light traffic while bounding secondary-path
cost and preventing a slow path from delaying a healthy primary.

### Safe IPv4 classification

- [ ] **CLASS-01:** Create `internal/scheduler/classify.go`.
- [ ] **CLASS-02:** Accept only already validated IPv4 metadata.
- [ ] **CLASS-03:** Classify ICMP echo request as latency.
- [ ] **CLASS-04:** Classify ICMP echo reply as latency.
- [ ] **CLASS-05:** Keep other ICMP types standard unless configured by a later
  design revision.
- [ ] **CLASS-06:** Classify non-fragmented UDP as latency when complete inner
  total length is at most 768 by default.
- [ ] **CLASS-07:** Classify larger UDP as standard.
- [ ] **CLASS-08:** Classify TCP SYN as latency.
- [ ] **CLASS-09:** Classify TCP FIN as latency.
- [ ] **CLASS-10:** Classify TCP RST as latency.
- [ ] **CLASS-11:** Keep other TCP packets standard.
- [ ] **CLASS-12:** Classify configured DSCP values as latency.
- [ ] **CLASS-13:** Treat every non-initial IPv4 fragment as standard.
- [ ] **CLASS-14:** Treat an unsafe or incomplete transport header as standard.
- [ ] **CLASS-15:** Never drop or reroute a packet merely because classification
  failed.
- [ ] **CLASS-16:** Add table tests for ICMP, UDP boundaries, TCP flags, DSCP,
  options, and fragments.

### Immutable eligibility snapshot

- [ ] **SCHED-01:** Create `internal/scheduler/scheduler.go`.
- [ ] **SCHED-02:** Read one immutable snapshot of all paths per scheduling
  decision.
- [ ] **SCHED-03:** Exclude paths without completed handshake validation.
- [ ] **SCHED-04:** Exclude paths whose send direction is DEAD.
- [ ] **SCHED-05:** Exclude paths whose confirmed PMTU cannot carry the packet.
- [ ] **SCHED-06:** Exclude paths whose actor is draining or closed.
- [ ] **SCHED-07:** Keep a validated DEGRADED path available only for bounded
  primary fallback.
- [ ] **SCHED-08:** Return a no-path decision without blocking when no path can
  carry the packet.

### Primary selection

- [ ] **SCHED-09:** Rank send-healthy paths by the documented score.
- [ ] **SCHED-10:** Use stable path token ordering as the final deterministic
  tie-breaker.
- [ ] **SCHED-11:** Apply one-second primary dwell.
- [ ] **SCHED-12:** Apply two-evaluation hysteresis.
- [ ] **SCHED-13:** Switch immediately when current primary becomes ineligible.
- [ ] **SCHED-14:** Choose the best validated DEGRADED fallback when no healthy
  path exists.
- [ ] **SCHED-15:** Require fallback congestion controller to permit DATA.
- [ ] **SCHED-16:** Emit a bounded no-path drop reason otherwise.

### Replica selection and budgets

- [ ] **SCHED-17:** Remove the chosen primary from replica candidates.
- [ ] **SCHED-18:** Rank replica candidates by health, score, queue delay,
  winner rate, and rescue rate.
- [ ] **SCHED-19:** Limit total selected paths to configured maximum four.
- [ ] **SCHED-20:** Require replica send direction HEALTHY.
- [ ] **SCHED-21:** Require fresh feedback.
- [ ] **SCHED-22:** Require PMTU eligibility.
- [ ] **SCHED-23:** Require queue-sojourn estimate within 5 ms default budget.
- [ ] **SCHED-24:** Create one latency replica token bucket per secondary path.
- [ ] **SCHED-25:** Create one standard replica token bucket per secondary path.
- [ ] **SCHED-26:** Charge complete outer packet size to replica budgets.
- [ ] **SCHED-27:** Use 5 Mbit/s default latency replica budget.
- [ ] **SCHED-28:** Use 10 Mbit/s default standard replica budget.
- [ ] **SCHED-29:** Omit only the replica when its budget is exhausted.
- [ ] **SCHED-30:** Never omit or delay primary because a replica budget is
  exhausted.
- [ ] **SCHED-31:** Replicate every eligible packet at low load when all
  conditions pass.

### Queue split and service

- [ ] **QUEUE-01:** Replace the actor's initial DATA queue with control, latency,
  primary-data, and replica-data queues.
- [ ] **QUEUE-02:** Bound every queue by packet count.
- [ ] **QUEUE-03:** Bound every queue by byte count.
- [ ] **QUEUE-04:** Use control service priority first.
- [ ] **QUEUE-05:** Use latency service priority second.
- [ ] **QUEUE-06:** Use primary-data service priority third.
- [ ] **QUEUE-07:** Use replica-data service priority fourth.
- [ ] **QUEUE-08:** Apply the separate control token bucket despite control
  priority.
- [ ] **QUEUE-09:** Set default primary limit to 1024 packets.
- [ ] **QUEUE-10:** Set default primary byte limit to 2 MiB.
- [ ] **QUEUE-11:** Set default primary deadline to 50 ms.
- [ ] **QUEUE-12:** Set default replica limit to 256 packets.
- [ ] **QUEUE-13:** Set default replica byte limit to 512 KiB.
- [ ] **QUEUE-14:** Set default replica deadline to 5 ms.
- [ ] **QUEUE-15:** Drop a new replica when its queue is full.
- [ ] **QUEUE-16:** Leave the primary item untouched when replica enqueue fails.
- [ ] **QUEUE-17:** Apply bounded primary backpressure only until its deadline.
- [ ] **QUEUE-18:** Drop primary tail after the deadline.
- [ ] **QUEUE-19:** Drop an expired replica before assigning path/outer sequence.
- [ ] **QUEUE-20:** Drop an expired primary before encryption.
- [ ] **QUEUE-21:** Return every queue-dropped buffer exactly once.
- [ ] **QUEUE-22:** Publish queue packet, byte, and oldest-sojourn snapshots.
- [ ] **QUEUE-23:** Prove one blocked path actor cannot block another actor.

### Replica circuit breaker

- [ ] **CB-01:** Create `internal/scheduler/circuit.go`.
- [ ] **CB-02:** Open on queue delay above 15 ms and at least twice baseline.
- [ ] **CB-03:** Open when feedback is older than `feedback_stale_after`.
- [ ] **CB-04:** Open after replica queue exceeds its 5 ms budget for two
  checks.
- [ ] **CB-05:** Open when the class replica token bucket is empty.
- [ ] **CB-06:** Open during PMTU black-hole recovery.
- [ ] **CB-07:** Continue health and PMTU probes while open.
- [ ] **CB-08:** Keep primary eligibility independent of replica circuit state.
- [ ] **CB-09:** Start one-second cooldown when open conditions clear.
- [ ] **CB-10:** Require two healthy probe/report intervals after cooldown.
- [ ] **CB-11:** Close only after both recovery conditions pass.
- [ ] **CB-12:** Emit one state transition per open/close event.

### Scheduler tests

- [ ] **SCHED-TEST-01:** Select one healthy path as primary.
- [ ] **SCHED-TEST-02:** Fully replicate low-rate latency traffic over two paths.
- [ ] **SCHED-TEST-03:** Fully replicate low-rate standard traffic while budget
  permits.
- [ ] **SCHED-TEST-04:** Stop standard replicas when their budget is exhausted.
- [ ] **SCHED-TEST-05:** Preserve latency replicas under their separate budget.
- [ ] **SCHED-TEST-06:** Exclude a path for an oversized packet only.
- [ ] **SCHED-TEST-07:** Use that same path for a smaller packet.
- [ ] **SCHED-TEST-08:** Open the circuit on slow secondary queue growth.
- [ ] **SCHED-TEST-09:** Verify secondary queue never exceeds packet/byte limits.
- [ ] **SCHED-TEST-10:** Verify primary p99 does not grow from secondary backlog
  beyond the release threshold.
- [ ] **SCHED-TEST-11:** Recover the secondary after cooldown and healthy
  evidence.
- [ ] **SCHED-TEST-12:** Drop with no-path reason when every path is ineligible.
- [ ] **SCHED-TEST-13:** Run all rate/queue/cooldown tests with fake time.
- [ ] **SCHED-TEST-14:** Run scheduler/session/actor interaction under the race
  detector.

### Gate

- [ ] Low-rate traffic replicates across every healthy eligible path.
- [ ] Bulk traffic cannot create an unbounded secondary queue.
- [ ] Replica failure never blocks or drops an otherwise accepted primary copy.
- [ ] All scheduler boundaries have deterministic tests.

### Checkpoint

```text
feat: add adaptive replication and bounded path queues
```

## 23. M19 — NAT rebinding, interface changes, rekey, and lifecycle

### Objective

Replace endpoints and keys without redirecting traffic based on unauthenticated
input or resetting counters under an existing key.

### Remote NAT rebinding

- [ ] **ROAM-01:** Create `internal/path/roaming.go`.
- [ ] **ROAM-02:** Compare receive source address to the committed endpoint only
  after AEAD succeeds and outer replay commits.
- [ ] **ROAM-03:** Ignore a new source for candidate purposes when the outer
  packet is replayed or too old.
- [ ] **ROAM-04:** Store at most one candidate endpoint per path.
- [ ] **ROAM-05:** Replace an older unvalidated candidate with a newer
  authenticated candidate without growing state.
- [ ] **ROAM-06:** Track authenticated bytes received from the candidate.
- [ ] **ROAM-07:** Cap candidate-address output at three times authenticated
  bytes received from it.
- [ ] **ROAM-08:** Keep unrestricted downlink directed to the old endpoint.
- [ ] **ROAM-09:** Generate a cryptographically random nonzero challenge ID.
- [ ] **ROAM-10:** Store at most one outstanding challenge.
- [ ] **ROAM-11:** Send encrypted PATH_CHALLENGE to the candidate.
- [ ] **ROAM-12:** Require PATH_RESPONSE on the candidate source endpoint.
- [ ] **ROAM-13:** Require matching session ID, path token, and challenge ID.
- [ ] **ROAM-14:** Require PATH_RESPONSE outer replay freshness.
- [ ] **ROAM-15:** Commit the candidate only after every check passes.
- [ ] **ROAM-16:** Clear candidate/challenge state after commitment.
- [ ] **ROAM-17:** Expire an unanswered candidate using injected time.
- [ ] **ROAM-18:** Never reset outer packet number during endpoint replacement.

### Local address, interface, and gateway changes

- [ ] **REBIND-01:** Deliver typed link/address/route events to the session
  engine.
- [ ] **REBIND-02:** Ignore an event older than the current interface generation.
- [ ] **REBIND-03:** Mark a deleted interface path unavailable immediately.
- [ ] **REBIND-04:** Keep other paths running during one interface failure.
- [ ] **REBIND-05:** On source-address change, create a new socket.
- [ ] **REBIND-06:** Apply the same interface binding and mark to the new socket.
- [ ] **REBIND-07:** Perform a fresh JOIN with a fresh ephemeral key.
- [ ] **REBIND-08:** Identify the old path token as the replacement target.
- [ ] **REBIND-09:** Never move new socket state into the old cipher actor.
- [ ] **REBIND-10:** On gateway change, reconcile the path routing table first.
- [ ] **REBIND-11:** Create a fresh path incarnation after route reconciliation.
- [ ] **REBIND-12:** Keep the old incarnation until the replacement validates or
  definitively fails.

### Rekey triggers

- [ ] **REKEY-01:** Create `internal/path/rekey.go`.
- [ ] **REKEY-02:** Start an age deadline at path key installation.
- [ ] **REKEY-03:** Trigger replacement before one hour by default.
- [ ] **REKEY-04:** Count encrypted datagrams independently in both directions.
- [ ] **REKEY-05:** Trigger replacement before either direction reaches 2^32
  encrypted datagrams.
- [ ] **REKEY-06:** Trigger immediate replacement before any counter exhaustion.
- [ ] **REKEY-07:** Coalesce simultaneous age and packet-count triggers.
- [ ] **REKEY-08:** Allow only one replacement handshake per old path.
- [ ] **REKEY-09:** Use fresh handshake ID, path nonce, ephemeral key, path token,
  and transport keys.
- [ ] **REKEY-10:** Reset new transport packet numbers to zero only under the new
  keys.
- [ ] **REKEY-11:** Reset new path DATA sequences to one.
- [ ] **REKEY-12:** Reset new congestion/PMTU confidence conservatively.
- [ ] **REKEY-13:** Validate liveness and PMTU before scheduler insertion.
- [ ] **REKEY-14:** Atomically replace the scheduler snapshot entry.
- [ ] **REKEY-15:** Put the old path into DRAINING.
- [ ] **REKEY-16:** Accept authenticated late packets on the draining path.
- [ ] **REKEY-17:** Send no new DATA on the draining path.
- [ ] **REKEY-18:** Drain for `max(3*srtt, 1s)`.
- [ ] **REKEY-19:** Close the old socket after drain.
- [ ] **REKEY-20:** Erase retired cipher and join-related state as far as the Go
  runtime permits.

### Session replacement and shutdown

- [ ] **SESSION-LIFE-01:** Keep an active session routing while one provisional
  OPEN replacement validates.
- [ ] **SESSION-LIFE-02:** Switch tunnel-IP mapping only after replacement path
  validation.
- [ ] **SESSION-LIFE-03:** Close the old session after atomic mapping switch.
- [ ] **SESSION-LIFE-04:** Reject a second concurrent provisional replacement.
- [ ] **SESSION-LIFE-05:** Treat server restart as loss of all prior sessions.
- [ ] **SESSION-LIFE-06:** Reopen client session with bounded exponential
  backoff.
- [ ] **SESSION-LIFE-07:** Handle client suspend without emitting a burst of
  expired queued packets on resume.
- [ ] **SESSION-LIFE-08:** Re-evaluate interface addresses/routes after resume.
- [ ] **SESSION-LIFE-09:** Reopen when server idle timeout expired during
  suspend.
- [ ] **SESSION-LIFE-10:** On SIGTERM, stop TUN ingress first.
- [ ] **SESSION-LIFE-11:** Stop accepting new handshakes.
- [ ] **SESSION-LIFE-12:** Send bounded advisory CLOSE packets.
- [ ] **SESSION-LIFE-13:** Drain accepted primary output for a configured short
  deadline.
- [ ] **SESSION-LIFE-14:** Cancel path actors.
- [ ] **SESSION-LIFE-15:** Close TUN and UDP descriptors.
- [ ] **SESSION-LIFE-16:** Hand network restoration to the ordered cleanup path.

### Tests

- [ ] **ROAM-TEST-01:** Send a valid fresh packet from a new UDP source port.
- [ ] **ROAM-TEST-02:** Verify unrestricted downlink stays on the old endpoint
  before validation.
- [ ] **ROAM-TEST-03:** Complete challenge/response and verify endpoint switch.
- [ ] **ROAM-TEST-04:** Replay a captured valid packet from a spoofed candidate.
- [ ] **ROAM-TEST-05:** Verify anti-amplification never exceeds three times
  authenticated candidate input.
- [ ] **ROAM-TEST-06:** Send a wrong challenge response.
- [ ] **ROAM-TEST-07:** Expire an unanswered candidate.
- [ ] **REBIND-TEST-01:** Change client interface address and complete fresh JOIN.
- [ ] **REBIND-TEST-02:** Change gateway and verify route-before-JOIN ordering.
- [ ] **REBIND-TEST-03:** Bring one interface down and keep the other active.
- [ ] **REBIND-TEST-04:** Bring it back with a fresh incarnation.
- [ ] **REKEY-TEST-01:** Trigger time-based rekey with fake time.
- [ ] **REKEY-TEST-02:** Trigger packet-count rekey at the exact boundary.
- [ ] **REKEY-TEST-03:** Verify old/new keys overlap without nonce collision.
- [ ] **REKEY-TEST-04:** Verify late old-path duplicates remain deduplicated.
- [ ] **SESSION-LIFE-TEST-01:** Suspend/resume with expired queues.
- [ ] **SESSION-LIFE-TEST-02:** Restart server during active two-path traffic.
- [ ] **SESSION-LIFE-TEST-03:** Terminate client during pacing and verify bounded
  shutdown.

### Gate

- [ ] Endpoint changes require authenticated freshness and challenge proof.
- [ ] Local network changes and rekeys always create a new key incarnation.
- [ ] No lifecycle test resets a nonce under an existing key.

### Checkpoint

```text
feat: add secure roaming rekey and session lifecycle
```

## 24. M20 — Full-tunnel routing, DNS, IPv6, and kill-switch hardening

### Objective

Make production traffic policy explicit and fail closed throughout startup,
operation, path loss, crash recovery, and graceful shutdown.

### Client kill switch

- [ ] **KILL-01:** Create `internal/firewall/client_linux.go`.
- [ ] **KILL-02:** Build the client nftables table as one atomic transaction.
- [ ] **KILL-03:** Allow loopback traffic.
- [ ] **KILL-04:** Allow UDP to the exact configured server IPv4/port only when
  carrying a configured RED_MPUDP mark.
- [ ] **KILL-05:** Restrict each allowed mark to its configured physical
  interface.
- [ ] **KILL-06:** Allow narrowly matched IPv4 DHCP client traffic.
- [ ] **KILL-07:** Allow output through `red0`.
- [ ] **KILL-08:** Allow configured connected LAN prefixes only when `allow_lan`
  is true.
- [ ] **KILL-09:** Reject other IPv4 output through physical interfaces.
- [ ] **KILL-10:** Reject IPv6 output when `ipv6_policy` is `block`.
- [ ] **KILL-11:** Leave IPv6 routing unchanged only when policy is explicit
  `passthrough`.
- [ ] **KILL-12:** Publish a warning/metric for leak-capable IPv6 passthrough.
- [ ] **KILL-13:** Reject an unknown IPv6 policy at config load.
- [ ] **KILL-14:** Record exact table ownership in the mutation journal.
- [ ] **KILL-15:** Make kill-switch installation idempotent.
- [ ] **KILL-16:** Make kill-switch removal ownership checked.

### Startup ordering

- [ ] **START-01:** Load configuration.
- [ ] **START-02:** Validate every configuration field.
- [ ] **START-03:** Load and validate key permissions.
- [ ] **START-04:** Discover physical interfaces, addresses, gateways, and MTUs.
- [ ] **START-05:** Create and fsync the mutation journal.
- [ ] **START-06:** Prepare marked path sockets without sending packets.
- [ ] **START-07:** Install/reconcile path policy tables.
- [ ] **START-08:** Apply required reverse-path sysctls.
- [ ] **START-09:** Install the strict kill switch.
- [ ] **START-10:** Begin the first handshake only after kill-switch readback
  succeeds.
- [ ] **START-11:** Validate bidirectional liveness and PMTU.
- [ ] **START-12:** Configure negotiated TUN address and MTU.
- [ ] **START-13:** Configure managed DNS.
- [ ] **START-14:** Install the tunnel-table default route last.
- [ ] **START-15:** Mark the client ready only after route readback succeeds.
- [ ] **START-16:** On any failure, run ownership-checked rollback in reverse
  order.

### systemd-resolved integration

- [ ] **DNS-01:** Create `internal/resolver/resolved_linux.go`.
- [ ] **DNS-02:** Connect to systemd-resolved through D-Bus.
- [ ] **DNS-03:** Detect absence of systemd-resolved before network mutation.
- [ ] **DNS-04:** Read and journal prior per-link DNS servers.
- [ ] **DNS-05:** Read and journal prior per-link default-route setting.
- [ ] **DNS-06:** Set configured IPv4 DNS servers on `red0`.
- [ ] **DNS-07:** Mark `red0` as the default DNS route.
- [ ] **DNS-08:** Ensure configured DNS-server packets use the tunnel route.
- [ ] **DNS-09:** Never write `/etc/resolv.conf`.
- [ ] **DNS-10:** Fail startup in strict mode when D-Bus authorization is absent.
- [ ] **DNS-11:** Permit `manage: false` only as an explicit operator choice.
- [ ] **DNS-12:** Restore exact prior per-link settings during graceful cleanup.
- [ ] **DNS-13:** Preserve newer operator changes and report a restoration
  conflict.

### Shutdown and crash behavior

- [ ] **STOP-01:** Remove the tunnel default rule/route first.
- [ ] **STOP-02:** Restore resolver state second.
- [ ] **STOP-03:** Remove TUN-specific policy routes/rules.
- [ ] **STOP-04:** Restore conditional sysctls.
- [ ] **STOP-05:** Remove the kill switch last on graceful shutdown.
- [ ] **STOP-06:** Delete the journal only after successful complete cleanup.
- [ ] **STOP-07:** Preserve the journal after a partial cleanup error.
- [ ] **STOP-08:** Leave the kill-switch nftables table installed after SIGKILL.
- [ ] **STOP-09:** Reconcile a stale owned table on next start.
- [ ] **STOP-10:** Recover stale routes/rules from the journal.
- [ ] **STOP-11:** Make
  `red-mpudp cleanup --state-file /run/red-mpudp/client-default.json`
  perform the same ordered recovery.
- [ ] **STOP-12:** Refuse cleanup of a journal belonging to another role or
  instance.

### Server production networking

- [ ] **SERVER-NET-01:** Apply server forwarding/NAT only when `manage_nat` is
  true.
- [ ] **SERVER-NET-02:** Leave host forwarding/NAT untouched when it is false.
- [ ] **SERVER-NET-03:** Journal server forwarding and owned nftables state.
- [ ] **SERVER-NET-04:** Install one /32 peer route with negotiated MTU per
  active peer.
- [ ] **SERVER-NET-05:** Remove a peer route when its session mapping closes.
- [ ] **SERVER-NET-06:** Preserve a replacement session's old route until the
  atomic session switch.
- [ ] **SERVER-NET-07:** Reconcile stale server-owned NAT state on restart.
- [ ] **SERVER-NET-08:** Document `manage_nat: false` as preferred when the host
  has persistent operator-managed networking.

### Service hardening

- [ ] **SYSTEMD-01:** Create `packaging/systemd/red-mpudp-client.service`.
- [ ] **SYSTEMD-02:** Create `packaging/systemd/red-mpudp-server.service`.
- [ ] **SYSTEMD-03:** Use a dedicated service account.
- [ ] **SYSTEMD-04:** Set `RuntimeDirectory=red-mpudp`.
- [ ] **SYSTEMD-05:** Limit device access to `/dev/net/tun`.
- [ ] **SYSTEMD-06:** Set the smallest tested capability bounding set.
- [ ] **SYSTEMD-07:** Set required ambient capabilities explicitly.
- [ ] **SYSTEMD-08:** Enable `NoNewPrivileges` when compatible with the tested
  capability setup.
- [ ] **SYSTEMD-09:** Protect system directories while allowing config/key reads
  and runtime-journal writes.
- [ ] **SYSTEMD-10:** Add a narrow systemd-resolved authorization policy for the
  service account.
- [ ] **SYSTEMD-11:** Verify client service startup under the hardened unit.
- [ ] **SYSTEMD-12:** Verify server service startup under the hardened unit.
- [ ] **SYSTEMD-13:** Verify key files unavailable to the service cause a clean
  fail-closed error.

### Leak and policy matrix

- [ ] **LEAK-01:** Test IPv4 during startup before handshake completion.
- [ ] **LEAK-02:** Test IPv4 during active healthy operation.
- [ ] **LEAK-03:** Test IPv4 after all paths fail.
- [ ] **LEAK-04:** Test IPv4 during lower-MTU session recovery.
- [ ] **LEAK-05:** Test IPv4 during graceful shutdown before kill-switch removal.
- [ ] **LEAK-06:** Test IPv4 after SIGKILL.
- [ ] **LEAK-07:** Test IPv6 with block policy at each lifecycle state.
- [ ] **LEAK-08:** Test IPv6 passthrough only when explicitly configured.
- [ ] **LEAK-09:** Test DNS during startup.
- [ ] **LEAK-10:** Test DNS during active operation.
- [ ] **LEAK-11:** Test DNS after all paths fail.
- [ ] **LEAK-12:** Test DNS restoration after graceful shutdown.
- [ ] **LEAK-13:** Test physical LAN access with `allow_lan: false`.
- [ ] **LEAK-14:** Test physical LAN access with `allow_lan: true`.
- [ ] **LEAK-15:** Test DHCP renewal while LAN bypass is false.
- [ ] **LEAK-16:** Capture every physical interface and assert unauthorized
  application/DNS packets never appear.

### Gate

- [ ] The strict lifecycle matrix has no unauthorized IPv4, IPv6, DNS, or LAN
  escape.
- [ ] Graceful shutdown restores prior state in the documented order.
- [ ] SIGKILL remains fail-closed and is recoverable through restart or guarded
  cleanup.
- [ ] Hardened systemd client and server units work on the release matrix.

### Checkpoint

```text
feat: harden full-tunnel routing DNS and kill switch
```

## 25. M21 — Metrics, health endpoints, logs, and operator documentation

### Objective

Make correctness and failure modes observable without exposing secrets or
creating attacker-controlled metric cardinality.

### Metrics foundation

- [ ] **METRIC-01:** Create `internal/metrics/registry.go`.
- [ ] **METRIC-02:** Use a private registry rather than implicit global
  registration.
- [ ] **METRIC-03:** Define the allowed bounded labels centrally.
- [ ] **METRIC-04:** Permit configured peer name as a label.
- [ ] **METRIC-05:** Permit bounded path index as a label.
- [ ] **METRIC-06:** Permit bounded direction, class, state, and reason enums.
- [ ] **METRIC-07:** Prohibit session ID as a label.
- [ ] **METRIC-08:** Prohibit source address/port as labels.
- [ ] **METRIC-09:** Prohibit handshake ID, path token, and attacker text as
  labels.
- [ ] **METRIC-10:** Test label construction against the allowlist.

### Path and scheduler metrics

- [ ] **METRIC-11:** Export aggregate/send/receive path state.
- [ ] **METRIC-12:** Export SRTT, RTTVAR, minimum RTT, and queue delay.
- [ ] **METRIC-13:** Export inbound and remotely reported loss.
- [ ] **METRIC-14:** Export delivery rate.
- [ ] **METRIC-15:** Export winner and unique-rescue rates.
- [ ] **METRIC-16:** Export local and peer confirmed PMTU.
- [ ] **METRIC-17:** Export report age.
- [ ] **METRIC-18:** Export congestion pacing rate and effective caps.
- [ ] **METRIC-19:** Export per-path/class packets and bytes enqueued.
- [ ] **METRIC-20:** Export per-path/class packets and bytes sent.
- [ ] **METRIC-21:** Export queue depth and queue bytes.
- [ ] **METRIC-22:** Export expired, queue-full, budget, circuit, send-error, and
  MTU-ineligible drops by bounded reason.
- [ ] **METRIC-23:** Export current primary and selected replica count without
  random-token labels.
- [ ] **METRIC-24:** Export classification counts.

### Crypto, dedup, session, and OS metrics

- [ ] **METRIC-25:** Export handshake attempts and successful handshakes.
- [ ] **METRIC-26:** Export handshake failures by bounded reason.
- [ ] **METRIC-27:** Export retry and rekey counts.
- [ ] **METRIC-28:** Export malformed, authentication-failure, and replay-drop
  counts.
- [ ] **METRIC-29:** Export dedup fresh, duplicate, and too-old counts.
- [ ] **METRIC-30:** Export dedup-window utilization.
- [ ] **METRIC-31:** Export reorder depth and observation-unknown counts.
- [ ] **METRIC-32:** Export active/provisional sessions and paths.
- [ ] **METRIC-33:** Export pending handshake counts.
- [ ] **METRIC-34:** Export idle closes and endpoint-validation results.
- [ ] **METRIC-35:** Export TUN packets, bytes, reads, writes, and errors.
- [ ] **METRIC-36:** Export route/firewall/resolver reconciliation failures.
- [ ] **METRIC-37:** Export kill-switch installed state.
- [ ] **METRIC-38:** Export goroutine, heap, allocation, GC pause, CPU, and
  process metrics.

### Snapshot safety and HTTP

- [ ] **METRIC-39:** Collect immutable snapshots without reading actor-owned
  mutable objects.
- [ ] **METRIC-40:** Run metrics snapshot collection under the race detector.
- [ ] **METRIC-41:** Bind metrics to loopback by default.
- [ ] **METRIC-42:** Reject public bind without explicit opt-in.
- [ ] **METRIC-43:** Serve Prometheus text format at `/metrics`.
- [ ] **METRIC-44:** Serve liveness at `/healthz`.
- [ ] **METRIC-45:** Return server healthy only after listener/TUN requirements
  are active.
- [ ] **METRIC-46:** Return client ready only after authenticated path, PMTU,
  DNS, kill switch, and route activation succeed.
- [ ] **METRIC-47:** Return not-ready during lower-MTU recovery or no-path state.
- [ ] **METRIC-48:** Shut down the HTTP server through the process cancellation
  path.

### Structured logging and redaction

- [ ] **LOG-01:** Create `internal/logging/logging.go`.
- [ ] **LOG-02:** Use stable event names and bounded reason fields.
- [ ] **LOG-03:** Log session/path state transitions without random identifiers
  at normal level.
- [ ] **LOG-04:** Permit shortened internal correlation IDs only at debug level
  when they cannot become secrets or metric labels.
- [ ] **LOG-05:** Never log static private keys.
- [ ] **LOG-06:** Never log PSKs or derived keys.
- [ ] **LOG-07:** Never log join tokens.
- [ ] **LOG-08:** Never log plaintext inner packets.
- [ ] **LOG-09:** Never log full unauthenticated datagrams.
- [ ] **LOG-10:** Bound logged remote addresses to necessary operational events.
- [ ] **LOG-11:** Add automated redaction tests with sentinel secret values.
- [ ] **LOG-12:** Scan captured test logs and fail if a sentinel appears.

### Operator documentation

- [ ] **DOC-01:** Add `configs/client.example.yaml` containing no secret values.
- [ ] **DOC-02:** Add `configs/server.example.yaml` containing no secret values.
- [ ] **DOC-03:** Add `docs/operations/key-provisioning.md`.
- [ ] **DOC-04:** Document creation and secure transfer of client public key and
  per-peer PSK.
- [ ] **DOC-05:** Document file ownership and mode requirements.
- [ ] **DOC-06:** Add `docs/operations/client-routing.md`.
- [ ] **DOC-07:** Document marks, tables, rule priorities, DHCP, LAN, DNS, and
  IPv6 policies.
- [ ] **DOC-08:** Add `docs/operations/server-setup.md`.
- [ ] **DOC-09:** Document managed and operator-managed NAT modes.
- [ ] **DOC-10:** Add `docs/operations/mtu.md`.
- [ ] **DOC-11:** Explain physical MTU 1500 versus TUN MTU 1180 and 88-byte
  overhead.
- [ ] **DOC-12:** Document PMTU fallback, minimum 1112, and maximum 1400.
- [ ] **DOC-13:** Add `docs/operations/recovery.md`.
- [ ] **DOC-14:** Document graceful stop, SIGKILL fail-closed behavior, restart,
  and guarded cleanup.
- [ ] **DOC-15:** Add `docs/operations/troubleshooting.md` keyed to bounded reason
  codes and health metrics.
- [ ] **DOC-16:** Document that one exit server cannot eliminate failures shared
  beyond the exit.

### Gate

- [ ] Metrics cover every required subsystem with bounded cardinality.
- [ ] Redaction tests prove secrets and plaintext never enter logs.
- [ ] A new operator can provision, start, inspect, stop, and recover both roles
  from the checked-in documentation.

### Checkpoint

```text
feat: add bounded observability and operations documentation
```

## 26. M22 — Complete verification, performance, security review, and release

### Objective

Prove every v1 claim with saved, reproducible evidence before producing a
release artifact.

### Unit, race, fuzz, and static checks

- [ ] **VERIFY-01:** Run `make fmt` and verify a clean diff afterward.
- [ ] **VERIFY-02:** Run `make vet`.
- [ ] **VERIFY-03:** Run `make test-unit`.
- [ ] **VERIFY-04:** Run `make test-race`.
- [ ] **VERIFY-05:** Run every wire fuzzer for the release-duration budget.
- [ ] **VERIFY-06:** Run the replay reference-model fuzzer.
- [ ] **VERIFY-07:** Run the dedup reference-model fuzzer.
- [ ] **VERIFY-08:** Run the IPv4 parser fuzzer.
- [ ] **VERIFY-09:** Run random session/path event-sequence fuzzing.
- [ ] **VERIFY-10:** Run dependency vulnerability scanning with the selected Go
  toolchain.
- [ ] **VERIFY-11:** Run license-policy checks for direct and transitive
  dependencies.
- [ ] **VERIFY-12:** Save command lines, tool versions, seeds, and outputs.

### Full namespace integration suite

- [ ] **VERIFY-13:** Run single-path ICMP, UDP, TCP, and DNS tests.
- [ ] **VERIFY-14:** Run two-path first-copy/dedup tests in both directions.
- [ ] **VERIFY-15:** Run independent delay, jitter, loss, duplicate, reorder, and
  rate-limit cases on all four path directions.
- [ ] **VERIFY-16:** Run path-A and path-B deterministic cut tests.
- [ ] **VERIFY-17:** Run asymmetric uplink/downlink failure tests.
- [ ] **VERIFY-18:** Run slow-secondary queue and circuit-breaker tests.
- [ ] **VERIFY-19:** Run NAT rebinding and local address/gateway change tests.
- [ ] **VERIFY-20:** Run handshake replay, cookie, work-limit, and invalid-tag
  flood tests.
- [ ] **VERIFY-21:** Run the complete PMTU/PTB/black-hole matrix.
- [ ] **VERIFY-22:** Run server/client restart, idle timeout, suspend/resume, and
  rekey overlap tests.
- [ ] **VERIFY-23:** Run multiple-peer source-spoof tests.
- [ ] **VERIFY-24:** Run journal, SIGKILL, cleanup, and operator-conflict tests.
- [ ] **VERIFY-25:** Run the entire IPv4/IPv6/DNS/LAN leak matrix.
- [ ] **VERIFY-26:** Save namespace topology, kernel version, sysctls, and results
  as artifacts.

### Performance comparison

- [ ] **PERF-01:** Create a runner for direct path A.
- [ ] **PERF-02:** Create a runner for direct path B.
- [ ] **PERF-03:** Create a runner for RED_MPUDP primary-only.
- [ ] **PERF-04:** Create a runner for RED_MPUDP adaptive redundant mode.
- [ ] **PERF-05:** Feed identical deterministic impairment seeds to all modes.
- [ ] **PERF-06:** Record p50 RTT.
- [ ] **PERF-07:** Record p95 RTT.
- [ ] **PERF-08:** Record p99 RTT.
- [ ] **PERF-09:** Record p99.9 RTT.
- [ ] **PERF-10:** Record accepted logical loss.
- [ ] **PERF-11:** Record reorder depth.
- [ ] **PERF-12:** Record per-path win and rescue rates.
- [ ] **PERF-13:** Record queue sojourn distributions.
- [ ] **PERF-14:** Record CPU, allocations, GC pauses, packets/s, and bytes/s.
- [ ] **PERF-15:** Verify primary-only adds no more than 2 ms to p99 versus the
  same direct namespace path at 10,000 packets/s.
- [ ] **PERF-16:** Verify redundant mode follows the faster-copy distribution
  when both paths have capacity.
- [ ] **PERF-17:** Verify deterministic path cuts produce no global DATA gap
  under eligible replicated load.
- [ ] **PERF-18:** Rate-limit the secondary below offered load.
- [ ] **PERF-19:** Verify all secondary queues remain within configured limits.
- [ ] **PERF-20:** Verify primary p99 is no more than 5 ms worse than
  primary-only in the slow-secondary test.
- [ ] **PERF-21:** Re-run greedy-inner-UDP versus native-TCP fairness.
- [ ] **PERF-22:** Verify native TCP retains at least 35% of bottleneck
  throughput.
- [ ] **PERF-23:** Verify Jain's fairness index remains at least 0.90.
- [ ] **PERF-24:** Save raw samples and summarized results without replacing raw
  data.

### Long stress and resource bounds

- [ ] **STRESS-01:** Run a 30-minute two-path traffic test.
- [ ] **STRESS-02:** Cycle path A down/up during the stress test.
- [ ] **STRESS-03:** Cycle path B down/up during the stress test.
- [ ] **STRESS-04:** Trigger at least one rekey during the stress test.
- [ ] **STRESS-05:** Trigger PMTU revalidation during the stress test.
- [ ] **STRESS-06:** Track heap samples over time.
- [ ] **STRESS-07:** Track goroutine count over time.
- [ ] **STRESS-08:** Track every queue depth over time.
- [ ] **STRESS-09:** Assert no monotonic heap growth after warm-up.
- [ ] **STRESS-10:** Assert no monotonic goroutine growth.
- [ ] **STRESS-11:** Assert no queue exceeds configured packet/byte bounds.
- [ ] **STRESS-12:** Assert no deadlock, race report, nonce failure, or duplicate
  TUN injection.

### Real two-uplink test bed

- [ ] **REAL-01:** Record client hardware, kernel, Go build, server hardware, and
  physical interface MTUs.
- [ ] **REAL-02:** Verify the two client uplinks are genuinely independent.
- [ ] **REAL-03:** Record client-to-exit route per uplink.
- [ ] **REAL-04:** Record exit-to-game-target route.
- [ ] **REAL-05:** Run direct path-A baseline.
- [ ] **REAL-06:** Run direct path-B baseline.
- [ ] **REAL-07:** Run RED_MPUDP primary-only over path A.
- [ ] **REAL-08:** Run RED_MPUDP primary-only over path B.
- [ ] **REAL-09:** Run adaptive redundant mode.
- [ ] **REAL-10:** Run idle game-like UDP traffic.
- [ ] **REAL-11:** Add a background download.
- [ ] **REAL-12:** Introduce impairment on uplink A only.
- [ ] **REAL-13:** Introduce impairment on uplink B only.
- [ ] **REAL-14:** Physically disconnect and reconnect uplink A.
- [ ] **REAL-15:** Physically disconnect and reconnect uplink B.
- [ ] **REAL-16:** Test suspend/resume.
- [ ] **REAL-17:** Test DHCP renewal and source-address change.
- [ ] **REAL-18:** Save packet captures with payload capture disabled or safely
  redacted.
- [ ] **REAL-19:** Save machine-readable latency, loss, resource, and path
  results.
- [ ] **REAL-20:** Record whether the exit route itself dominates end-to-end
  latency.

### Security and correctness review

- [ ] **SEC-01:** Review every receive path against the authentication-before-
  state invariant.
- [ ] **SEC-02:** Review every cipher-state owner for concurrent access.
- [ ] **SEC-03:** Review every nonce initialization, increment, rekey, and close
  path.
- [ ] **SEC-04:** Review handshake retry and response sizes for amplification.
- [ ] **SEC-05:** Review candidate endpoint output for the three-times limit.
- [ ] **SEC-06:** Review all maps, queues, caches, labels, and logs for attacker-
  controlled unbounded growth.
- [ ] **SEC-07:** Review tunnel-address source enforcement across multiple peers.
- [ ] **SEC-08:** Review all key file opens for symlink, permission, and overwrite
  behavior.
- [ ] **SEC-09:** Review journal cleanup for broad or ambiguous deletion.
- [ ] **SEC-10:** Review nftables transactions for foreign-rule preservation.
- [ ] **SEC-11:** Review resolver and sysctl restoration conflict behavior.
- [ ] **SEC-12:** Review release logs and metrics for sentinel secrets.
- [ ] **SEC-13:** Resolve every high-severity finding before release.
- [ ] **SEC-14:** Record accepted lower-severity risks with owner and review date.

### Release evidence and artifacts

- [ ] **RELEASE-01:** Complete every row in `v1-traceability.md` with a passing
  test or benchmark artifact.
- [ ] **RELEASE-02:** Update the design document for every approved protocol
  deviation discovered during implementation.
- [ ] **RELEASE-03:** Regenerate and verify all protocol golden vectors.
- [ ] **RELEASE-04:** Freeze configuration defaults from measured evidence.
- [ ] **RELEASE-05:** Record the environment behind every frozen default.
- [ ] **RELEASE-06:** Build the Linux release binary reproducibly.
- [ ] **RELEASE-07:** Generate a checksum for the binary.
- [ ] **RELEASE-08:** Generate a software bill of materials.
- [ ] **RELEASE-09:** Package example configs, systemd units, and operations
  documentation.
- [ ] **RELEASE-10:** Install the package on a clean client test host.
- [ ] **RELEASE-11:** Install the package on a clean server test host.
- [ ] **RELEASE-12:** Run a final smoke tunnel using only packaged artifacts.
- [ ] **RELEASE-13:** Verify uninstall/cleanup preserves unrelated networking
  state.
- [ ] **RELEASE-14:** Create `docs/releases/v1-evidence.md` as the release
  evidence index.
- [ ] **RELEASE-15:** Record the normative design baseline commit in the
  evidence index.
- [ ] **RELEASE-16:** Record the Phase 0 transport decision record and commit in
  the evidence index.
- [ ] **RELEASE-17:** Record the release-candidate source commit in the evidence
  index.
- [ ] **RELEASE-18:** Record the Go version and full Linux kernel test matrix in
  the evidence index.
- [ ] **RELEASE-19:** Link the unit, race, and static-analysis artifacts from the
  evidence index.
- [ ] **RELEASE-20:** Link the fuzz and protocol-vector artifacts from the
  evidence index.
- [ ] **RELEASE-21:** Link the namespace integration and leak-test artifacts from
  the evidence index.
- [ ] **RELEASE-22:** Link the performance, fairness, and stress artifacts from
  the evidence index.
- [ ] **RELEASE-23:** Link the real two-uplink validation artifact from the
  evidence index.
- [ ] **RELEASE-24:** Link the completed security review from the evidence
  index.
- [ ] **RELEASE-25:** Record the release binary checksum and software bill of
  materials location in the evidence index.
- [ ] **RELEASE-26:** Link every accepted risk to its owner and next review date.
- [ ] **RELEASE-27:** Verify every evidence-index link resolves from a clean
  checkout.
- [ ] **RELEASE-28:** Verify the evidence index contains no secret, private key,
  public client IP, or unredacted payload capture.
- [ ] **RELEASE-29:** Re-run evidence-index link and redaction checks after its
  final edit.

### Final gate

- [ ] Every source-design v1 success criterion has objective evidence.
- [ ] Every mandatory unit, race, fuzz, integration, performance, stress, leak,
  and security check passes.
- [ ] No release path contains plaintext forwarding or unpaced DATA.
- [ ] No implementation, verification, or evidence-index item above this gate
  remains unchecked.

### Release-validation checkpoint

```text
release: validate RED_MPUDP v1
```

### Tag and closeout

- [ ] **RELEASE-30:** Verify the release-validation commit contains the exact
  source and evidence intended for release.
- [ ] **RELEASE-31:** Verify the worktree is clean apart from explicitly
  excluded local test artifacts.
- [ ] **RELEASE-32:** Create the approved annotated v1 release tag on the
  release-validation commit.
- [ ] **RELEASE-33:** Verify the tag resolves to that exact commit.
- [ ] **RELEASE-34:** Rebuild the binary from the tag in the recorded clean
  build environment.
- [ ] **RELEASE-35:** Verify the rebuilt binary checksum equals the checksum in
  the evidence index.
- [ ] **RELEASE-36:** Archive the tag name, commit hash, checksum, and evidence
  index together in the release system.

### Completion gate

- [ ] The annotated v1 tag points to the validated release commit.
- [ ] The tagged source reproduces the archived binary checksum.
- [ ] All M00 through M22 gates and closeout steps are checked.
