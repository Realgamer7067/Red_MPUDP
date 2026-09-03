# Security policy

RED_MPUDP is pre-release software. It has not had an independent security
review (that is milestone M22). Do not deploy it to protect real traffic yet.

## Reporting a vulnerability

Report suspected vulnerabilities **privately**. Do not open a public issue or
pull request for a security problem.

- Use GitHub's **"Report a vulnerability"** (Security → Advisories → Report a
  vulnerability) on this repository, or
- email the maintainer listed as **Author** in
  `docs/superpowers/specs/2026-09-02-red-mpudp-design.md`.

Please include: affected commit, a description of the problem and its impact,
and the minimal steps or shape that reproduce it.

### Do not attach secrets or plaintext

When reporting, **never attach**:

- static private keys or public/private key pairs,
- pre-shared keys (PSKs) or any HKDF-derived key,
- join tokens or session tokens,
- plaintext inner-packet captures, or full captures of unauthenticated
  datagrams.

Describe the class of problem and the packet/state shape instead. If a proof
of concept needs key material, generate throwaway keys and say so.

## Scope

In scope: the RED_MPUDP protocol and daemon (handshake, data plane, replay/
dedup, scheduler, routing/firewall/DNS integration, mutation journal and
recovery, CLI).

Out of scope (per design §4.2): a compromised client or server host, traffic
analysis / timing / size / server-address concealment, availability against an
attacker who can drop all traffic, and privacy from the exit server.

## Disclosure

We aim to acknowledge a report within a few days and to agree a coordinated
disclosure timeline with the reporter.
