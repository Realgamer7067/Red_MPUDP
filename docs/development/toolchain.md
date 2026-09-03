# Toolchain

## Go (LOCK-04)

- **Selected version: Go 1.27.**
- `go.mod` pins the Go version; CI resolves it with
  `go-version-file: go.mod` on every job, so the workflow and `go.mod` never
  drift.
- Rationale: 1.27 is the version installed on the development host
  (`go1.27.0 linux/amd64`) and the current stable release as of 2026-09.
  No language or standard-library feature below 1.27 is required; the pin is
  a floor, not a ceiling.
- Bumping the pin is a deliberate change: update the `go` directive in
  `go.mod` and this file in the same commit (CI follows `go.mod`
  automatically), and re-run the full `make` target set.

## Reproducible builds (BOOT-16)

`make build` uses:

- `-trimpath` — no absolute paths in the binary.
- `-ldflags` injecting `internal/buildinfo.{Version,Commit,Date}`.
- `Date` comes from the **commit** timestamp
  (`git log -1 --format=%cI`), never wall-clock `date`, so a rebuild from the
  same commit produces the same bytes (RELEASE-34/35).
- `Version` comes from `git describe --tags --always --dirty`.
- `Commit` comes from `git rev-parse --short=12 HEAD`.

Override any of `VERSION` / `COMMIT` / `DATE` on the `make` command line for
release builds driven by an external build system.

## Required host tools

| Tool  | Used by                                  | Needed for |
|-------|------------------------------------------|------------|
| `go`  | everything                               | build, unit, race, fuzz, bench |
| `git` | `make build` version stamping            | build |
| `ip`  | `internal/routing`, integration harness  | integration tests only |
| `nft` | `internal/firewall`, integration harness | integration tests only |
| `tc`  | integration harness (`netem`)            | integration tests only |
| `sudo`/root | privileged integration tests       | integration tests only |

Unit, race, fuzz-smoke, and bench targets never require root or `ip`/`nft`/`tc`.
