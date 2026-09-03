<!-- LOCK-17: protocol changes require a traceability update. -->

## What

<!-- One or two sentences. Link the milestone / checkbox IDs from
     docs/superpowers/plans/2026-09-03-red-mpudp-implementation-plan.md -->

## Testing

<!-- Commands run and their result. New/changed tests. -->

## Protocol-impact checklist

- [ ] This PR does **not** change the wire format, security model, MTU
      arithmetic, or Linux routing behaviour.

If any box below is checked, the design document
(`docs/superpowers/specs/2026-09-02-red-mpudp-design.md`) is updated and its
revision bumped **in this PR**, and
`docs/superpowers/plans/v1-traceability.md` is updated:

- [ ] Wire format (packet layout, constants, magic, codecs, golden vectors)
- [ ] Security model (handshake, keys, nonces, replay, auth-before-state)
- [ ] MTU arithmetic (overhead, base/min/max sizes, PMTU search)
- [ ] Linux routing / firewall / sysctl / DNS behaviour
- [ ] A v1 success criterion's implementing milestone or proving test changed

## Design / decision references

<!-- e.g. design §6.2, docs/superpowers/plans/open-decisions.md D-P0-1,
     docs/decisions/0001-v1-transport.md -->
