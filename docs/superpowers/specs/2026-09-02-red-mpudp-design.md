# RED_MPUDP — Multipath UDP VPN — Design and Implementation Plan

**Status:** Implementation-ready v1 design draft
**Date:** 2026-09-02
**Revision:** 2 — security, congestion, routing, PMTU, and test plan completed
**Author:** malharrajpara28@gmail.com

## 1. Purpose

RED_MPUDP is a latency-first IPv4 VPN for Linux. It creates a TUN interface,
encrypts inner IP packets, and sends selected packets over multiple independent
network paths. The receiver authenticates every path copy, accepts the first
valid copy of each logical packet, and discards later copies.

The primary goal is to reduce tail latency and avoid a path-detection pause when
one uplink develops jitter, loss, or a brief outage. The benefit applies to the
client-to-exit-server portion of the route. A single exit server cannot improve
the shared route from that server to the final Internet destination, and the
tunnel is not expected to beat a direct route in every topology.

"No-gap failover" in this document has a precise, conditional meaning: when a
packet was replicated before a path failed and at least one independent copy
survives, delivery does not wait for failure detection or retransmission. It is
not a guarantee of zero loss under correlated failures, server failure,
congestion on every path, or packets that were not eligible for replication.

### 1.1 V1 success criteria

V1 is complete only when all of the following are true:

- A Linux client can carry IPv4 TCP, UDP, and ICMP traffic through one Linux
  exit server.
- Two physical client interfaces can remain active in the same logical session.
- Low-rate latency-sensitive traffic is replicated across all healthy eligible
  paths and deduplicated correctly in both directions.
- Killing either path in the deterministic integration test creates no global
  DATA-sequence gap while the other path remains healthy and has capacity.
- A slow secondary path cannot build an unbounded queue or materially inflate
  the primary path's latency.
- Every accepted datagram is authenticated before replay, endpoint, metric, or
  dedup state is committed.
- Outer UDP fragmentation is avoided, and path-MTU reduction is handled.
- The client has explicit IPv6, DNS, LAN-bypass, and kill-switch behavior; it
  never silently calls a configuration "full tunnel" while leaking traffic.
- Route, nftables, TUN, and sysctl changes are idempotent and recoverable after
  a crash or restart.
- The parser, replay windows, dedup windows, scheduler, and session state
  machine have unit, fuzz, race, and namespace integration coverage.

### 1.2 V1 scope

- Linux client and Linux server.
- IPv4 inner traffic and IPv4 outer transport.
- One exit server.
- Up to four simultaneously active client-interface paths.
- Static operator-provisioned peers.
- One active logical session per peer by default.
- Multiple configured peers are supported by the server architecture, although
  the v1 release gate uses one client.
- Adaptive redundant scheduling: full replication at low load, bounded
  replication under bulk load.
- Plain UDP wire transport secured with Noise.

### 1.3 Non-goals for v1

- Throughput aggregation or bandwidth bonding.
- Multiple exit servers.
- Windows, macOS, Android, or iOS clients.
- IPv6 inner traffic.
- Application-specific split tunnelling.
- A latency-adding reorder/playout buffer.
- Traffic-shape obfuscation or censorship circumvention.
- Transparent operation through networks that block arbitrary UDP.
- Anonymous operation or protection from a trusted exit server.

## 2. Key decisions

| ID | Decision | Rationale |
|----|----------|-----------|
| D1 | TUN-based layer-3 VPN | The product must carry TCP, UDP, and ICMP without per-application integration. TAP/Ethernet support is unnecessary in v1. |
| D2 | Custom UDP data plane secured by `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` | RED_MPUDP needs explicit packet numbers, per-copy authentication, path feedback, bounded queues, and high-throughput datagrams. Noise replaces the unsafe ad hoc PSK handshake and key derivation. |
| D3 | One independent Noise handshake and key pair per path incarnation | This guarantees unique direction/path keys, makes rebind and rekey explicit, and prevents nonce reuse when an interface reconnects. |
| D4 | Global DATA sequence assigned before replication | Every path copy of one inner packet shares the same logical sequence, which is the dedup key. |
| D5 | Adaptive redundant scheduler with a pacer on every sending direction | All latency traffic and all traffic under light load are replicated. Every outer byte is congestion-accounted; bulk replication is suspended when a secondary path is queued, congested, over budget, or MTU-ineligible. |
| D6 | Maximum four active paths | Replication cost and state remain bounded. Four also covers the later two-interface by two-server topology. |
| D7 | No reorder buffer by default | First-arrival delivery is the latency objective. Reordering is measured so an optional, tightly bounded buffer can be justified later with data. |
| D8 | Per-interface policy routing using socket marks | `SO_BINDTODEVICE` alone is not a complete routing design. Marks and dedicated routing tables provide deterministic egress and prevent tunnel recursion. |
| D9 | PMTU handling is a v1 requirement | Outer fragmentation amplifies loss and can be black-holed. V1 starts conservatively and performs per-path datagram PMTU discovery. |
| D10 | Go implementation | Go is suitable for an initial implementation, but latency, allocation rate, GC behavior, and packet throughput are release measurements rather than assumptions. |
| D11 | nftables-owned rules, not generic appended iptables rules | A named table can be installed atomically, identified safely, and removed without touching operator rules. |
| D12 | QUIC DATAGRAM remains an alternate transport, not the v1 dependency | QUIC DATAGRAM has the correct unreliable semantics, but the current Go implementation documents an unoptimized high-throughput path and lacks key datagram feedback/size APIs. It must pass the Phase 0 benchmark before being reconsidered. |

## 3. Architecture

```text
                         CLIENT (Linux)

 apps -> kernel routes -> red0 TUN -> session engine
                                      | global DATA seq
                                      | classification
                                      | bounded replication
                          +-----------+-----------+
                          |                       |
                     path actor 0            path actor 1
                     Noise keys              Noise keys
                     tx/rx/replay            tx/rx/replay
                     PMTU/health             PMTU/health
                          |                       |
                    marked UDP socket       marked UDP socket
                    bound to wlan0          bound to wlan1
                          |                       |
                          +-----------+-----------+
                                      |
                                  Internet
                                      |
                          +-----------+-----------+
                          |      SERVER UDP       |
                          | parse -> lookup ->     |
                          | authenticate -> dedup |
                          +-----------+-----------+
                                      |
                                  red0 TUN
                                      |
                         nftables forward + NAT
                                      |
                                   Internet
```

A **logical session** belongs to one configured peer and owns the client's
tunnel address, the global uplink/downlink DATA sequence spaces, dedup windows,
and the set of active paths.

A **path incarnation** is one independently keyed `(client interface, server)`
association. Reconnecting, rebinding, or rekeying creates a new incarnation and
new Noise transport keys. Old and new incarnations may overlap briefly so a
path can be replaced without interrupting other paths.

## 4. Threat model and security properties

### 4.1 In scope

An attacker may observe, delay, drop, duplicate, replay, reorder, or inject UDP
datagrams. The server may receive arbitrary Internet traffic on its UDP port.
A client may attempt to spoof another configured client's tunnel address. The
implementation must remain memory- and state-bounded under malformed traffic.

### 4.2 Out of scope

- A compromised client or server host.
- Hiding packet timing, size, server address, or the fact that a custom UDP
  protocol is in use.
- Availability against an attacker able to drop all traffic.
- Privacy from the exit server.

### 4.3 Provisioned identity

Each peer has:

- An X25519 static private/public key pair.
- The server's pinned X25519 public key.
- A distinct random 32-byte pre-shared key.
- A server-assigned IPv4 tunnel address.

Key files contain one canonical, unpadded RFC 4648 standard-base64 value
decoding to exactly 32 bytes, followed by an optional newline. The CLI provides
`keygen`, `public-key`, and `psk` subcommands so operators never need to
improvise key formatting.

The server maps the full client public key to a peer configuration. A plaintext
8-byte `peer_id`, defined as the first eight bytes of SHA-256 over the client
public key, is only a lookup hint; the Noise handshake authenticates the full
key. Configuration loading rejects duplicate `peer_id` values.

Private keys and PSKs are read from separate files that must not be group- or
world-readable. Secrets are never accepted directly as CLI flags, exposed in
metrics, or logged.

### 4.4 Noise construction

- Protocol: `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s`.
- Prologue: the exact UTF-8 bytes `RED_MPUDP/v1`.
- The client knows and pins the server static public key.
- The server authenticates the client static public key against its peer table.
- The peer PSK is mixed at placement 2.
- Each path performs a fresh handshake with fresh ephemeral keys.
- The two Noise split cipher states provide distinct send and receive keys.
  The initiator uses the first split state to send and the second to receive;
  the responder uses the first to receive and the second to send.
- Transport nonces are explicit 64-bit packet numbers encoded in the packet
  header. The path actor sets the Noise cipher-state nonce to that authenticated
  packet number immediately before each encrypt/decrypt operation.
- Cipher state is owned by one path actor; it is never used concurrently.
- Ephemeral private keys, handshake state, and retired cipher states are erased
  on completion/close on a best-effort basis supported by the Go runtime.

For cheap rejection before Diffie-Hellman work, two domain-separated keys are
derived from the configured PSK with HKDF-SHA-256:

```text
salt        = SHA-256("RED_MPUDP/v1/key-schedule")
prk         = HKDF-Extract(salt, psk)
preauth_key = HKDF-Expand(prk, "RED_MPUDP/v1/preauth", 32)
noise_psk   = HKDF-Expand(prk, "RED_MPUDP/v1/noise-psk", 32)
```

`noise_psk` is supplied to Noise. `preauth_key` authenticates the handshake
envelope with HMAC-SHA-256 truncated to 16 bytes. This is not a replacement for
Noise authentication; it is a low-cost filter and peer lookup check.

## 5. Session and path lifecycle

### 5.1 State machines

Logical session states:

```text
DISCONNECTED -> OPENING -> ACTIVE -> DRAINING -> CLOSED
                    \          |
                     +-> FAILED+
```

Path-incarnation states:

```text
NEW -> HANDSHAKING -> VALIDATING -> HEALTHY <-> DEGRADED
           |              |            |            |
           +--------------+------------+------------+-> DEAD -> CLOSED
                                        |
                                        +-> DRAINING -> CLOSED
```

`HEALTHY` and `DEGRADED` are aggregate operator-facing states. Internally, each
active incarnation keeps separate send and receive health because a path may
fail in only one direction.

Only authenticated state-machine events may create a session, attach a path,
change a destination address, or close state.

### 5.2 Opening the first path

1. The client creates a random 128-bit `client_session_nonce` and random 64-bit
   `handshake_id`.
2. It creates the first interface-bound UDP socket and starts a Noise IKpsk2
   initiator handshake.
3. The encrypted Noise payload contains an `OPEN` request. The client does not
   request or choose its tunnel address.
4. The server verifies packet shape, peer lookup, retry cookie when required,
   and the pre-authentication tag before allocating handshake state or doing DH.
5. Noise authenticates the client's complete static public key. The server
   looks up its configured tunnel address and enforces the per-peer session
   limit.
6. The server creates a nonzero, collision-checked random 64-bit `session_id`,
   random 256-bit `join_token`, and nonzero, non-reused random 32-bit
   `path_token`.
7. The encrypted Noise response contains `OPEN_ACK` with those values, the
   assigned client/server tunnel addresses, negotiated probe interval, protocol
   limits, and base path MTU.
8. Both sides install the Noise split cipher states. The path enters
   `VALIDATING` and becomes `HEALTHY` after authenticated liveness and PMTU
   probe exchanges succeed in both directions.
9. In strict mode the kill switch is already active. Only after at least one
   path is healthy does the client install the full-tunnel default route.

### 5.3 Joining additional paths

1. The client creates a new socket, new Noise handshake, and new 64-bit
   `handshake_id` on the target interface.
2. The encrypted initiator payload contains `JOIN`, `session_id`, `join_token`,
   a random 128-bit `client_path_nonce`, and an optional token identifying the
   path incarnation being replaced.
3. The server accepts the join only when the authenticated client static key
   owns that active session and the join token matches in constant time.
4. The server enforces four scheduler-eligible paths. It permits one bounded
   validating replacement above that cap only when the replacement token names
   a path owned by the same session, then allocates a new `path_token`.
5. `JOIN_ACK` confirms the token and path parameters.
6. The path becomes scheduler-eligible only after validation succeeds.

The 256-bit join token is a bearer capability carried only inside an encrypted
Noise handshake. It is zeroed in memory when the logical session closes.

### 5.4 Handshake retransmission and denial-of-service bounds

- Handshake datagrams use exponential retransmission starting at 250 ms with
  jitter, capped at 2 seconds and five attempts.
- A retransmission reuses the exact Noise handshake message and
  `handshake_id`; it does not advance Noise state or generate another
  ephemeral key.
- The server keys its cache by `(peer_id, handshake_id)` and a hash of the exact
  `INIT`. It caches the response for five seconds so it can replay the identical
  response without repeating DH; reuse with different bytes is dropped.
- Pending handshake state expires after five seconds and is capped globally
  and per source prefix.
- Invalid peer IDs and invalid pre-authentication tags receive no response.
- A token bucket limits handshake work per source prefix and globally.
- By default the server sends a stateless `RETRY` before doing DH. The retry
  cookie is a 16-byte truncated HMAC over source address, source port, peer ID,
  handshake ID, and a hash of the invariant INIT fields/Noise message using a
  server secret rotated by a monotonic timer every two minutes. The cookie and
  pre-authentication tag fields themselves are excluded from that INIT hash.
  The server tries the current and immediately previous secret, so cookie
  validation does not depend on synchronized or non-jumping wall clocks.
- After decryption, repeated `client_session_nonce` values are rejected from a
  4,096-entry per-peer 24-hour cache, and repeated `client_path_nonce` values
  are rejected for the logical session's lifetime. Completed-handshake cache
  hits are handled before these replay checks.
- Responses sent to an unvalidated source never exceed the number of bytes
  received from that source.

### 5.5 Rekey

A path is replaced through a new `JOIN` handshake before either threshold:

- `rekey_after`, default one hour.
- `rekey_after_packets`, default `2^32` encrypted datagrams in either direction.

The new path incarnation receives a new token and new keys. Once healthy, it
replaces the old incarnation in the scheduler. The old path drains for
`max(3 * srtt, 1 second)` and is then destroyed. Path tokens are never reused
within a logical session, and zero is never assigned. If token allocation
cannot remain unique, the whole session is reopened.

### 5.6 Roaming and NAT rebinding

- A known local interface address change creates a new socket and performs a
  fresh `JOIN`; nonce counters are never reset under old keys.
- If an authenticated, fresh transport packet arrives from a new source
  address/port, the server records it as a candidate but does not immediately
  redirect unrestricted downlink traffic.
- Each path stores at most one candidate and one outstanding challenge; a newer
  authenticated candidate replaces the older unvalidated candidate without
  growing state.
- The server sends an unpredictable encrypted `PATH_CHALLENGE` to the candidate
  and switches the destination only after the matching `PATH_RESPONSE` arrives.
- Until validation, bytes sent to a candidate address are capped at three times
  the authenticated bytes received from it.
- Only a packet that passes AEAD and commits as fresh in the outer replay window
  may create or refresh a candidate.

### 5.7 Teardown and restart

- `CLOSE` is an encrypted control packet and is advisory; idle timeout is the
  authoritative cleanup mechanism.
- Server session idle timeout defaults to 120 seconds.
- Unknown or expired sessions are dropped silently. After all path probes fail,
  the client opens a new logical session with exponential backoff and jitter.
- A new `OPEN` from a peer that already has its configured maximum sessions
  atomically replaces the old session only after the new first path validates.
- At most one provisional replacement session per peer may exceed that limit;
  it cannot route DATA and expires after ten seconds if validation does not
  complete.
- Global DATA sequences reset only when a new logical session is created.

## 6. Wire protocol

All multi-byte integers are unsigned and big-endian. Parsers reject truncated
packets, excess bytes in fixed-size control payloads, nonzero reserved flags,
unknown mandatory types, and payloads larger than the configured maximum before
allocation.

The byte following `magic` distinguishes packet families without speculative
parsing: handshake packets use the complete byte `0x01` as their version;
transport packets use a high version nibble of `1`, so the byte is `0x10` through
`0x1f`. Handshake type values are `INIT=1`, `RESPONSE=2`, and `RETRY=3`.

### 6.1 Handshake envelope

Handshake packets are not transport packets and do not use a session key.

```text
magic[4]       = "RMCU"
version        u8 = 1
type           u8 = INIT | RESPONSE | RETRY
flags          u16 = 0
peer_id        u64
handshake_id   u64
cookie         [16]byte, all zero when absent
noise_len      u16
noise_message  [noise_len]byte
preauth_tag    [16]byte
```

The pre-authentication tag covers every preceding envelope byte and is present
on `INIT`, `RESPONSE`, and `RETRY`. For a retry, `noise_len` is zero and the
cookie is generated by the server. The cookie itself is opaque to the client,
but the client can authenticate the envelope with its `preauth_key`. A client
accepts a retry only when its tag, `peer_id`, and outstanding `handshake_id`
match; accepting it does not restart the overall attempt deadline. `noise_len`
is capped at 512 before allocation.

Noise payloads are compact binary structs:

```text
OPEN:
  operation              u8 = 1
  client_session_nonce   [16]byte
  requested_tun_mtu      u16
  requested_probe_interval_ms u16
  client_features        u32

JOIN:
  operation              u8 = 2
  session_id             u64
  join_token             [32]byte
  client_path_nonce      [16]byte
  replaces_path_token    u32, zero when adding a path

OPEN_ACK:
  result                 u8
  session_id             u64
  join_token             [32]byte
  path_token             u32
  client_tun_ipv4        [4]byte
  server_tun_ipv4        [4]byte
  tun_mtu                 u16
  base_outer_mtu          u16
  probe_interval_ms       u16
  server_features        u32

JOIN_ACK:
  result                 u8
  session_id             u64
  path_token             u32
  base_outer_mtu          u16
  probe_interval_ms       u16
  server_features        u32
```

The server selects `tun_mtu` no higher than either endpoint's configured limit;
the server installs the value as a per-peer route MTU. The negotiated probe
interval is the greater of the client request and server-configured interval,
clamped to 100–5000 ms; joined paths inherit the session value.
`base_outer_mtu` is exactly 1200 in v1. The
exact encoded lengths are constants and are asserted by tests. A nonzero result
contains only a numeric error code; detailed authentication failures are not
exposed on the wire. In v1, all feature fields and unknown operation values
must be zero/rejected respectively; feature bits become usable only after their
semantics and downgrade behavior are specified. The cookie is zero in the first
`INIT` and every `RESPONSE`, nonzero in `RETRY`, and copied into the retried
`INIT`. All fields following a nonzero ACK result are zero.

ACK results are `OK=0`, `BUSY=1`, `SESSION_LIMIT=2`, `PATH_LIMIT=3`,
`NO_SESSION=4`, `INVALID_JOIN=5`, and `MTU_UNSUPPORTED=6`. They are available
only inside a successfully authenticated Noise response.

### 6.2 Transport datagram

Every established-path datagram is:

```text
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+---------------------------------------------------------------+
|                         magic = "RMCU"                        |
+---------------+---------------+-------------------------------+
| ver(4)|type(4)|     flags     |         header_len = 44       |
+---------------------------------------------------------------+
|                         session_id (64)                       |
+                                                               +
|                                                               |
+---------------------------------------------------------------+
| path_token (32)                                               |
+---------------------------------------------------------------+
|                      outer_packet_no (64)                     |
+                                                               +
|                                                               |
+---------------------------------------------------------------+
|                        logical_seq (64)                       |
+                                                               +
|                                                               |
+---------------------------------------------------------------+
|                          path_seq (64)                        |
+                                                               +
|                                                               |
+---------------------------------------------------------------+
|              ciphertext(payload) + AEAD tag (16)             |
+---------------------------------------------------------------+
```

The fixed authenticated header is 44 bytes. The explicit nonce is derived from
`outer_packet_no` and is not transmitted separately. Transport overhead over
the inner packet is 60 bytes: 44-byte header plus 16-byte tag. Including outer
IPv4 and UDP, total overhead is 88 bytes per copy.

Fields:

| Field | Meaning |
|-------|---------|
| `ver` | Protocol version, 1. |
| `type` | DATA, PING, PONG, PATH_REPORT, PATH_CHALLENGE, PATH_RESPONSE, PMTU_PROBE, PMTU_ACK, or CLOSE. |
| `flags` | Reserved in v1; must be zero. |
| `header_len` | Allows future authenticated header extension; exactly 44 in v1. |
| `session_id` | Server-generated logical-session routing identifier. |
| `path_token` | Non-reused path-incarnation identifier and key lookup. |
| `outer_packet_no` | Per-path, per-direction AEAD nonce and replay sequence; increments for every transport datagram. |
| `logical_seq` | Global DATA sequence for DATA; report, probe, or challenge identifier for related controls; otherwise zero. |
| `path_seq` | Per-path DATA sequence for delivery/loss measurement; zero for control packets. |

Transport type values are `DATA=0`, `PING=1`, `PONG=2`, `PATH_REPORT=3`,
`PATH_CHALLENGE=4`, `PATH_RESPONSE=5`, `PMTU_PROBE=6`, `PMTU_ACK=7`, and
`CLOSE=8`. Values 9–15 are reserved and rejected in v1.

Global DATA, per-path DATA, and per-reported-path report sequences start at one;
zero is reserved for control packets without an identifier. Each transport direction starts
`outer_packet_no` at zero after a fresh handshake. Probe and challenge IDs are
cryptographically random nonzero 64-bit values and are tracked until completion
or timeout. Exhausting any counter closes/rekeys before reuse; counters never
wrap.

Each replicated DATA copy has the same `logical_seq`, but a different
`path_token`, `outer_packet_no`, and `path_seq`. Therefore each copy is sealed
separately. "Seal once and edit the header" is forbidden.

### 6.3 Packet types

| Type | Payload | Behavior |
|------|---------|----------|
| `DATA` | One complete inner IPv4 packet | Authenticate, update path metrics, global-dedup, validate IP, write first copy to TUN. |
| `PING` | Empty | `logical_seq` is a random probe ID. Reply immediately on the same path. |
| `PONG` | Empty | Matches a locally outstanding probe ID; one RTT sample per ID. |
| `PATH_REPORT` | Fixed-size delivery, race, and PMTU summary | Reports receiver-observed loss/race results and this endpoint's confirmed send MTU for the path. |
| `PATH_CHALLENGE` | Empty | `logical_seq` is an unpredictable challenge. |
| `PATH_RESPONSE` | Empty | Echoes the challenge on the candidate path. |
| `PMTU_PROBE` | Random padding to requested outer size | Never carries application data. |
| `PMTU_ACK` | Confirmed probe size | Raises the sender's path MTU only for the acknowledged size. |
| `CLOSE` | Reason code | Graceful path/session teardown; never relied on for cleanup. |

### 6.4 Control payload encodings

Control payload lengths are fixed except for `PMTU_PROBE`. Empty means exactly
zero plaintext bytes. The v1 binary layouts are:

```text
PATH_REPORT (88 bytes):
  largest_path_seq       u64
  received_bitmap        [32]byte
  received_data_packets  u64
  received_outer_bytes   u64
  declared_lost_packets  u64
  first_arrivals         u64
  unique_rescues         u64
  confirmed_send_outer_mtu u16
  report_flags           u16
  reported_path_token    u32

PMTU_PROBE (variable):
  requested_outer_size   u16
  random_padding         [requested_outer_size - 90]byte

PMTU_ACK (2 bytes):
  confirmed_outer_size   u16

CLOSE (4 bytes):
  scope                   u8 = PATH(1) | SESSION(2)
  reserved                u8 = 0
  reason                  u16
```

In `received_bitmap`, bit zero represents `largest_path_seq`, and bit `n`
represents `largest_path_seq - n`; bit `n` is mask `1 << (n % 8)` in byte
`n / 8`. When no DATA has arrived, the largest value and bitmap are zero. The
counters are cumulative for one path incarnation and count authenticated DATA
copies before global deduplication.
`received_outer_bytes` counts each complete v1 outer datagram as the received
UDP payload length plus 28. Bit zero of `report_flags` means the reported send
MTU has completed validation; every other bit is zero in v1. The report's
authenticated outer packet number supplies freshness; counter deltas and local
elapsed time supply delivery rate. A report is also sent immediately when the
confirmed MTU or validation flag changes. `reported_path_token` identifies the
path incarnation whose counters and MTU are described; it must be nonzero and
belong to the same logical session as the path carrying the report.
`logical_seq` is the monotonically increasing report sequence scoped to that
reported path. A duplicate copy sent over another carrier path keeps the same
report sequence; receivers ignore reports older than the newest processed value
so cumulative counters cannot regress.

For a PMTU probe, 90 is the 28-byte outer IPv4/UDP header, 44-byte transport
header, 16-byte AEAD tag, and 2-byte size field. `logical_seq` is a random probe
ID. The receiver sends `PMTU_ACK` only when the declared size equals the
received UDP payload length plus 28, the v1 IPv4/UDP outer-header size. The
sender raises its confirmed size only when the authenticated ACK's ID and size
match an outstanding probe on that path.

PING, PONG, PATH_CHALLENGE, and PATH_RESPONSE have empty payloads. Their
`logical_seq` values carry the probe or challenge identifier as defined above.
CLOSE reasons are `NORMAL=0`, `REKEYED=1`, `IDLE=2`, `SHUTDOWN=3`,
`PROTOCOL=4`, and `INTERNAL=5`. Reserved CLOSE scopes/reasons and malformed
fixed lengths are rejected.

### 6.5 Receive order

For an established transport packet, the receiver performs these steps exactly:

1. Check minimum/maximum length, magic, version, type, flags, and header length.
2. Look up `(session_id, path_token)` without modifying state.
3. Perform a non-mutating replay precheck; packets definitely older than the
   outer window may be discarded, but no sequence state is advanced.
4. Set the receive cipher nonce to `outer_packet_no` and authenticate/decrypt
   with the complete 44-byte header as AAD.
5. Commit `outer_packet_no` to the path replay window.
6. Update authenticated path liveness and candidate-endpoint observations.
7. Process path sequence/loss/race metrics, including later DATA copies.
8. For DATA, apply global logical-sequence dedup.
9. Validate inner IPv4 version, total length, and source/destination policy.
10. Write only the first accepted logical DATA packet to TUN.

No unauthenticated packet may advance a replay/dedup window, refresh a session,
change an endpoint, create a path, or affect path scoring.

## 7. Replay, dedup, and reordering

### 7.1 Outer replay

Each path direction owns `highest_outer_packet_no` and a 4096-bit replay bitmap.
This window protects all packet types. The path actor authenticates before
committing a new number. Reordering within the window is accepted; duplicate or
older authenticated packets are dropped.

### 7.2 Global DATA dedup

Each session direction owns:

- `highest_data_seq`.
- A configurable bitmap, default 65,536 sequence numbers.
- A ring recording first-arrival path and monotonic arrival time for race
  metrics.

The window must be at least:

```text
peak_packets_per_second * maximum_supported_path_skew_seconds + safety margin
```

The default bitmap costs 8 KiB per direction before race metadata. Operators
may configure 4,096 through 1,048,576 entries. Memory limits are checked before
sessions are admitted.

DATA processing results are `fresh`, `duplicate`, or `too_old`. There is no
sequence wraparound: reaching `MaxUint64` closes and reopens the session.

### 7.3 Reordering

Fresh packets are written to TUN in first-arrival order. V1 does not delay a
packet waiting for a missing sequence. Metrics record:

- Out-of-order packet count.
- Maximum and histogrammed reorder depth.
- First-arrival path per logical packet.
- Arrival delta for later copies.
- Per-path winner rate.
- Per-path unique-rescue estimate after the observation horizon.

The race ring records a received-path bitset for each logical sequence. Its
observation horizon is
`clamp(max(3 * largest_current_srtt, 250 ms), 250 ms, 2 seconds)`. A packet is a
unique rescue for its first path only when no other copy has arrived by that
horizon. If an entry must be evicted before finalization, it increments an
`observation_unknown` counter and is not claimed as a rescue.

An optional reorder buffer is a future change and requires measured evidence,
a configured maximum delay, and separate treatment for latency-class traffic.

## 8. Path measurement and health

### 8.1 Directional metrics

Each endpoint maintains its own outbound and inbound view; uplink quality is not
assumed to equal downlink quality.

| Metric | Source | Purpose |
|--------|--------|---------|
| `srtt` / `rttvar` | One sample per unique PING/PONG ID | Liveness, scoring, rekey drain time. |
| `min_rtt` | Minimum recent valid RTT | Queue-delay baseline. |
| `queue_delay` | `max(0, srtt - min_rtt)` | Replica circuit breaker. |
| `inbound_loss` | Reorder-tolerant gaps in `path_seq` | Receiver path quality. |
| `remote_loss` | Peer `PATH_REPORT` | Sender view of its outbound delivery. |
| `delivery_rate` | Bytes confirmed by peer reports per interval | Replica pacing/budget assistance. |
| `last_valid_recv` | Any fresh authenticated datagram | Receive liveness. |
| `last_probe_ack` | Matching PONG | Round-trip liveness. |
| `winner_rate` | First-arrival observations | Path racing value. |
| `rescue_rate` | Packets observed only on this path by horizon | Marginal redundancy value. |

Repeated PONGs for the same ID do not create additional RTT samples. Wall-clock
time is never compared across hosts; only local monotonic send records are used.
SRTT/RTTVAR use the RFC 6298 smoothing constants, while `min_rtt` is the minimum
valid sample in a rolling 60-second window and resets on a new incarnation.
`feedback_stale_after` is
`max(3 * srtt, 3 * active_report_interval, 250 ms)`. Probe timeout is
`clamp(srtt + 4 * rttvar, 250 ms, 2 seconds)`.

### 8.2 Probes and reports

- Each endpoint sends a PING on every active path at `probe_interval`, default
  250 ms, unless a matching probe has completed more recently.
- Probes are not disabled merely because DATA is flowing.
- PONG is sent immediately on the same path and uses the high-priority control
  queue.
- Under DATA load, `PATH_REPORT` is sent after 32 received DATA packets or the
  v1 active-report interval of 20 ms, whichever happens first. When idle, the v1
  interval is 500 ms. Counter deltas, not remote timestamps, calculate delivery
  rate.
- Report delivery is independent of the path being described. The session
  normally uses the best control-capable path and switches to an alternate when
  the reported path is asymmetric or stale. This lets feedback for a working
  uplink arrive over a different working downlink.
- Missing `path_seq` values are not declared lost until they leave a
  4096-entry reorder-tolerant window or exceed `max(3 * srtt, 250 ms)`.

### 8.3 Health states

For each incarnation:

- Receive health comes from fresh authenticated packets arriving on that path.
- Send health comes from the peer's report about that path, even when the report
  itself arrived over another path.
- A direction is `HEALTHY` after validation and recent positive evidence,
  `DEGRADED` when loss, queue delay, report staleness, or send errors exceed
  thresholds, and `DEAD` after `dead_after_missed_probes` consecutive probe
  timeouts without authenticated directional evidence.
- The whole incarnation becomes `DEAD` immediately on a socket/interface or
  Noise-state failure, or when both directions are dead. A one-way path remains
  available to the functioning direction and for bounded validation probes.

Loss alone does not immediately make a direction dead; a lossy independent path
may still rescue packets. Congestion and queue-delay circuit breakers may make
its send direction ineligible for replicas while probes continue.

### 8.4 Score

The v1 primary-path score is:

```text
score = srtt
      + 2 * rttvar
      + remote_loss * 200ms
      + queue_delay * 2
      - active_hysteresis_bonus
```

The default hysteresis bonus is 5 ms. Paths with stale feedback receive an
additional penalty. With two healthy low-load paths, both are still selected;
the score mainly chooses the primary and matters when paths are capped or
replication is budget-limited. A healthy primary is held for at least one
second and is switched afterward only when another path remains better by more
than the hysteresis bonus for two consecutive evaluations. Ineligibility or
failure bypasses the dwell period.

Future multi-server selection must also maximize failure-domain diversity; it
must not select several low-score paths that share the same interface, gateway,
server, or transit when a more independent alternative exists.

## 9. Scheduler, congestion safety, and queues

### 9.1 Traffic classes

V1 classifies inner IPv4 packets into two scheduling classes without changing
their routing destination:

`latency`:

- ICMP echo request/reply.
- Non-fragmented UDP whose complete inner IPv4 total length is at or below
  `latency_packet_max_bytes`, default 768 bytes.
- TCP SYN, FIN, and RST packets.
- Packets with a configured latency DSCP value.

`standard`:

- All other IPv4 traffic, including fragments that cannot be classified
  safely.

Classification controls duplication and queue priority, not whether a packet
uses the VPN. Split tunnelling remains out of scope.

### 9.2 Scheduling algorithm

For every inner packet read from TUN:

```text
data_seq = next_global_data_seq()
class = classify(inner_packet)
eligible = send-healthy paths whose confirmed PMTU can carry the packet

if eligible is empty:
    fallback = best validated DEGRADED path whose PMTU can carry the packet
    if fallback's congestion controller permits DATA:
        enqueue a bounded primary copy
    else:
        drop with a no-path metric
else:
    primary = lowest-score eligible path
    enqueue primary copy with primary deadline

    replicas = eligible minus primary, ranked by:
        health, score, queue delay, winner rate, rescue rate

    for path in replicas[:max_paths-1]:
        if replica_allowed(path, class, packet_size):
            enqueue an owned copy with a replica deadline
```

The path actor assigns `path_seq` and `outer_packet_no` and seals the copy only
when it leaves the queue for transmission. A local queue/deadline drop therefore
does not appear to the peer as network loss. Every selected path still seals
independently under its own key and nonce.

`replica_allowed` requires all of:

- The path's send direction is `HEALTHY`.
- Queue sojourn estimate is within `replica_queue_budget`, default 5 ms.
- Probe/report feedback is fresh.
- PMTU can carry the complete datagram.
- The class-specific token bucket has capacity.
- Queue-delay circuit breaker is not open.

At low traffic rates these conditions are normally true, so every packet is
replicated exactly as in the original redundant design.

### 9.3 Per-path queues

Every path has separate control, latency, primary-data, and replica-data queues.
Service priority is:

```text
control > latency > primary data > replica data
```

Rules:

- All queues are bounded by packets and bytes.
- Control packets are never placed behind data packets.
- Expired replica packets are dropped; they are never sent late merely to empty
  a queue.
- A full replica queue drops the new replica copy without affecting the primary.
- A full primary queue applies bounded backpressure up to its deadline, then
  drops tail so inner TCP/transport can observe congestion.
- TUN ingress is never backed by an unbounded channel.
- One blocked or failed path cannot block another path's tx worker.
- Queue drops are labeled by path, class, and reason in metrics.

Default limits:

| Setting | Default |
|---------|---------|
| `primary_queue_packets` | 1024 |
| `primary_queue_bytes` | 2 MiB |
| `primary_queue_deadline` | 50 ms |
| `replica_queue_packets` | 256 |
| `replica_queue_bytes` | 512 KiB |
| `replica_queue_budget` | 5 ms |
| `latency_replica_budget_mbps` | 5 per secondary path |
| `standard_replica_budget_mbps` | 10 per secondary path |

Defaults are starting points and must be validated on the real test bed.

### 9.4 Circuit breaker

Replica sending is suspended for a path when any condition holds:

- `queue_delay` exceeds 15 ms and is at least twice the recent baseline.
- Remote reports are older than `feedback_stale_after`.
- Replica queue remains above its budget for two consecutive checks.
- The configured token bucket is exhausted.
- PMTU black-hole detection is active.

The path continues receiving probes. Replica eligibility returns only after a
one-second cooldown and two healthy probe/report intervals. The primary is not
removed solely because a replica circuit breaker opened.

This is a safety envelope, not a complete new congestion-control algorithm.
It prevents masked congestion on secondary copies from creating unlimited load.

### 9.5 Per-direction congestion controller and pacer

Queue bounds and replica budgets are not sufficient for a general IP-over-UDP
tunnel: inner UDP may have no congestion control, and every replica consumes
real network capacity. Each path therefore has an independent controller for
each sending direction. Every DATA copy—primary or replica—and every PMTU probe
is paced through it and charged at its complete outer size. Other control
traffic is charged to the separate bounded control bucket.

The v1 controller is deliberately conservative and testable:

- It starts at `initial_pacing_rate_mbps`, default 1 Mbit/s.
- An operator safety cap, `max_pacing_rate_mbps`, defaults to 100 Mbit/s per
  path and may be lowered per interface.
- Once per RTT with fresh reports, no newly finalized loss, and queue delay
  below the target, the controller applies
  `pacing_rate += confirmed_outer_mtu / srtt` bytes per second.
- Newly finalized loss halves the rate at most once per RTT. Queue delay above
  the 15 ms target for two feedback rounds also halves it.
- Missing feedback for `feedback_stale_after` marks the sending
  direction degraded and reduces it to the minimum data rate. Only control
  traffic can bypass the DATA pacer, through a separate token bucket sized to
  `max(64 Kbit/s, 10% of the current DATA rate)` and capped at 1 Mbit/s. Reports
  are coalesced and probe responses are rate-limited when that bucket is full.
- Rates are clamped to configured minimum/maximum values. Changing a path
  incarnation resets the controller; it never inherits an optimistic rate.
- Replica token buckets are an additional limit, not a substitute for pacing.

This rate-based AIMD controller must coexist fairly with a long-lived TCP flow
in the Phase 0/6 netem tests. If it cannot meet the congestion, fairness, and
latency gates, the transport decision is reopened in favor of an established
congestion-controlled datagram transport; shipping an unpaced custom UDP data
plane is not an option.

## 10. Path MTU

- V1 never intentionally sends an outer fragmented UDP datagram.
- Sockets set IPv4 DF behavior and consume `EMSGSIZE`/validated ICMP information
  when available.
- An error-queue ICMP packet is considered only when its quoted UDP tuple maps
  to the socket/path. It may lower the next probe ceiling and temporarily block
  oversized DATA, but it never raises or permanently commits PMTU; authenticated
  probe results make that decision. A local synchronous `EMSGSIZE` immediately
  blocks the failed size.
- The default TUN MTU is 1180. With the v1 60-byte transport overhead and
  28-byte outer IPv4/UDP overhead, this produces a 1268-byte outer packet. This
  is a deployment baseline, not a universal IPv4-path guarantee.
- Every path first confirms the 1200-byte v1 base outer size, then probes the
  size required by the negotiated TUN MTU using encrypted
  `PMTU_PROBE`/`PMTU_ACK` packets.
- A probe is padded to an exact candidate outer size. Only an ACK for that
  unique probe and size raises the confirmed path MTU.
- Each candidate gets three attempts using the path probe timeout. After the
  base and required size, further search bisects the remaining range on an
  8-byte boundary. Only one candidate probe is outstanding per direction.
- The current size is revalidated every 60 seconds and after `EMSGSIZE`, a valid
  PTB hint, or three finalized losses concentrated at the largest packet-size
  bucket. If current-size probes fail while a smaller probe succeeds, black-hole
  recovery immediately lowers the confirmed size. Upward probing after a
  reduction is limited to once every ten minutes.
- Before installing the tunnel default route, the first path must confirm the
  outer size required by the negotiated TUN MTU in both directions. If it
  cannot, the provisional session closes and reopens with a requested TUN MTU
  no larger than the smaller reported confirmed size minus 88, within configured
  safety bounds, or startup fails closed. It never sends an oversized DATA
  packet to discover the limit accidentally.
- Application DATA must not exceed a path's confirmed payload limit.
- The configured TUN MTU is capped at the minimum size required for the desired
  redundant path set. A newly joined smaller path remains probe-only for
  oversized DATA until the operator allows a lower TUN MTU or PMTU succeeds.
- If every active path later falls below the negotiated size, DATA pauses in
  bounded queues while the client revalidates or reopens a lower-MTU session;
  the kill switch remains enforced during that transition.
- The engine reports `replica_mtu_ineligible` rather than causing outer
  fragmentation.

Path-MTU behavior follows RFC 8899. Increasing the default TUN MTU above 1180 is
allowed only after integration tests cover PTB delivery and black-hole cases.
V1 requires an outer path capable of a 1200-byte UDP datagram, so the minimum
negotiated inner MTU is 1112 bytes; the configured v1 range is 1112–1400.

## 11. Linux networking

### 11.1 Client policy routing

For every configured physical interface, startup discovers and records its
ifindex, source address, connected prefixes, gateway, and effective MTU before
installing the tunnel default route.

Each path socket receives:

- `SO_BINDTODEVICE` for its interface.
- A unique `SO_MARK` value.
- An explicit local source address when available.

Each mark has a dedicated `ip rule` and route table containing that interface's
connected routes and default gateway. Mark rules have higher priority than the
unmarked full-tunnel rule. Consequently, RED_MPUDP's own UDP packets cannot
recurse through `red0`, even when every path targets the same server IP.

The default derived rule order is:

```text
10000..10003  fwmark for path N -> path N table
10100..10999  narrowly scoped DHCP and enabled LAN exemptions -> main table
11000         otherwise -> tunnel table containing default dev red0
32766         existing main-table fallback
```

Priorities are based at the configured `rule_priority_base` and are collision
checked. RED_MPUDP does not replace the main-table default route. When
`allow_lan` is false, connected-prefix exemptions are omitted; only narrowly
matched IPv4 DHCP client traffic receives a main-table exemption, and the kill
switch independently enforces the same allowance.

Netlink changes are transactional and identified by configured table IDs,
marks, and rule priorities. The interface watcher updates gateway/source state
and creates a new path incarnation; it does not reset counters on an existing
key.

Startup audits reverse-path filtering. On path interfaces it changes strict
`rp_filter=1` to loose mode `2` when required for policy-routed multihoming and
enables `net.ipv4.conf.all.src_valid_mark=1` so marked reverse-path lookups and
ICMP errors use the same policy. These sysctl changes use the mutation journal,
conditional restoration rules, and namespace tests; failure to apply a required
setting aborts before the tunnel route is enabled.

### 11.2 Full-tunnel behavior

V1 defines full tunnel as all non-exempt IPv4 traffic. Configuration explicitly
controls:

- `allow_lan`: whether connected LAN prefixes bypass the VPN.
- `kill_switch`: whether physical interfaces may carry non-RED_MPUDP traffic
  while the tunnel is active.
- `ipv6_policy`: `block` by default; `passthrough` must be explicitly selected
  and is reported as a leak-capable mode.
- DNS management and restoration.

When DNS management is enabled, v1 uses systemd-resolved's D-Bus API to make the
TUN the default DNS route and restores prior per-link settings on shutdown. It
does not overwrite `/etc/resolv.conf`. Configured DNS server IPv4 traffic
follows the tunnel. If systemd-resolved is unavailable, strict mode fails
startup rather than silently leaking link-specific DNS; the operator may instead
disable managed DNS and provide an explicitly audited resolver configuration.

In strict full-tunnel mode, the kill switch is installed after configuration
validation and path sockets are prepared, but before the first handshake and
before the default route is changed. This prevents application traffic from
escaping during path establishment while still allowing marked RED_MPUDP
handshakes. Its nftables output rules allow:

- Loopback.
- RED_MPUDP marked UDP to the configured server endpoint.
- Required DHCP traffic.
- Explicit LAN prefixes only when `allow_lan` is true.
- Traffic through `red0`.

All other physical-interface IPv4 traffic and, under the default policy, IPv6
traffic are rejected. Rules live in a dedicated named nftables table.

### 11.3 Server forwarding and NAT

The server owns a single TUN and maintains both maps:

```text
session_id -> session state
assigned_tunnel_ipv4 -> session_id
```

On authenticated uplink DATA, the server verifies that the inner IPv4 source is
exactly the address assigned to that peer before writing to TUN. It rejects
spoofed source addresses, invalid lengths, broadcast/multicast source addresses,
and unauthorized peer-to-peer tunnel traffic.

Both endpoints require IPv4 version 4, `IHL >= 5`, an exact payload/total-length
match, a valid header checksum, and internally consistent fragment fields. The
client additionally requires downlink destination to equal its assigned tunnel
address. The server allows arbitrary non-tunnel destinations after source
validation, but rejects the tunnel subnet unless an explicit future peer-routing
policy authorizes it. Valid IPv4 fragments are carried as `standard` traffic.

On a packet read from server TUN, the destination tunnel IPv4 selects the
logical session. Unknown destinations are dropped. A per-peer host route carries
that session's negotiated MTU so the kernel can fragment when permitted or
generate the correct inner ICMP fragmentation-needed response.

When `manage_nat` is enabled, RED_MPUDP:

- Enables IPv4 forwarding while recording whether it changed the sysctl.
- Installs an atomic named nftables table with scoped forward, established
  return, and masquerade rules.
- Never flushes or edits unrelated operator chains.
- Reconciles stale rules from its own table at startup.
- Removes only its own table on graceful shutdown.

Production deployments should preferably configure forwarding/NAT through the
host's persistent network configuration and set `manage_nat: false`.

### 11.4 Privileges

The process requires `CAP_NET_ADMIN`; target kernels and socket-rebind behavior
must also be tested with `CAP_NET_RAW`. Running as root is supported for
development but not the preferred deployment. File capabilities should be
limited to the final binary, key files should be mode `0600`, and metrics should
listen on loopback by default.

The release ships hardened systemd units using a dedicated service account,
`RuntimeDirectory=red-mpudp`, a capability bounding set, device access limited
to `/dev/net/tun`, and a narrow authorization for the required
systemd-resolved D-Bus methods. Strict DNS startup fails if that authorization
is absent; the daemon never falls back to broad resolver-file writes.

A separate privileged network helper is a post-v1 hardening option. Until then,
all netlink and nftables inputs are structured values, never shell-concatenated
commands.

### 11.5 Mutation journal and recovery

Before its first host-network mutation, each client or managed-NAT server writes
an atomic, mode-0600 state journal under `/run/red-mpudp/`. It records the role,
instance ID, exact owned route/rule/table identifiers, prior resolver state, and
prior sysctl values. The journal contains no cryptographic secrets.

- Apply/reconcile operations are idempotent.
- Graceful shutdown removes the tunnel default route first, restores resolver
  state, removes only owned rules/routes, and removes the kill switch last.
- A sysctl is restored only if its current value still equals the value this
  process installed; otherwise the process logs a conflict and preserves the
  operator's newer value.
- With a configured kill switch, an unexpected daemon exit intentionally leaves
  the dedicated nftables table fail-closed. The next start reconciles it before
  opening traffic.
- `red-mpudp cleanup --state-file <path>` performs the same ownership-checked
  recovery when the daemon cannot restart. It refuses a missing, malformed, or
  mismatched journal instead of deleting broad networking state.

## 12. Concurrency and ownership

### 12.1 Client

- **TUN reader:** reads owned packet buffers and sends them to the session
  engine through a bounded ingress queue.
- **Session engine:** the only owner of global DATA sequences, dedup state,
  classification, scheduler decisions, and the current path snapshot.
- **Path actor per incarnation:** owns its socket, Noise send/receive cipher
  states, outer replay windows, path sequences, endpoint, PMTU, queues, and path
  metrics.
- **TUN writer:** writes accepted buffers to `red0` and returns them to the pool.
- **Netlink watcher:** converts link/address/route changes into session-engine
  events; it never mutates path state directly.
- **Timer source:** sends tick events; it never flips health or recomputes state
  independently.
- **Metrics collector:** reads immutable snapshots and does not share mutable
  hot-path objects.

### 12.2 Server

- One UDP receive loop reads into pooled buffers, validates the minimal header,
  and dispatches by `(session_id, path_token)`.
- Unknown-session transport traffic is dropped before allocation.
- Handshake traffic goes to a bounded handshake worker pool.
- Established traffic is sharded by session so packets for one session retain
  single-owner state without a global hot-path mutex.
- One TUN reader parses the inner destination and dispatches to the owning
  session shard.
- Per-path tx queues isolate slow clients and paths.

### 12.3 Buffer and drop rules

- Packet-buffer ownership is transferred exactly once across a channel.
- A pool buffer is returned by the final consumer on every success/error path.
- Channels are bounded and expose capacity/drop metrics.
- `drop-oldest` is not a universal policy. Only expired replica work may be
  discarded as stale; primary and control paths use their explicit policies.
- Control events have a separate bounded queue so DATA floods cannot starve
  liveness, close, PMTU, or rekey processing.

## 13. Transport and wire wrappers

V1 uses plain UDP after RED_MPUDP encryption. The engine does not use the old
`Send([]byte)`/`Recv() []byte` abstraction because the server requires peer
addresses, receive metadata, bounded buffers, and MTU information.

```go
type Endpoint struct {
    AddrPort netip.AddrPort
    IfIndex  int
}

type ReceiveMeta struct {
    Source    netip.AddrPort
    LocalAddr netip.Addr
    IfIndex   int
    Truncated bool
}

type PathError struct {
    Peer      netip.AddrPort
    MTU       int
    Local     bool // synchronous/local error rather than ICMP error queue
}

type DatagramIO interface {
    ReadInto(ctx context.Context, dst []byte) (n int, meta ReceiveMeta, err error)
    WriteTo(ctx context.Context, pkt []byte, dst Endpoint) error
    ReadPathError(ctx context.Context) (PathError, error)
    MaxDatagramSize() int
    Close() error
}
```

The client implementation wraps one connected/bound UDP socket per path. The
server implementation wraps its shared listen socket. The crypto/session layer,
not `DatagramIO`, owns authentication and endpoint validation. UDP reads use
`recvmsg`/equivalent metadata so `MSG_TRUNC` is never mistaken for a complete
datagram and error-queue events can be mapped back to a path without parsing
attacker-controlled strings.

A future post-encryption wire wrapper must declare exact overhead, maximum
datagram size, handshake/state requirements, and whether it changes addressing.
`FakeTLS` is removed: TLS 1.3 records over UDP are not legitimate TLS traffic
and are readily distinguishable. Real QUIC, DTLS, MASQUE, or a separately
specified pluggable transport may be evaluated later.

## 14. Configuration

Durations use Go duration strings. Secret values are file references.

### 14.1 Client

```yaml
server: "203.0.113.10:51820"

identity:
  private_key_file: "/etc/red-mpudp/client.key"
  server_public_key_file: "/etc/red-mpudp/server.pub"
  psk_file: "/etc/red-mpudp/client.psk"

interfaces:
  - name: "wlan0"
    fwmark: 0x524d0001
    routing_table: 201
    gateway: "auto"
    max_pacing_rate_mbps: 100
  - name: "wlan1"
    fwmark: 0x524d0002
    routing_table: 202
    gateway: "auto"
    max_pacing_rate_mbps: 50

tun:
  name: "red0"
  mtu: 1180

routing:
  manage: true
  full_tunnel: true
  tunnel_table: 200
  rule_priority_base: 10000
  allow_lan: false
  kill_switch: true
  ipv6_policy: "block"       # block | passthrough

dns:
  manage: true
  servers: ["1.1.1.1", "1.0.0.1"]
  strict: true

scheduler:
  mode: "adaptive-redundant"
  max_paths: 4
  latency_packet_max_bytes: 768
  latency_replica_budget_mbps: 5
  standard_replica_budget_mbps: 10
  replica_queue_budget: "5ms"
  primary_queue_deadline: "50ms"

health:
  probe_interval: "250ms"
  dead_after_missed_probes: 3

congestion:
  initial_pacing_rate_mbps: 1
  min_pacing_rate_kbps: 128
  max_pacing_rate_mbps: 100
  queue_delay_target: "15ms"

session:
  rekey_after: "1h"
  rekey_after_packets: 4294967296
  dedup_window_packets: 65536

metrics_addr: "127.0.0.1:9090"
log_level: "info"
```

### 14.2 Server

```yaml
listen: "0.0.0.0:51820"

identity:
  private_key_file: "/etc/red-mpudp/server.key"

peers:
  - name: "laptop"
    public_key_file: "/etc/red-mpudp/peers/laptop.pub"
    psk_file: "/etc/red-mpudp/peers/laptop.psk"
    tunnel_ip: "10.9.0.2"
    max_sessions: 1

tun:
  name: "red0"
  address: "10.9.0.1/24"
  subnet: "10.9.0.0/24"
  mtu: 1180

network:
  manage_nat: true
  wan_interface: "eth0"

limits:
  max_paths_per_session: 4
  max_sessions: 64
  max_pending_handshakes: 256
  max_pending_per_source: 8
  session_idle_timeout: "120s"
  retry_mode: "always"        # always | auto; never is test-only

health:
  probe_interval: "250ms"
  dead_after_missed_probes: 3

congestion:
  initial_pacing_rate_mbps: 1
  min_pacing_rate_kbps: 128
  max_pacing_rate_mbps: 100
  queue_delay_target: "15ms"

metrics_addr: "127.0.0.1:9090"
log_level: "info"
```

Configuration validation occurs before any network mutation. It rejects bad
key lengths/permissions, overlapping peer IPs, invalid subnets, duplicate
interfaces/marks/tables, unsafe MTUs/rates, a non-literal or invalid IPv4 server
endpoint, path caps outside 1–4, and metrics listeners that are public without
explicit opt-in. Requiring a literal server address avoids a bootstrap DNS leak
before the strict kill switch is installed.

## 15. Observability

`GET /metrics` exposes Prometheus text format. Labels are bounded to configured
peer names and path indices; random session IDs, source addresses, and unbounded
attacker-controlled values are not labels.

Required metrics:

- Per path: send/receive/aggregate state, srtt, rttvar, min RTT, queue delay,
  inbound/remote loss, delivery rate, winner/rescue rate, local/peer confirmed
  PMTU, and last-report age.
- Per path and class: packets/bytes enqueued, sent, received, expired, queue-full
  drops, budget drops, circuit-breaker drops, send errors, queue depth/bytes.
- Crypto: handshake attempts/success/failure by bounded reason, rekeys,
  malformed packets, authentication failures, replay drops.
- Dedup: accepted, duplicates, too-old, window utilization, reorder depth.
- Scheduler: primary path, selected replica set, classifications, no-path drops,
  MTU-ineligible copies.
- Session/server: active sessions, active paths, pending handshakes, idle closes,
  address validations.
- TUN/routing: packets/bytes, read/write errors, route reconciliation failures,
  kill-switch state.
- Runtime: goroutines, heap, allocations, GC pause distribution, process CPU.

`GET /healthz` returns success only when configuration is valid and required
listeners/TUN resources are active. Client readiness additionally requires one
healthy authenticated path and completed routing policy.

Structured logs include state transitions and bounded reason codes. They never
include keys, PSKs, join tokens, plaintext inner packets, or full unauthenticated
packet dumps.

## 16. Repository layout

```text
cmd/red-mpudp/              CLI and subcommands
internal/config/            YAML schema, defaults, validation
internal/identity/          key files, peer IDs, HKDF helpers
internal/noisehandshake/    Noise IKpsk2 OPEN/JOIN state machine
internal/wire/              fixed codecs and parser fuzz targets
internal/replay/            outer replay bitmap
internal/dedup/             global DATA window and race metadata
internal/path/              path actor, crypto, queues, health, PMTU
internal/session/           logical session and server session shard
internal/scheduler/         classification, scores, replication budgets
internal/tun/               Linux TUN allocation and packet validation
internal/routing/           netlink policy routes and resolver integration
internal/firewall/          nftables transaction/reconciliation
internal/transport/udp/     DatagramIO client/server implementations
internal/metrics/           Prometheus metrics and health endpoint
internal/testclock/         deterministic clock for state-machine tests
test/integration/           netns, veth, nftables, netem harness
```

Linux-specific packages use build tags. Core wire, replay, dedup, scheduler, and
state-machine logic remain testable without root.

## 17. Testing plan

### 17.1 Unit and property tests

- Every codec round-trips minimum, maximum, and invalid values.
- Encoded struct sizes equal their protocol constants.
- Checked-in golden vectors cover HKDF outputs, handshake envelopes, transport
  headers/AAD, each fixed control payload, and representative ciphertexts.
- Unknown types/flags, bad lengths, and trailing fixed-control bytes fail.
- Oversized/truncated UDP datagrams are detected from receive metadata and are
  never authenticated as a shorter packet.
- Noise handshake succeeds for matching peers and fails for every wrong key,
  PSK, prologue, or tampered transcript case.
- Each path direction uses an independent key and nonce space.
- AEAD tampering of every authenticated header field fails.
- Replay state is unchanged after authentication failure.
- Replay/dedup boundaries, large jumps, reordering, duplicates, and exhaustion.
- A forged high `logical_seq` cannot poison dedup state.
- Loss accounting recovers reordered `path_seq` values before finalization.
- A PATH_REPORT carried on path B updates only its authenticated
  `reported_path_token` A and cannot reference another session.
- RTT samples are produced once per probe ID.
- Score/hysteresis and all scheduler eligibility conditions.
- Queue priorities, deadlines, byte/packet limits, and token buckets using a
  deterministic clock.
- Pacer arithmetic, additive increase, multiplicative decrease, feedback loss,
  min/max clamps, report coalescing, and controller reset on new incarnations.
- PMTU search, acknowledgment validation, black-hole fallback, and
  MTU-ineligible scheduling.
- Session OPEN/JOIN/rekey/rebind/timeout/restart state transitions.
- Inner IPv4 validation and per-peer source-address enforcement.
- Configuration validation before mutation.

### 17.2 Fuzz and race tests

- Fuzz handshake envelope and every transport packet type.
- Fuzz inner IPv4 validation independently.
- Fuzz replay and dedup operations against a simple reference model.
- Fuzz random session/path state-event sequences and assert invariants.
- Run the complete unit suite under `go test -race`.
- Assert bounded allocations for malformed datagrams and unknown sessions.

### 17.3 Integration topology

The basic test uses two client/server paths, not one shared veth:

```text
client-ns:c0 <---- veth path A ----> s0:server-ns
client-ns:c1 <---- veth path B ----> s1:server-ns
                                      |
                                  exit veth
                                      |
                                 internet-ns
```

Client and server each create one TUN. `internet-ns` supplies ping, UDP echo,
TCP, and DNS targets so NAT and return routing are tested without external
network access.

Required scenarios:

- Single-path end-to-end ping, UDP, TCP, and DNS.
- Two-path duplication and first-copy dedup in both directions.
- Different delay, jitter, loss, duplication, and reorder on each direction of
  each veth using `tc netem`.
- Kill path A and path B independently during latency traffic and controlled
  bulk traffic.
- Keep uplink working while breaking only downlink, and the reverse.
- Make the secondary path much slower than offered load; verify bounded queues,
  opened circuit breaker, and stable primary latency.
- Run greedy inner UDP beside a native TCP flow on the same constrained path;
  verify the outer pacer reacts to loss/queue feedback and does not starve TCP.
- Change client address and UDP source port; validate candidate challenge and
  continued session operation.
- Interface down/up and gateway change through netlink.
- Replay captured authenticated datagrams and inject forged high sequences.
- Handshake replay, retry-cookie validation, pending-state limits, and invalid
  pre-authentication floods.
- PMTU steps, forged PTB, filtered PTB, and black-hole sizes.
- Server restart, client restart, idle expiry, and rekey overlap.
- Multiple configured peers attempting tunnel-address spoofing.
- Crash/restart route and nftables reconciliation.
- SIGKILL with the kill switch enabled leaves traffic fail-closed; restart and
  the ownership-checked cleanup command recover it. Malformed journals and
  operator-modified sysctls/rules are preserved with an error.
- IPv6 leak, DNS leak, LAN bypass, and kill-switch assertions for every policy.

### 17.4 Performance and latency tests

Compare four modes using identical impairment seeds:

1. Direct path A.
2. Direct path B.
3. RED_MPUDP primary-only.
4. RED_MPUDP adaptive redundant.

Record p50/p95/p99/p99.9 RTT, accepted loss, reorder depth, path win/rescue
rates, queue sojourn, CPU, allocations, GC pauses, and packets/bytes per second.

Release gates in the controlled test environment:

- Primary-only tunnel adds no more than 2 ms to p99 ping versus the same direct
  namespace path at 10,000 packets/s.
- With both paths capable of the offered load, redundant mode follows the
  faster copy distribution and adds no global DATA gap when either path is
  deterministically cut.
- When the secondary is rate-limited below offered load, its queues remain
  within configured bounds and primary p99 RTT is no more than 5 ms worse than
  primary-only mode.
- In the single-bottleneck greedy-UDP-versus-native-TCP test, the native TCP
  flow retains at least 35% of bottleneck throughput and the two-flow Jain
  fairness index is at least 0.90 after warm-up.
- A 30-minute stress run shows no monotonic heap/queue growth and no nonce,
  race, or deadlock failure.

Numbers may be tightened after the first benchmark, but they may not be removed
or replaced by subjective observations.

### 17.5 Real test bed

Use the same A/B matrix on a laptop with two genuinely independent uplinks and
one exit server. Record the path from client to exit and exit to game server so
an exit-route detour is not misattributed to the multipath engine.

Test:

- Idle game-like UDP traffic.
- Simulated game traffic while a background download runs.
- Jitter/loss introduced on each uplink separately.
- Physical disconnect and reconnect.
- Suspend/resume and DHCP renewal.
- Direct game path versus single-tunnel paths versus redundant mode.

## 18. Implementation plan

Every phase has a deliverable and exit criterion. A later phase does not begin
while a foundational invariant from an earlier phase is failing.

The checklist-level execution sequence is maintained in
[2026-09-03-red-mpudp-implementation-plan.md](../plans/2026-09-03-red-mpudp-implementation-plan.md).

### Phase 0 — Feasibility and dependency spike

1. Initialize the Go module, lint/test commands, CI, and package skeleton.
2. Pin and review the selected Noise library; run official Noise vectors.
3. Prove IKpsk2 OPEN/JOIN handshakes over a lossy/reordered UDP harness.
4. Prove explicit `CipherState.SetNonce` correctly authenticates out-of-order
   transport packets without shared-state races.
5. Benchmark seal/open at 1, 2, and 4 copies across representative packet sizes.
6. Prototype the feedback bitmap, per-direction AIMD controller, and pacer in a
   netem harness; compare it against a native TCP flow under loss and queueing.
7. Build a small QUIC DATAGRAM comparison using an interface-bound UDP socket.
   Measure throughput, p99 latency, allocations, queue bounds, MTU visibility,
   and delivery feedback. It may replace the custom data plane only if it meets
   the same requirements without private library forks.

**Exit:** the Noise dependency, explicit-nonce use, packet-rate target, and
congestion/fairness gates are validated; otherwise stop and revise the transport
choice before building the VPN around it.

### Phase 1 — Deterministic Linux harness and OS primitives

1. Create the three-namespace/two-veth integration topology.
2. Implement TUN create/read/write with `IFF_TUN | IFF_NO_PI`, `CLOEXEC`, owned
   packet buffers, and configurable MTU.
3. Implement the ownership journal, reconciliation, and guarded cleanup command
   before adding host-network mutations.
4. Implement UDP sockets with `SO_BINDTODEVICE`, `SO_MARK`, DF behavior,
   buffer sizing, and source-address selection.
5. Implement netlink snapshot, per-mark policy tables, rule install/remove, and
   rollback tests.
6. Implement named nftables table installation/reconciliation in the isolated
   namespaces.

**Exit:** a plaintext single-path packet can traverse the namespaces without
route recursion, and every mutation is restored after normal exit and reconciled
after forced termination. Plaintext mode is test-only and cannot run outside
the integration build.

### Phase 2 — Wire, identity, and authenticated sessions

1. Implement fixed codecs with no per-packet heap allocation.
2. Implement key generation/loading, permission checks, peer IDs, and HKDF.
3. Implement handshake envelope, pre-authentication, OPEN/OPEN_ACK, retry,
   retransmission cache, and limits.
4. Implement JOIN/JOIN_ACK and logical session/path tables.
5. Implement explicit transport nonce, per-path directional cipher ownership,
   and outer replay.
6. Put all DATA behind a conservative fixed 1 Mbit/s safety pacer until the
   feedback-driven controller replaces it in Phase 6.
7. Generate checked-in protocol/HKDF/ciphertext golden vectors.
8. Add parser fuzzers and authentication-before-state invariant tests.

**Exit:** one authenticated path exchanges encrypted DATA in memory and over
the namespace UDP link; tamper/replay/sequence-poison tests pass.

### Phase 3 — Single-path packet forwarding

1. Connect TUN ingress to client session DATA sealing.
2. Authenticate/decrypt and write client uplink to server TUN.
3. Implement server `tunnel_ip -> session` downlink dispatch.
4. Enforce inner IPv4 source/destination policy.
5. Enable forwarding/NAT and validate TCP, UDP, ICMP, and DNS through the
   `internet-ns` target.
6. Add session timeout, close, reconnect, and server-restart behavior.

**Exit:** primary-only encrypted IPv4 forwarding passes the functional,
spoofing, and restart tests inside the deterministic namespace harness. This is
not a production full-tunnel release configuration.

### Phase 4 — Multipath replication and dedup

1. Join a second interface path through an independent Noise handshake.
2. Implement global DATA sequence assignment before replication.
3. Implement the 65,536-entry dedup/race ring in both directions.
4. Process all authenticated path copies for metrics before dedup.
5. Add downlink replication across the session's current validated endpoints.
6. Add interface down/up and fresh-JOIN replacement behavior.

**Exit:** both directions race two paths, first-copy delivery is correct, and
deterministic single-path cuts create no logical sequence gap under eligible
load.

### Phase 5 — Health, feedback, roaming, and PMTU

1. Implement unique PING/PONG probes and directional health state.
2. Implement reorder-tolerant `path_seq` accounting, the report bitmap, active
   report triggering, cross-path report delivery, and idle PATH_REPORT.
3. Implement win/delta/rescue metrics and score/hysteresis.
4. Implement candidate endpoint challenge/response for NAT rebinding.
5. Implement conservative base MTU, PMTU probes, ACK validation, error-queue
   handling, black-hole fallback, and lower-MTU session renegotiation.
6. Implement parallel path rekey and drain.

**Exit:** asymmetric failures, NAT rebinding, rekey, and all PMTU scenarios pass
without nonce reuse, endpoint redirection, or outer fragmentation.

### Phase 6 — Adaptive scheduler and queue safety

1. Implement safe IPv4 traffic classification.
2. Implement primary selection and path eligibility snapshots.
3. Implement isolated priority queues and precise buffer ownership.
4. Implement the per-direction controller and DATA/control pacers.
5. Implement class token buckets, replica deadlines, and circuit breakers.
6. Add deterministic clock tests for every rate/queue/drop/cooldown transition.
7. Run mismatched-capacity, competing-TCP, and sustained-bulk benchmarks.

**Exit:** low-rate traffic fully replicates, bulk cannot create unbounded
secondary queues, native TCP is not starved, and the stated latency/fairness
gates pass.

### Phase 7 — Full-tunnel hardening

1. Implement the strict kill switch and install it before the first handshake.
2. Install default routes only after a path validates.
3. Implement LAN policy, IPv6 block/passthrough policy, and DNS resolver
   integration/rollback.
4. Harden nftables forwarding/NAT and startup reconciliation.
5. Validate capability-only deployment and key/config permissions.
6. Add hardened systemd units, resolver authorization, signals, graceful drain,
   and crash recovery documentation.

**Exit:** the full route/DNS/IPv6/LAN policy matrix passes and no unauthorized
traffic escapes during startup, operation, path loss, or shutdown.

### Phase 8 — Observability, profiling, and release validation

1. Implement bounded-cardinality Prometheus metrics and health endpoint.
2. Add structured transition/error logs with secret redaction tests.
3. Profile allocations and reuse buffers; add batching only when measurements
   show it is needed and semantics remain unchanged.
4. Run unit, fuzz, race, namespace, stress, and performance suites in CI where
   privileges are available.
5. Execute the real two-ISP A/B test matrix and save machine-readable results.
6. Update defaults only from measured evidence and record the environment.

**Exit:** every v1 success criterion and release gate is evidenced by a test or
saved benchmark result.

## 19. Deferred work

- Multiple exit servers and topology-aware failure-domain selection.
- IPv6 inner tunnel support.
- CIDR/application split tunnelling.
- Throughput bonding and coupled multipath congestion control.
- Optional bounded flow-aware reorder buffer.
- Kernel fast path, TUN multiqueue, `recvmmsg`/`sendmmsg`, GSO/GRO optimization.
- Separate privileged networking helper.
- GUI/tray application.
- Real QUIC/DTLS/MASQUE transport based on benchmark and deployment needs.
- Independently specified traffic-shape obfuscation; no fake-TLS framing.

## 20. Alternatives considered

### WireGuard

WireGuard provides an excellent secure tunnel, but one peer tracks one current
endpoint rather than simultaneously racing identical packets over several
endpoints. Multiple WireGuard tunnels plus another dedup encapsulation layer
remain a valid prototype comparison, but add routing/configuration and extra
headers without removing RED_MPUDP's scheduler/session layer.

### QUIC DATAGRAM

RFC 9221 provides unreliable, encrypted, congestion-controlled datagrams with
no retransmission and explicitly supports VPN-style tunnelling. It is not
rejected because of stream head-of-line blocking. It is deferred because the
current Go implementation documents an unoptimized DATAGRAM path and lacks APIs
needed for precise path delivery and current datagram-size feedback. Phase 0
keeps this decision evidence-based.

### Multipath TCP

MPTCP is a reliable ordered byte stream and cannot directly carry arbitrary UDP
game traffic without another tunnel and head-of-line behavior.

### Multipath DCCP

RFC 9897 is highly relevant: it defines unreliable congestion-controlled
multipath datagrams, path management, and multipath sequence concepts. It does
not supply encryption, UDP middlebox traversal, the RED_MPUDP replication
scheduler, or general-Internet coupled scheduling rules. Current deployment and
Go support make it a design reference rather than the v1 implementation base.

## 21. Normative and design references

- Noise Protocol Framework: <https://noiseprotocol.org/noise.html>
- Go Noise library API: <https://pkg.go.dev/github.com/flynn/noise>
- ChaCha20-Poly1305 nonce requirements, RFC 8439:
  <https://www.rfc-editor.org/rfc/rfc8439.html>
- HKDF, RFC 5869: <https://www.rfc-editor.org/rfc/rfc5869.html>
- UDP usage and tunnel congestion guidance, RFC 8085:
  <https://www.rfc-editor.org/rfc/rfc8085.html>
- RTT estimator, RFC 6298: <https://www.rfc-editor.org/rfc/rfc6298.html>
- Datagram PMTU discovery, RFC 8899:
  <https://www.rfc-editor.org/rfc/rfc8899.html>
- QUIC DATAGRAM, RFC 9221:
  <https://www.rfc-editor.org/rfc/rfc9221.html>
- quic-go DATAGRAM implementation notes:
  <https://quic-go.net/docs/quic/datagrams/>
- QUIC address/path validation model, RFC 9000:
  <https://www.rfc-editor.org/rfc/rfc9000.html>
- Multipath DCCP, RFC 9897:
  <https://www.rfc-editor.org/rfc/rfc9897.html>
- Linux TUN/TAP documentation:
  <https://docs.kernel.org/networking/tuntap.html>
