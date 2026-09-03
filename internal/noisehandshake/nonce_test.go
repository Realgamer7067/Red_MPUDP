package noisehandshake

import (
	"bytes"
	"encoding/binary"
	"sync"
	"testing"

	"github.com/flynn/noise"
)

// transportHeader builds a 44-byte design-shaped header whose bytes 20..28 hold
// the big-endian outer packet number, mirroring design §6.2 (the whole header
// is the AEAD AAD, and the nonce is derived from outer_packet_no).
func transportHeader(outerPacketNo uint64) []byte {
	h := make([]byte, 44)
	copy(h[0:4], "RMPU")
	h[4] = 0x10 // ver=1, type=DATA
	binary.BigEndian.PutUint16(h[6:8], 44)
	binary.BigEndian.PutUint64(h[20:28], outerPacketNo)
	return h
}

// sealPacket encrypts pt for outer packet number n. The send cipher state's
// nonce is set explicitly to n immediately before the operation.
func sealPacket(t *testing.T, send *noise.CipherState, n uint64, pt []byte) (hdr, ct []byte) {
	t.Helper()
	hdr = transportHeader(n)
	send.SetNonce(n)
	c, err := send.Encrypt(nil, hdr, pt)
	if err != nil {
		t.Fatalf("seal n=%d: %v", n, err)
	}
	return hdr, c
}

// openPacket authenticates/decrypts a received datagram: parse n from the
// header, set the receive nonce to n, then Decrypt with the full header as AAD.
func openPacket(recv *noise.CipherState, hdr, ct []byte) ([]byte, error) {
	n := binary.BigEndian.Uint64(hdr[20:28])
	recv.SetNonce(n)
	return recv.Decrypt(nil, hdr, ct)
}

func transportPair(t *testing.T) (send, recv *noise.CipherState) {
	t.Helper()
	r := runIKpsk2(t, defaultParams(t))
	if r.err1 != nil || r.err2 != nil {
		t.Fatalf("handshake failed: %v / %v", r.err1, r.err2)
	}
	return r.iSend, r.rRecv
}

// SPIKE-22, SPIKE-23, SPIKE-24: seal packets with nonces {0,1,2,4097}, deliver
// them in the order {2,0,4097,1}, and confirm each opens exactly once with the
// right plaintext.
func TestOutOfOrderNoncesOpenOnce(t *testing.T) {
	send, recv := transportPair(t)

	type pkt struct {
		n   uint64
		hdr []byte
		ct  []byte
		pt  []byte
	}
	nonces := []uint64{0, 1, 2, 4097}
	pkts := make(map[uint64]pkt, len(nonces))
	for _, n := range nonces {
		pt := []byte("packet-" + string(rune('A'+int(n%26))))
		hdr, ct := sealPacket(t, send, n, pt)
		pkts[n] = pkt{n: n, hdr: hdr, ct: ct, pt: pt}
	}

	var replay sync.Map // uint64 -> struct{}
	deliver := func(n uint64) {
		p := pkts[n]
		got, err := openPacket(recv, p.hdr, p.ct)
		if err != nil {
			t.Fatalf("open n=%d: %v", n, err)
		}
		if !bytes.Equal(got, p.pt) {
			t.Fatalf("open n=%d: got %q want %q", n, got, p.pt)
		}
		if _, dup := replay.LoadOrStore(n, struct{}{}); dup {
			t.Fatalf("n=%d opened twice", n)
		}
	}

	for _, n := range []uint64{2, 0, 4097, 1} {
		deliver(n)
	}
}

// SPIKE-25: the spike's replay set rejects a repeated nonce/ciphertext.
func TestReplayRejectsRepeatedNonce(t *testing.T) {
	send, recv := transportPair(t)
	hdr, ct := sealPacket(t, send, 7, []byte("once"))

	seen := map[uint64]bool{}
	accept := func() error {
		n := binary.BigEndian.Uint64(hdr[20:28])
		if seen[n] {
			return errReplayed
		}
		if _, err := openPacket(recv, hdr, ct); err != nil {
			return err
		}
		seen[n] = true
		return nil
	}

	if err := accept(); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := accept(); err != errReplayed {
		t.Fatalf("second delivery: got %v, want errReplayed", err)
	}
}

var errReplayed = &replayErr{}

type replayErr struct{}

func (*replayErr) Error() string { return "replayed outer packet number" }

// SPIKE-26: changing any authenticated header byte breaks AEAD.
func TestHeaderTamperingBreaksAEAD(t *testing.T) {
	send, recv := transportPair(t)
	hdr, ct := sealPacket(t, send, 3, []byte("authenticated"))

	for i := 0; i < len(hdr); i++ {
		bad := append([]byte(nil), hdr...)
		bad[i] ^= 0x01
		// Nonce is still parsed from bytes 20..28; keep it consistent so we are
		// really testing AAD authentication, not a nonce mismatch, except for
		// the nonce bytes themselves which must also fail.
		recv.SetNonce(binary.BigEndian.Uint64(hdr[20:28]))
		if _, err := recv.Decrypt(nil, bad, ct); err == nil {
			t.Fatalf("Decrypt accepted header with byte %d flipped", i)
		}
	}
}

// SPIKE-27: an authentication failure does not prevent a later valid packet
// with a lower nonce from opening, because SetNonce is called again.
func TestAuthFailureDoesNotWedgeReceiver(t *testing.T) {
	send, recv := transportPair(t)

	hdr0, ct0 := sealPacket(t, send, 0, []byte("zero"))
	hdr5, ct5 := sealPacket(t, send, 5, []byte("five"))

	// Deliver a forged packet claiming nonce 5.
	forged := append([]byte(nil), ct5...)
	forged[0] ^= 0xFF
	if _, err := openPacket(recv, hdr5, forged); err == nil {
		t.Fatal("forged ciphertext opened")
	}

	// The genuine nonce-0 packet must still open.
	got, err := openPacket(recv, hdr0, ct0)
	if err != nil {
		t.Fatalf("valid lower-nonce packet rejected after an auth failure: %v", err)
	}
	if !bytes.Equal(got, []byte("zero")) {
		t.Fatalf("got %q want %q", got, "zero")
	}

	// And the genuine nonce-5 packet too.
	got, err = openPacket(recv, hdr5, ct5)
	if err != nil {
		t.Fatalf("valid nonce-5 packet rejected: %v", err)
	}
	if !bytes.Equal(got, []byte("five")) {
		t.Fatalf("got %q want %q", got, "five")
	}
}

// SPIKE-28: two independently owned cipher states (one per path) can run
// concurrently with no data race. A single CipherState is never shared.
func TestPerPathCipherStatesAreIndependent(t *testing.T) {
	r := runIKpsk2(t, defaultParams(t))
	if r.err1 != nil || r.err2 != nil {
		t.Fatalf("handshake failed: %v / %v", r.err1, r.err2)
	}
	// Model: path A uses iSend/rRecv, path B uses rSend/iRecv. Distinct objects.
	var wg sync.WaitGroup
	for _, pair := range []struct{ s, r *noise.CipherState }{
		{r.iSend, r.rRecv},
		{r.rSend, r.iRecv},
	} {
		wg.Add(1)
		go func(s, rc *noise.CipherState) {
			defer wg.Done()
			for n := uint64(0); n < 64; n++ {
				hdr := transportHeader(n)
				s.SetNonce(n)
				ct, err := s.Encrypt(nil, hdr, []byte("x"))
				if err != nil {
					t.Errorf("encrypt n=%d: %v", n, err)
					return
				}
				rc.SetNonce(n)
				if _, err := rc.Decrypt(nil, hdr, ct); err != nil {
					t.Errorf("decrypt n=%d: %v", n, err)
					return
				}
			}
		}(pair.s, pair.r)
	}
	wg.Wait()
}
