package noisehandshake

import (
	"bytes"
	"testing"

	"github.com/flynn/noise"
)

// suite is Noise_..._25519_ChaChaPoly_BLAKE2s from design D2.
var suite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

// prologue is the exact value from design §4.4.
const prologue = "RED_MPUDP/v1"

// deterministicKey derives a fixed X25519 keypair from a repeated seed byte so
// tests do not depend on a random source (SPIKE-10).
func deterministicKey(t *testing.T, seed byte) noise.DHKey {
	t.Helper()
	k, err := noise.DH25519.GenerateKeypair(bytes.NewReader(bytes.Repeat([]byte{seed}, 32)))
	if err != nil {
		t.Fatalf("GenerateKeypair(seed=%d): %v", seed, err)
	}
	return k
}

type handshakeParams struct {
	clientStatic  noise.DHKey
	serverStatic  noise.DHKey
	clientEph     noise.DHKey
	serverEph     noise.DHKey
	psk           []byte
	clientPeerPub []byte // server static pub as the client believes it to be
	clientProlog  string
	serverProlog  string
}

func defaultParams(t *testing.T) handshakeParams {
	t.Helper()
	cs := deterministicKey(t, 0x11)
	ss := deterministicKey(t, 0x22)
	return handshakeParams{
		clientStatic:  cs,
		serverStatic:  ss,
		clientEph:     deterministicKey(t, 0x33),
		serverEph:     deterministicKey(t, 0x44),
		psk:           bytes.Repeat([]byte{0x55}, 32),
		clientPeerPub: ss.Public,
		clientProlog:  prologue,
		serverProlog:  prologue,
	}
}

type handshakeResult struct {
	msg1, msg2       []byte
	payload1         []byte
	payload2         []byte
	err1, err2       error // responder ReadMessage(msg1), initiator ReadMessage(msg2)
	iSend, iRecv     *noise.CipherState
	rRecv, rSend     *noise.CipherState
	serverSeesClient []byte
}

// runIKpsk2 drives a full two-message IKpsk2 handshake with the given params
// and returns every intermediate value the assertions need.
func runIKpsk2(t *testing.T, p handshakeParams) handshakeResult {
	t.Helper()

	hsI, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           suite,
		Pattern:               noise.HandshakeIK,
		Initiator:             true,
		Prologue:              []byte(p.clientProlog),
		PresharedKey:          p.psk,
		PresharedKeyPlacement: 2,
		StaticKeypair:         p.clientStatic,
		EphemeralKeypair:      p.clientEph,
		PeerStatic:            p.clientPeerPub,
	})
	if err != nil {
		t.Fatalf("initiator NewHandshakeState: %v", err)
	}
	hsR, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           suite,
		Pattern:               noise.HandshakeIK,
		Initiator:             false,
		Prologue:              []byte(p.serverProlog),
		PresharedKey:          p.psk,
		PresharedKeyPlacement: 2,
		StaticKeypair:         p.serverStatic,
		EphemeralKeypair:      p.serverEph,
	})
	if err != nil {
		t.Fatalf("responder NewHandshakeState: %v", err)
	}

	var res handshakeResult

	res.msg1, _, _, err = hsI.WriteMessage(nil, []byte("OPEN"))
	if err != nil {
		t.Fatalf("initiator WriteMessage(msg1): %v", err)
	}
	res.payload1, _, _, res.err1 = hsR.ReadMessage(nil, res.msg1)
	if res.err1 != nil {
		return res // caller decides whether that is expected
	}
	res.serverSeesClient = hsR.PeerStatic()

	res.msg2, res.rRecv, res.rSend, err = hsR.WriteMessage(nil, []byte("OPEN_ACK"))
	if err != nil {
		t.Fatalf("responder WriteMessage(msg2): %v", err)
	}
	res.payload2, res.iSend, res.iRecv, res.err2 = hsI.ReadMessage(nil, res.msg2)
	return res
}

// SPIKE-11, SPIKE-12, SPIKE-13: a clean IKpsk2 handshake completes and both
// peers derive matching cipher states in both directions.
func TestIKpsk2HandshakeCompletes(t *testing.T) {
	r := runIKpsk2(t, defaultParams(t))
	if r.err1 != nil {
		t.Fatalf("responder rejected msg1: %v", r.err1)
	}
	if r.err2 != nil {
		t.Fatalf("initiator rejected msg2: %v", r.err2)
	}
	if string(r.payload1) != "OPEN" {
		t.Errorf("responder read payload %q, want %q", r.payload1, "OPEN")
	}
	if string(r.payload2) != "OPEN_ACK" {
		t.Errorf("initiator read payload %q, want %q", r.payload2, "OPEN_ACK")
	}
	for _, c := range []*noise.CipherState{r.iSend, r.iRecv, r.rRecv, r.rSend} {
		if c == nil {
			t.Fatal("handshake did not yield both split cipher states on both sides")
		}
	}

	// initiator send  <-> responder receive  (first split state)
	assertPair(t, "initiator->responder", r.iSend, r.rRecv)
	// responder send  <-> initiator receive  (second split state)
	assertPair(t, "responder->initiator", r.rSend, r.iRecv)
}

// assertPair encrypts with enc and decrypts with dec, asserting agreement and
// that the 44-byte-style AAD is authenticated.
func assertPair(t *testing.T, name string, enc, dec *noise.CipherState) {
	t.Helper()
	aad := bytes.Repeat([]byte{0xAB}, 44)
	pt := []byte("latency packet")

	ct, err := enc.Encrypt(nil, aad, pt)
	if err != nil {
		t.Fatalf("%s: Encrypt: %v", name, err)
	}
	// Both sides must set the nonce explicitly per design §6.2; here both are
	// still at 0 so a plain Decrypt matches, but assert via SetNonce too.
	dec.SetNonce(0)
	got, err := dec.Decrypt(nil, aad, ct)
	if err != nil {
		t.Fatalf("%s: Decrypt: %v", name, err)
	}
	if !bytes.Equal(got, pt) {
		t.Fatalf("%s: round-trip mismatch: got %q want %q", name, got, pt)
	}
	// Tamper the AAD -> auth failure.
	badAAD := append([]byte(nil), aad...)
	badAAD[0] ^= 0x01
	dec.SetNonce(0)
	if _, err := dec.Decrypt(nil, badAAD, ct); err == nil {
		t.Fatalf("%s: Decrypt accepted tampered AAD", name)
	}
}

// SPIKE-14: the two transport directions do not share a key.
func TestSplitStatesUseDistinctKeys(t *testing.T) {
	r := runIKpsk2(t, defaultParams(t))
	if r.err1 != nil || r.err2 != nil {
		t.Fatalf("handshake failed: err1=%v err2=%v", r.err1, r.err2)
	}
	kSend := r.iSend.UnsafeKey()
	kRecv := r.iRecv.UnsafeKey()
	if kSend == kRecv {
		t.Fatal("initiator send and receive cipher states share a key")
	}
	// And the peer's mirrored states match ours.
	if r.rRecv.UnsafeKey() != kSend {
		t.Error("responder receive key != initiator send key")
	}
	if r.rSend.UnsafeKey() != kRecv {
		t.Error("responder send key != initiator receive key")
	}
}

// SPIKE-15: a wrong server static key (client pins the wrong key) fails.
func TestWrongServerStaticKeyFails(t *testing.T) {
	p := defaultParams(t)
	p.clientPeerPub = deterministicKey(t, 0x99).Public // not the real server
	r := runIKpsk2(t, p)
	if r.err1 == nil {
		t.Fatal("responder accepted msg1 built against the wrong server key")
	}
}

// SPIKE-16: a wrong client static key fails server authorization. Noise still
// completes, but the key the server authenticates is not the expected peer.
func TestWrongClientStaticKeyFailsAuthorization(t *testing.T) {
	p := defaultParams(t)
	expectedClient := p.clientStatic.Public
	p.clientStatic = deterministicKey(t, 0x77) // attacker's own static

	r := runIKpsk2(t, p)
	if r.err1 != nil {
		t.Fatalf("handshake unexpectedly failed at msg1: %v", r.err1)
	}
	// This is the check the server MUST perform (design §5.2 step 5): compare
	// the Noise-authenticated client static key against the configured peer.
	if bytes.Equal(r.serverSeesClient, expectedClient) {
		t.Fatal("server authenticated the expected client key despite a different static keypair")
	}
	if len(r.serverSeesClient) != 32 {
		t.Fatalf("PeerStatic() length = %d, want 32", len(r.serverSeesClient))
	}
}

// SPIKE-17: a wrong PSK fails. PSK is mixed at placement 2 (second message), so
// msg1 still parses but the initiator rejects msg2.
func TestWrongPSKFails(t *testing.T) {
	p := defaultParams(t)
	good := runIKpsk2(t, p)
	if good.err1 != nil || good.err2 != nil {
		t.Fatalf("baseline handshake failed: %v / %v", good.err1, good.err2)
	}

	// Re-run with the responder holding a different PSK.
	hsI, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Pattern: noise.HandshakeIK, Initiator: true,
		Prologue: []byte(prologue), PresharedKey: p.psk, PresharedKeyPlacement: 2,
		StaticKeypair: p.clientStatic, EphemeralKeypair: p.clientEph, PeerStatic: p.clientPeerPub,
	})
	if err != nil {
		t.Fatal(err)
	}
	hsR, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Pattern: noise.HandshakeIK, Initiator: false,
		Prologue: []byte(prologue), PresharedKey: bytes.Repeat([]byte{0x66}, 32), PresharedKeyPlacement: 2,
		StaticKeypair: p.serverStatic, EphemeralKeypair: p.serverEph,
	})
	if err != nil {
		t.Fatal(err)
	}
	msg1, _, _, err := hsI.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := hsR.ReadMessage(nil, msg1); err != nil {
		t.Fatalf("msg1 should still parse with a mismatched placement-2 PSK: %v", err)
	}
	msg2, _, _, err := hsR.WriteMessage(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := hsI.ReadMessage(nil, msg2); err == nil {
		t.Fatal("initiator accepted msg2 sealed under a different PSK")
	}
}

// SPIKE-18: a wrong prologue fails.
func TestWrongPrologueFails(t *testing.T) {
	p := defaultParams(t)
	p.serverProlog = "RED_MPUDP/v2"
	r := runIKpsk2(t, p)
	if r.err1 == nil {
		t.Fatal("responder accepted msg1 with a mismatched prologue")
	}
}

// SPIKE-19: transcript tampering fails.
func TestTranscriptTamperingFails(t *testing.T) {
	p := defaultParams(t)

	hsI, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Pattern: noise.HandshakeIK, Initiator: true,
		Prologue: []byte(prologue), PresharedKey: p.psk, PresharedKeyPlacement: 2,
		StaticKeypair: p.clientStatic, EphemeralKeypair: p.clientEph, PeerStatic: p.clientPeerPub,
	})
	if err != nil {
		t.Fatal(err)
	}
	hsR, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: suite, Pattern: noise.HandshakeIK, Initiator: false,
		Prologue: []byte(prologue), PresharedKey: p.psk, PresharedKeyPlacement: 2,
		StaticKeypair: p.serverStatic, EphemeralKeypair: p.serverEph,
	})
	if err != nil {
		t.Fatal(err)
	}

	msg1, _, _, err := hsI.WriteMessage(nil, []byte("OPEN"))
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, len(msg1) / 2, len(msg1) - 1} {
		tampered := append([]byte(nil), msg1...)
		tampered[i] ^= 0x01
		// fresh responder each attempt
		hs, err := noise.NewHandshakeState(noise.Config{
			CipherSuite: suite, Pattern: noise.HandshakeIK, Initiator: false,
			Prologue: []byte(prologue), PresharedKey: p.psk, PresharedKeyPlacement: 2,
			StaticKeypair: p.serverStatic, EphemeralKeypair: p.serverEph,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := hs.ReadMessage(nil, tampered); err == nil {
			t.Fatalf("responder accepted msg1 with byte %d flipped", i)
		}
	}
	_ = hsR
}
