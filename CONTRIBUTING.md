# Contributing to RED_MPUDP

## Design first

`docs/superpowers/specs/2026-09-02-red-mpudp-design.md` is the normative v1
protocol document. `docs/superpowers/plans/2026-09-03-red-mpudp-implementation-plan.md`
is the ordered checklist. Work follows the plan's milestone order; each
checkbox is one focused change with its own test.

Any change to the **wire format, security model, MTU arithmetic, or Linux
routing behaviour** updates the design document and bumps its revision in the
same pull request (see `.github/pull_request_template.md` and
`docs/superpowers/plans/v1-traceability.md`).

## Per-change workflow

1. Add or adjust the smallest focused test; confirm it fails for the expected
   reason.
2. Make the smallest production change that passes it.
3. `make fmt vet test-unit` (and `make test-race` for anything concurrent).
4. Commit at the milestone checkpoint shown in the plan.

Keep the tree buildable at every checkpoint. Never expose a plaintext
production mode, send unpaced DATA, or mutate host networking before config
validation and journal creation succeed.

## Test commands and privilege boundaries

| Command | Privilege | Notes |
|---------|-----------|-------|
| `make fmt` | none | rewrites Go files in place |
| `make vet` | none | `go vet ./...` |
| `make test-unit` | none | `go test ./...` |
| `make test-race` | none | `go test -race ./...` |
| `make test-fuzz-smoke` | none | short run of every `Fuzz*` target |
| `make bench` | none | one-shot benchmarks |
| `make build` | none | reproducible binary into `bin/` |
| `make test-integration` | **root / CAP_NET_ADMIN, Linux** | creates network namespaces; needs `ip`, `nft`, `tc` |

Unit, race, fuzz-smoke, bench, and build must always pass **without root** and
**without** `ip`/`nft`/`tc`. Privileged tests live behind the `integration`
build tag under `test/` and run in isolated network namespaces only.

Toolchain: Go 1.27 (`docs/development/toolchain.md`). Minimum target kernel
5.15 (`docs/development/kernel-support.md`).

## Security-sensitive material

Never attach private keys, PSKs, join tokens, or plaintext packet captures to
an issue, PR, or test artifact. See `SECURITY.md`.
