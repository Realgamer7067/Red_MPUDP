package noisehandshake

import (
	"crypto/rand"
	"encoding/binary"
	"testing"

	"github.com/flynn/noise"
)

// SPIKE-29..37: measure the per-packet cost of the RED_MPUDP transport AEAD.
//
// Model (design §6.2): each datagram sets the cipher-state nonce to the outer
// packet number, then seals/opens the payload with the 44-byte transport header
// as AAD. Each replicated path copy is sealed independently under that path's
// own key and nonce ("seal once and edit the header" is forbidden).
//
// Run:  go test ./internal/noisehandshake/ -run '^$' -bench . -benchmem
// Results for the transport decision are saved under test/results/phase0/.

const aadLen = 44 // design §6.2 fixed authenticated header

func newHeader(n uint64) []byte {
	h := make([]byte, aadLen)
	copy(h[0:4], "RMPU")
	h[4] = 0x10
	binary.BigEndian.PutUint16(h[6:8], aadLen)
	binary.BigEndian.PutUint64(h[20:28], n)
	return h
}

func randKey(tb testing.TB) [32]byte {
	tb.Helper()
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		tb.Fatal(err)
	}
	return k
}

// oneWayPair returns a matched send/receive CipherState for one path direction.
func oneWayPair(tb testing.TB) (send, recv *noise.CipherState) {
	k := randKey(tb)
	return noise.UnsafeNewCipherState(suite, k, 0), noise.UnsafeNewCipherState(suite, k, 0)
}

func benchSealOpen(b *testing.B, ptLen int) {
	send, recv := oneWayPair(b)
	pt := make([]byte, ptLen)
	_, _ = rand.Read(pt)
	hdr := newHeader(0)
	ctBuf := make([]byte, 0, ptLen+16)
	ptBuf := make([]byte, 0, ptLen)

	b.SetBytes(int64(ptLen))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := uint64(i)
		binary.BigEndian.PutUint64(hdr[20:28], n)

		send.SetNonce(n)
		ct, err := send.Encrypt(ctBuf[:0], hdr, pt)
		if err != nil {
			b.Fatal(err)
		}
		recv.SetNonce(n)
		if _, err := recv.Decrypt(ptBuf[:0], hdr, ct); err != nil {
			b.Fatal(err)
		}
	}
}

// SPIKE-30..33: seal+open across representative inner packet sizes.
func BenchmarkSealOpen64(b *testing.B)   { benchSealOpen(b, 64) }
func BenchmarkSealOpen256(b *testing.B)  { benchSealOpen(b, 256) }
func BenchmarkSealOpen768(b *testing.B)  { benchSealOpen(b, 768) }
func BenchmarkSealOpen1180(b *testing.B) { benchSealOpen(b, 1180) }

// benchSealCopies measures sealing one 1180-byte inner packet into nPaths
// independent path copies (the replication cost per logical DATA packet).
func benchSealCopies(b *testing.B, nPaths int) {
	const ptLen = 1180
	pt := make([]byte, ptLen)
	_, _ = rand.Read(pt)

	sends := make([]*noise.CipherState, nPaths)
	hdrs := make([][]byte, nPaths)
	bufs := make([][]byte, nPaths)
	for p := range sends {
		k := randKey(b)
		sends[p] = noise.UnsafeNewCipherState(suite, k, 0)
		hdrs[p] = newHeader(0)
		bufs[p] = make([]byte, 0, ptLen+16)
	}

	b.SetBytes(int64(ptLen)) // one logical packet's worth of application data
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := uint64(i)
		for p := 0; p < nPaths; p++ {
			binary.BigEndian.PutUint64(hdrs[p][20:28], n)
			sends[p].SetNonce(n)
			if _, err := sends[p].Encrypt(bufs[p][:0], hdrs[p], pt); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// SPIKE-34..36: 1, 2, and 4 independently sealed path copies.
func BenchmarkSealCopies1(b *testing.B) { benchSealCopies(b, 1) }
func BenchmarkSealCopies2(b *testing.B) { benchSealCopies(b, 2) }
func BenchmarkSealCopies4(b *testing.B) { benchSealCopies(b, 4) }
