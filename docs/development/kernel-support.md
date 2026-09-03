# Linux kernel and distribution support (LOCK-05, LOCK-13)

## Minimum supported kernel: 5.15

Chosen as the oldest kernel on which every RED_MPUDP OS primitive is available
and stable. 5.15 is an LTS series (maintained upstream into 2026) and is the
baseline kernel of the release distribution matrix below.

### Required kernel features and where RED_MPUDP uses them

| Capability | Kernel interface | Available since | Milestone |
|------------|------------------|-----------------|-----------|
| TUN device, `IFF_TUN \| IFF_NO_PI` | `/dev/net/tun`, `TUNSETIFF` | long predates 5.15 | M06 |
| nftables tables/chains/sets, atomic transactions | `nf_tables` netlink | 4.x; stable well before 5.15 | M08, M20 |
| Policy routing: fwmark rules, multiple tables | `FRA_*` / `RTM_NEWRULE` | long predates 5.15 | M08 |
| Socket mark | `SO_MARK` | long predates 5.15 | M07 |
| Bind socket to device | `SO_BINDTODEVICE` | long predates 5.15 | M07 |
| Do-not-fragment + path-MTU on UDP | `IP_MTU_DISCOVER` = `IP_PMTUDISC_DO` | long predates 5.15 | M07, M16 |
| Extended socket error queue (ICMP PTB / local `EMSGSIZE`) | `IP_RECVERR`, `MSG_ERRQUEUE`, `SO_EE_ORIGIN_*` | long predates 5.15 | M07, M16 |
| Per-packet receive metadata (dest addr, ifindex) | `IP_PKTINFO` via `recvmsg` | long predates 5.15 | M07 |
| `MSG_TRUNC` on datagram receive | `recvmsg` flag | long predates 5.15 | M07 |
| Reverse-path filter mode + marked RP lookups | `net.ipv4.conf.*.rp_filter`, `net.ipv4.conf.all.src_valid_mark` | long predates 5.15 | M08 |
| Netlink link/addr/route monitor | `RTNLGRP_*` multicast groups | long predates 5.15 | M08 |
| Network namespaces + veth (test harness) | `CLONE_NEWNET`, `veth` | long predates 5.15 | M05 |
| `tc netem` delay/loss/dup/reorder/rate (test harness) | `sch_netem`, `sch_tbf` | long predates 5.15 | M05 |
| systemd-resolved D-Bus DNS control | `org.freedesktop.resolve1` | systemd ≥ 229 | M20 |

None of the interfaces above requires a kernel newer than 5.15; 5.15 is chosen
for LTS longevity and distribution coverage, not for a single feature gate.

## Release test matrix (LOCK-13)

| Distribution | Kernel series | Role in the matrix |
|--------------|---------------|--------------------|
| Debian 12 (bookworm) | 6.1 LTS | primary release target |
| Ubuntu 22.04 LTS | 5.15 | **minimum-kernel** gate |
| Ubuntu 24.04 LTS | 6.8 | current mainstream |
| Fedora (current stable) | rolling recent | newest nftables / systemd |
| Arch / CachyOS | rolling | developer host; not a release gate |

`make test-integration` must pass on the Ubuntu 22.04 (5.15) image before any
release. Newer kernels are expected to pass the same suite unchanged.

## CAP_NET_ADMIN / CAP_NET_RAW

The daemon needs `CAP_NET_ADMIN` (TUN, netlink, nftables, sysctl). Socket
rebind and error-queue behaviour are additionally exercised under `CAP_NET_RAW`
on the matrix images (see the design §11.4). Running as root is supported for
development only.
