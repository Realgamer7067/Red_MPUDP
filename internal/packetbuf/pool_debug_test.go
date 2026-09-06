//go:build debug

package packetbuf_test

import (
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/packetbuf"
)

// BASE-20: with -tags debug, a double Release panics.
func TestDoubleReleasePanicsInDebugBuild(t *testing.T) {
	p := packetbuf.NewPool()
	b := p.Get(64)
	b.Release()
	defer func() {
		if recover() == nil {
			t.Fatal("double Release did not panic under -tags debug")
		}
	}()
	b.Release()
}

// BASE-20: with -tags debug, using a released buffer panics.
func TestUseAfterReleasePanicsInDebugBuild(t *testing.T) {
	p := packetbuf.NewPool()
	b := p.Get(64)
	b.Release()
	defer func() {
		if recover() == nil {
			t.Fatal("use after Release did not panic under -tags debug")
		}
	}()
	_ = b.Bytes()
}
