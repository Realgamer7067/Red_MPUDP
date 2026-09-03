//go:build !debug

package packetbuf_test

import (
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/packetbuf"
)

// BASE-20: in the default (production) build a double Release is swallowed, not
// fatal. The panic behaviour is covered by pool_debug_test.go under -tags debug.
func TestDoubleReleaseIsSafeInProductionBuild(t *testing.T) {
	p := packetbuf.NewPool()
	b := p.Get(64)
	b.Release()
	b.Release() // must not panic in the default build
}
