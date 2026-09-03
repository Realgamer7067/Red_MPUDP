# experiments/

Throwaway spikes kept as **decision evidence**, not part of the product.

Each subdirectory has its **own `go.mod`**, so `go build ./...`, `go test ./...`,
`make`, and CI in the main module never touch this tree. Run a spike explicitly
from its own directory.

| Spike | Purpose | Outcome |
|---|---|---|
| `quicdatagram/` | M02 SPIKE-49..59: evaluate QUIC DATAGRAM (quic-go) as the v1 transport | Rejected — see `quicdatagram/README.md` and `docs/decisions/0001-v1-transport.md` |

Retained per `plan:SPIKE-66` ("remove the unselected experiment from normal
build and test paths while retaining its decision evidence").
