# RED_MCUDP — Multipath UDP VPN — Design

**Status:** Draft for implementation
**Date:** 2026-09-02
**Author:** malharrajpara28@gmail.com

## 1. Purpose

RED_MCUDP is a latency-first VPN for Linux. It tunnels all device traffic
through a self-hosted exit server, but instead of a single link it sends
every packet over **multiple network paths at once** and uses whichever copy
arrives first. The goal is the lowest achievable ping and zero-gap failover
under bad network conditions (jitter spikes, packet loss, brief outages on
one uplink).

This is the Speedify-style "packet-level bonding" model, tuned for redundancy
rather than throughput. It is comparable in spirit to ExitLag (multipath
routing for game traffic) and Speedify (link bonding), but self-hosted and
open.

### Non-goals (v1)

- Throughput aggregation / bandwidth bonding (a later mode).
- Multiple exit servers (v1 is one server; the design leaves room for a
  server mesh with downlink fan-out later).
- Windows / macOS / Android clients (Linux only).
- Split tunnelling (full tunnel in v1; split-tunnel CIDR/app rules later).
- Forward secrecy / handshake crypto (v1 uses a pre-shared key; Noise IK
  is v2).
- Traffic obfuscation on the wire (interface defined in v1, implementations
  are v2).

## 2. Key decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Custom UDP overlay, built from scratch | The scheduler, dedup, and path-health logic *are* the product. Building on WireGuard or QUIC buries them under machinery designed for a different model (single endpoint; reliable ordered streams with their own congestion control and head-of-line blocking). |
| D2 | Not Multipath TCP (RFC 8684) | TCP-only; carrying UDP game traffic inside a reliable ordered stream adds head-of-line blocking and *worse* jitter than a single path. |
| D3 | UDP overlay carries both TCP and UDP inner traffic | Full IP tunnel via a TUN device. Transport is our own UDP datagram protocol. |
| D4 | Default scheduler mode: **Redundant** (send every packet on all healthy paths, receiver keeps first arrival) | Gives zero-gap failover — there is no "detect spike, then switch" latency because we are always on every path. This is the mode that actually fixes ping under bad conditions. |
| D5 | Cap of **4 active duplicated paths** | N copies of every packet costs N× upload bandwidth. 4 is the practical ceiling and matches the eventual 2-interface × 2-server target. |
| D6 | One exit server in v1 | Uplink and downlink are both still duplicated across all local interfaces. Multi-server (and the downlink fan-out it requires) is deferred. |
| D7 | Language: **Go** | Fast to build, strong net/tun ecosystem, GC pauses are sub-millisecond and tolerable for this workload. Single static binary for client and server. |
| D8 | Crypto v1: pre-shared key + ChaCha20-Poly1305 AEAD on every datagram | Simple, correct, enough for a trusted self-hosted test bed. Noise IK handshake is v2. |
| D9 | No reordering / playout buffer in v1 | A reorder buffer trades latency for smoothness. Games tolerate mild reorder; inner TCP tolerates reorder. Add only if measurement shows it is needed. |
| D10 | Obfuscation is a pluggable `Transport`, off by default | Keeps the multipath engine wire-agnostic. `ScrambleUDP` (AmneziaWG-style junk/padding/header randomization) and `FakeTLS` are v2 and drop in with no engine change. |

## 3. Architecture

```
   +------------------- CLIENT (Linux) --------------------+
   |  apps -> kernel routing -> red0 (10.9.0.2/24)         |
   |                              |                        |
   |                       +------+-------+                |
   |                       |  core engine |                |
   |                       | dedup, seq,  |                |
   |                       | scheduler    |                |
   |                       +--+--------+--+                |
   |            sock bound    |        |  sock bound       |
   |            to wlan0      |        |  to wlan1         |
   +------------------------- | ------ | ------------------+
                              |        |
                     path 0   |        |  path 1
                     (wlan0->S)|        | (wlan1->S)
                              v        v
   +------------------- SERVER (Linux, 1 for now) ---------+
   |   udp :51820  -->  core engine (dedup, seq)          |
   |                         |                            |
   |                       red0 (10.9.0.1/24)             |
   |                         |                            |
   |          iptables MASQUERADE -> eth0 -> internet     |
   +-----------------------------------------------------+
```

### 3.1 Paths

A **path** is one `(local interface, server)` pair. In v1 there is one server,
so the number of paths equals the number of usable local interfaces.

- Client opens **one UDP socket per local interface**, bound with
  `SO_BINDTODEVICE` so its packets egress that specific NIC regardless of the
  kernel routing table.
- All sockets send to the same server `ip:port`.
- The server has **one** listen socket. The client's paths arrive as
  distinct source addresses; the server maps each to `(session_id, path_id)`
  carried in the header, not to the UDP 4-tuple, so a source-IP change
  (DHCP renew, reconnect) does not break the path.

### 3.2 Overlay network

- Subnet `10.9.0.0/24`. Server TUN `10.9.0.1`, client TUN `10.9.0.2`.
- Client: default route (v1) via `red0`. A pre-existing route to the server's
  public IP over the physical links is added so the tunnel's own packets do
  not recurse.
- Server: route for `10.9.0.0/24` via `red0`; `net.ipv4.ip_forward=1`;
  `iptables -t nat -A POSTROUTING -s 10.9.0.0/24 -o <wan_iface> -j MASQUERADE`.
  Applied on startup when `manage_nat: true`.

### 3.3 Process model

Single Go binary `red-mcudp` with subcommands:

```
red-mcudp client -c client.yaml
red-mcudp server -c server.yaml
```

Requires `CAP_NET_ADMIN` for TUN creation and route/iptables management (run
as root or `setcap`).

## 4. Wire protocol

Every datagram: fixed plaintext header (authenticated as AEAD associated
data) + 12-byte nonce + AEAD-sealed payload + 16-byte tag. Multi-byte fields
big-endian.

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-------+-------+---------------+-------------------------------+
| ver=1 | type  |    path_id    |          flags (16)           |
+-------+-------+---------------+-------------------------------+
|                        session_id (64)                        |
+                                                               +
|                                                               |
+---------------------------------------------------------------+
|                            seq (64)                           |
+                                                               +
|                                                               |
+---------------------------------------------------------------+
|                         path_seq (32)                         |
+---------------------------------------------------------------+
|                      send_ts_micros (64)                      |
+                                                               +
|                                                               |
+---------------------------------------------------------------+
|                      echo_ts_micros (64)                      |
+                                                               +
|                                                               |
+---------------------------------------------------------------+
|                     12-byte AEAD nonce ...                    |
+---------------------------------------------------------------+
|          ciphertext( inner IP packet ) + 16-byte tag          |
+---------------------------------------------------------------+
```

Plaintext header = 40 bytes (`ver`+`type` 1, `path_id` 1, `flags` 2,
`session_id` 8, `seq` 8, `path_seq` 4, `send_ts` 8, `echo_ts` 8). Total
per-packet overhead over the inner IP packet: 40 (header) + 12 (nonce) + 16
(tag) = **68 bytes**, plus the outer IPv4+UDP (28 bytes) on the wire.

**TUN MTU = 1372** (1500 − 28 outer − 68 overhead − 32 slack) so overlay
datagrams never fragment on a standard 1500-byte path. Fixed in v1; path-MTU
discovery is a later refinement.

### 4.1 Fields

| Field | Meaning |
|-------|---------|
| `ver` | protocol version, 1 |
| `type` | see table below |
| `path_id` | which path this datagram belongs to (0-based, assigned by client) |
| `flags` | bit 0: control-plane seq space (PING/PONG) vs data seq space; rest reserved |
| `session_id` | random 64-bit, assigned by server in `HELLO_ACK`; ties all paths together and survives roaming |
| `seq` | **global per-session** counter, incremented once per inner packet *before* duplication. Every copy of one inner packet on every path carries the same `seq`. This is the dedup key. |
| `path_seq` | **per-path** monotonic counter. Used only to measure per-path loss from gaps. |
| `send_ts_micros` | sender's monotonic clock at send |
| `echo_ts_micros` | the most recent `send_ts` the sender has seen *from the peer on this path*. Lets the peer compute path RTT from any DATA or PONG. |

### 4.2 Packet types

| val | name | payload |
|-----|------|---------|
| 0 | DATA | AEAD-sealed inner IP packet |
| 1 | PING | AEAD-sealed empty; idle-path health probe |
| 2 | PONG | AEAD-sealed empty; immediate reply to PING |
| 3 | HELLO | client -> server: PSK proof + requested TUN addr |
| 4 | HELLO_ACK | server -> client: assigned `session_id`, confirmed TUN addr, server params |
| 5 | BYE | graceful teardown |

### 4.3 Crypto (v1)

- The full 40-byte plaintext header is passed as AAD.
- Config carries a 32-byte pre-shared key (`psk`, base64).
- Per-session key = `BLAKE2s-256(psk || session_id)`.
- AEAD: ChaCha20-Poly1305.
- Nonce: 4-byte path-scoped prefix + 8-byte per-path monotonic counter.
  Counters never reused within a session; on counter exhaustion the session
  is torn down (not reachable in practice).
- Because the header is AAD, `path_id`, `seq`, `flags`, and timestamps cannot
  be altered without failing the tag.
- Replay: handled by the dedup window (Section 5.4); a replayed datagram has
  a `seq` already seen or too old and is dropped.

**v2:** Noise IK handshake for forward secrecy and identity hiding, replacing
the static per-session key derivation. `HELLO`/`HELLO_ACK` become the
handshake carrier.

## 5. Core engine

### 5.1 Path state

Per path, maintained as exponentially weighted moving averages:

| Metric | Source | Use |
|--------|--------|-----|
| `srtt` | `send_ts`/`echo_ts` pair on every received DATA and PONG; PING/PONG when the path is otherwise idle | ranking |
| `rttvar` | mean deviation of RTT samples | jitter penalty in score |
| `loss` | fraction of missing `path_seq` values over a trailing window | health gate + score penalty |
| `last_recv` | wall-clock time of last valid datagram on the path | dead detection |
| `alive` | `last_recv` within 3× keepalive interval **and** `loss` < 0.40 | membership in the healthy set |

### 5.2 Keepalive / probing

- If nothing has been sent on a path for `keepalive_ms` (default 200 ms),
  send a PING on it.
- PONG is sent immediately on receipt of PING.
- Active paths are measured for free from data traffic; only idle paths cost
  probe bandwidth (~76 B × 5/s per idle path ≈ negligible).

### 5.3 Scheduler — Redundant mode (v1), cap 4

On each inner packet read from the TUN:

```
seq = next_global_seq()
healthy = [p for p in paths if p.alive]
if healthy is empty:
    selected = all known paths            # desperation fallback
else:
    rank healthy by score ascending
    selected = healthy[:min(max_paths, len(healthy))]
sealed = seal(inner_packet, seq)          # sealed once
for p in selected:
    p.send(sealed with p.path_id, p.path_seq++)
```

**Score** (lower is better):

```
score = srtt + 2*rttvar + loss * 200ms
```

- `loss * 200ms`: 10% loss is treated as roughly +20 ms of latency-equivalent
  badness.
- **Hysteresis:** a path already in the active set gets a −5 ms sticky bonus
  to its score, so it does not flap in and out on small deltas.

With the 2-path test bed, both paths are healthy and below the cap, so both
are always selected — uplink and downlink are permanently duplicated and
failover has no gap. The ranking/cap logic becomes load-bearing once a third
interface or the second server is added.

### 5.4 Dedup (both ends)

- State: `highest_seq_seen` + a bitmap covering the last **4096** sequence
  numbers.
- `seq` already marked -> drop silently (expected duplicate copy).
- `seq` older than `highest_seq_seen − 4096` -> drop (late straggler / replay).
- New `seq` -> mark, decrypt, write the inner packet to the TUN.
- No reorder buffer: packets are written to the TUN in arrival order.

### 5.5 Downlink

Server-side mirror of the same logic. The server reads an inner packet from
its TUN, assigns a global `seq` from that session's downlink space, and
sprays it on all healthy paths for that session (each path's current
client-side source address is the destination). The client dedups and writes
to its TUN.

## 6. Client internals

Goroutines, communicating over buffered channels (drop-oldest on overflow):

- **TUN reader** — reads inner packets, forwards to the scheduler.
- **TUN writer** — receives deduped inner packets, writes to `red0`.
- **Scheduler (single goroutine)** — owns all mutable path state, the global
  seq counter, and the dedup window. No hot-path mutexes.
- **Per path: tx goroutine + rx goroutine** — tx socket bound via
  `SO_BINDTODEVICE`; rx blocks on `ReadFromUDP` and forwards to the
  scheduler (DATA -> dedup, PONG -> metrics).
- **Timer/metrics goroutine** — fires keepalive ticks, recomputes EWMA
  scores every 100 ms, flips `alive`.
- **Interface watcher** — netlink subscription; on interface up/down or
  IP change, tells the scheduler to add, drop, or rebind a path.

## 7. Server internals

Mirror of the client with these differences:

- **One UDP listen socket.** Inbound datagrams are demultiplexed by
  `(session_id, path_id)` from the header.
- **Session table:** `session_id -> { tun addr, per-path last source addr,
  per-path metrics, dedup windows (up + down), crypto counters, last activity }`.
  Idle sessions time out.
- **TUN + NAT setup** on startup when `manage_nat: true`: create `red0`,
  add the subnet route, set `ip_forward`, install the MASQUERADE rule; remove
  what it added on shutdown. `--no-nat` skips this for operators who
  pre-configure.
- Session table is keyed for **multiple clients**; v1 is tested with one.
- Downlink scheduler runs per session with the same scoring.

## 8. Configuration

`client.yaml`:

```yaml
server:       "203.0.113.10:51820"
psk:          "base64-encoded-32-bytes"
interfaces:   ["wlan0", "wlan1"]   # empty => auto-detect non-loopback
tun_name:     "red0"
tun_addr:     "10.9.0.2/24"
mtu:          1372
mode:         "redundant"          # only mode in v1
max_paths:    4
keepalive_ms: 200
transport:    "plain"             # plain | scramble | faketls (only plain in v1)
metrics_addr: "127.0.0.1:9090"
log_level:    "info"
```

`server.yaml`:

```yaml
listen:       "0.0.0.0:51820"
psk:          "base64-encoded-32-bytes"
tun_name:     "red0"
tun_addr:     "10.9.0.1/24"
subnet:       "10.9.0.0/24"
wan_iface:    "eth0"
manage_nat:   true
mtu:          1372
session_idle_timeout_s: 120
metrics_addr: "127.0.0.1:9090"
log_level:    "info"
```

## 9. Transport abstraction

```go
type Transport interface {
    Send(pkt []byte) error       // sends one opaque overlay datagram
    Recv() ([]byte, error)
    Rebind(iface string) error   // roaming: move the socket to a new NIC
}
```

The engine hands `Transport` sealed bytes and never inspects the wire form.

| Transport | Description | Version |
|-----------|-------------|---------|
| `PlainUDP` | raw datagram; our header visible, payload AEAD-encrypted | v1 |
| `ScrambleUDP` | AmneziaWG-style: keystream XOR mask over the whole datagram, random length padding, random junk packets at startup, randomized magic-header prefix. ~0 latency cost. Defeats naive DPI signature matching. | v2 |
| `FakeTLS` | wrap datagrams in real-looking TLS 1.3 record framing (ClientHello/ServerHello then "application data" records); self-signed, SNI spoofable | v2 |
| `QUIC-masq` | HTTP/3 / Connect-UDP packet-shape masquerade (borrow the shape, not QUIC's reliability) | later, possibly never |

QUIC is explicitly **not** used as the transport: its streams carry their own
loss recovery, congestion control, and head-of-line blocking, which fight the
per-packet duplication model.

## 10. Observability

`red-mcudp` serves `GET /metrics` (plain text) on `metrics_addr`:

- per-path: `srtt`, `rttvar`, `loss`, `alive`, `selected`, packets tx/rx,
  bytes tx/rx
- dedup: total accepted, duplicates dropped, stragglers dropped, dedup hit
  rate
- scheduler: current selected set, seq rate
- sessions (server): count, per-session age

This is enough to debug the engine and later to back a GUI.

## 11. Testing strategy

### Unit

- Header encode/decode round-trip (all types, all field extremes).
- AEAD seal/open; tamper any AAD byte -> open fails.
- Dedup window: fresh seq, duplicate seq, old seq, seq at the 4096 boundary,
  wraparound.
- Score ranking given synthetic path states; hysteresis behaviour.
- EWMA math for srtt/rttvar/loss.
- Scheduler selection: given fake path states, assert the selected set.

### Integration (network namespaces)

- Two TUNs in two netns joined by a veth pair; run client + server; ping and
  iperf across; assert zero loss.
- `tc qdisc netem` adds 100 ms delay / 10% loss / jitter on one veth ->
  assert end-to-end RTT tracks the *good* path, not the degraded one.
- Kill one path mid-iperf -> assert no packet loss and the session survives.
- Change an interface's IP mid-session -> assert the path rebinds and the
  session survives.

### Manual (real test bed)

- Laptop with two ISPs, one server in Mumbai. `mtr` / a real game to a real
  server. Pull one uplink -> watch ping stay flat.

## 12. Build order

1. TUN open/read/write wrapper; config loader; PSK loader.
2. Wire protocol codec + ChaCha20-Poly1305 seal/open (unit-tested).
3. `PlainUDP` transport with `SO_BINDTODEVICE`.
4. `HELLO`/`HELLO_ACK` session setup; server session table.
5. Single-path data flow end to end (client TUN -> server TUN -> NAT ->
   internet and back). Validate with one interface.
6. Dedup window.
7. Path metrics (srtt/rttvar/loss) from live timestamps + PING/PONG.
8. Scheduler with redundant mode + scoring + hysteresis.
9. Interface watcher (netlink) for add/drop/rebind.
10. Metrics endpoint.
11. Integration test harness (netns + netem).
12. Real test-bed validation.

## 13. Deferred (v2+)

- Second exit server + downlink fan-out over a server-to-server link
  (co-located servers, different transit providers).
- `ScrambleUDP` and `FakeTLS` transports.
- Noise IK handshake (forward secrecy).
- Split tunnelling (CIDR / app / port rules).
- Bonding / aggregation scheduler mode for bulk throughput.
- Hybrid mode: duplicate small latency-sensitive packets, bond bulk.
- Path-MTU discovery.
- Reorder / playout buffer, if measurement justifies it.
- GUI / tray app fed by the metrics endpoint.
