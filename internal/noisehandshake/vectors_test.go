package noisehandshake

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/flynn/noise"
)

// SPIKE-20: run flynn/noise against an independent implementation's published
// test vectors (cacophony, the Noise reference implementation's vector set).
//
// The file is vendored at test/vectors/noise/cacophony.txt with its SHA-256 in
// test/vectors/noise/SHA256SUMS; test/vectors/noise/fetch.sh re-downloads and
// verifies it.

type vector struct {
	ProtocolName     string   `json:"protocol_name"`
	InitPrologue     string   `json:"init_prologue"`
	InitPSKs         []string `json:"init_psks"`
	InitStatic       string   `json:"init_static"`
	InitEphemeral    string   `json:"init_ephemeral"`
	InitRemoteStatic string   `json:"init_remote_static"`
	RespPrologue     string   `json:"resp_prologue"`
	RespPSKs         []string `json:"resp_psks"`
	RespStatic       string   `json:"resp_static"`
	RespEphemeral    string   `json:"resp_ephemeral"`
	RespRemoteStatic string   `json:"resp_remote_static"`
	HandshakeHash    string   `json:"handshake_hash"`
	Messages         []vecMsg `json:"messages"`
}

type vecMsg struct {
	Payload    string `json:"payload"`
	Ciphertext string `json:"ciphertext"`
}

// preMessage records which static keys each side holds before the handshake for
// a given base pattern.
type preMessage struct {
	initStatic, initKnowsResp bool
	respStatic, respKnowsInit bool
}

var basePatterns = map[string]struct {
	pat noise.HandshakePattern
	pre preMessage
}{
	"NN": {noise.HandshakeNN, preMessage{}},
	"NK": {noise.HandshakeNK, preMessage{initKnowsResp: true, respStatic: true}},
	"KK": {noise.HandshakeKK, preMessage{initStatic: true, initKnowsResp: true, respStatic: true, respKnowsInit: true}},
	"XX": {noise.HandshakeXX, preMessage{initStatic: true, respStatic: true}},
	"IK": {noise.HandshakeIK, preMessage{initStatic: true, initKnowsResp: true, respStatic: true}},
}

var vecCiphers = map[string]noise.CipherFunc{
	"AESGCM":     noise.CipherAESGCM,
	"ChaChaPoly": noise.CipherChaChaPoly,
}

var vecHashes = map[string]noise.HashFunc{
	"SHA256":  noise.HashSHA256,
	"SHA512":  noise.HashSHA512,
	"BLAKE2b": noise.HashBLAKE2b,
	"BLAKE2s": noise.HashBLAKE2s,
}

// allowlist: the protocol names we assert on. allowlist[0] is the RED_MPUDP v1
// suite and MUST be present in the vector file.
var allowlist = []string{
	"Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s", // RED_MPUDP v1 (design D2)
	"Noise_IK_25519_ChaChaPoly_BLAKE2s",
	"Noise_IKpsk1_25519_ChaChaPoly_BLAKE2s",
	"Noise_XX_25519_ChaChaPoly_BLAKE2s",
	"Noise_XXpsk3_25519_ChaChaPoly_BLAKE2s",
	"Noise_NN_25519_ChaChaPoly_BLAKE2s",
	"Noise_NK_25519_ChaChaPoly_BLAKE2s",
	"Noise_KK_25519_ChaChaPoly_BLAKE2s",
	"Noise_IK_25519_ChaChaPoly_SHA256",
	"Noise_IK_25519_AESGCM_BLAKE2s",
}

var protoRE = regexp.MustCompile(`^Noise_([A-Z1]+?)(psk[0-3])?_(25519|448)_(AESGCM|ChaChaPoly)_(SHA256|SHA512|BLAKE2b|BLAKE2s)$`)

func TestFlynnNoiseAgainstCacophonyVectors(t *testing.T) {
	path := filepath.Join("..", "..", "test", "vectors", "noise", "cacophony.txt")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vectors: %v (run test/vectors/noise/fetch.sh)", err)
	}
	var file struct {
		Vectors []vector `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}

	byName := make(map[string]vector, len(file.Vectors))
	for _, v := range file.Vectors {
		if _, dup := byName[v.ProtocolName]; !dup {
			byName[v.ProtocolName] = v
		}
	}

	ran := 0
	for i, name := range allowlist {
		v, ok := byName[name]
		if !ok {
			if i == 0 {
				t.Fatalf("required suite %q missing from the vector file", name)
			}
			t.Logf("skip %q: not in vector file", name)
			continue
		}
		t.Run(name, func(t *testing.T) { runVector(t, v) })
		ran++
	}
	if ran == 0 {
		t.Fatal("no vectors ran")
	}
	t.Logf("validated flynn/noise against %d cacophony vectors", ran)
}

func runVector(t *testing.T, v vector) {
	m := protoRE.FindStringSubmatch(v.ProtocolName)
	if m == nil {
		t.Skipf("unparseable protocol name %q", v.ProtocolName)
	}
	base, pskMod, dh, cipherName, hashName := m[1], m[2], m[3], m[4], m[5]
	if dh != "25519" {
		t.Skipf("DH %s not supported by flynn/noise", dh)
	}
	bp, ok := basePatterns[base]
	if !ok {
		t.Skipf("base pattern %s not wired in this spike", base)
	}

	cs := noise.NewCipherSuite(noise.DH25519, vecCiphers[cipherName], vecHashes[hashName])

	pskPlacement := 0
	if pskMod != "" {
		pskPlacement = int(pskMod[len("psk")] - '0')
	}

	// The `e` token generates the ephemeral from Config.Random, so feed the
	// vector's ephemeral private bytes as the "random" source (one 32-byte
	// ephemeral per side for every pattern in the allowlist).
	initCfg := noise.Config{
		CipherSuite:           cs,
		Pattern:               bp.pat,
		Initiator:             true,
		Prologue:              decodeHex(t, v.InitPrologue),
		Random:                bytes.NewReader(decodeHex(t, v.InitEphemeral)),
		PresharedKeyPlacement: pskPlacement,
	}
	respCfg := noise.Config{
		CipherSuite:           cs,
		Pattern:               bp.pat,
		Initiator:             false,
		Prologue:              decodeHex(t, v.RespPrologue),
		Random:                bytes.NewReader(decodeHex(t, v.RespEphemeral)),
		PresharedKeyPlacement: pskPlacement,
	}
	if bp.pre.initStatic {
		initCfg.StaticKeypair = keypair(t, v.InitStatic)
	}
	if bp.pre.respStatic {
		respCfg.StaticKeypair = keypair(t, v.RespStatic)
	}
	if bp.pre.initKnowsResp {
		initCfg.PeerStatic = decodeHex(t, v.InitRemoteStatic)
	}
	if bp.pre.respKnowsInit {
		respCfg.PeerStatic = decodeHex(t, v.RespRemoteStatic)
	}
	if pskPlacement > 0 {
		initCfg.PresharedKey = decodeHex(t, v.InitPSKs[0])
		respCfg.PresharedKey = decodeHex(t, v.RespPSKs[0])
	}

	hsI, err := noise.NewHandshakeState(initCfg)
	if err != nil {
		t.Fatalf("initiator: %v", err)
	}
	hsR, err := noise.NewHandshakeState(respCfg)
	if err != nil {
		t.Fatalf("responder: %v", err)
	}

	var iSend, iRecv, rSend, rRecv *noise.CipherState
	assign := func(isInit bool, cs0, cs1 *noise.CipherState) {
		if isInit {
			iSend, iRecv = cs0, cs1
		} else {
			rSend, rRecv = cs1, cs0
		}
	}

	writerIsInit := true
	hsDone := false
	for i, msg := range v.Messages {
		wantCT := decodeHex(t, msg.Ciphertext)
		payload := decodeHex(t, msg.Payload)

		if !hsDone {
			wr, rd := hsI, hsR
			if !writerIsInit {
				wr, rd = hsR, hsI
			}
			out, wcs0, wcs1, err := wr.WriteMessage(nil, payload)
			if err != nil {
				t.Fatalf("msg %d WriteMessage: %v", i, err)
			}
			if !bytes.Equal(out, wantCT) {
				t.Fatalf("msg %d ciphertext mismatch\n got %x\nwant %x", i, out, wantCT)
			}
			in, rcs0, rcs1, err := rd.ReadMessage(nil, out)
			if err != nil {
				t.Fatalf("msg %d ReadMessage: %v", i, err)
			}
			if !bytes.Equal(in, payload) {
				t.Fatalf("msg %d payload mismatch", i)
			}
			if wcs0 != nil {
				assign(writerIsInit, wcs0, wcs1)
				assign(!writerIsInit, rcs0, rcs1)
				hsDone = true
				checkHandshakeHash(t, v, wr, rd)
			}
			writerIsInit = !writerIsInit
			continue
		}

		enc, dec := iSend, rRecv
		if !writerIsInit {
			enc, dec = rSend, iRecv
		}
		out, err := enc.Encrypt(nil, nil, payload)
		if err != nil {
			t.Fatalf("transport msg %d Encrypt: %v", i, err)
		}
		if !bytes.Equal(out, wantCT) {
			t.Fatalf("transport msg %d ciphertext mismatch\n got %x\nwant %x", i, out, wantCT)
		}
		back, err := dec.Decrypt(nil, nil, out)
		if err != nil {
			t.Fatalf("transport msg %d Decrypt: %v", i, err)
		}
		if !bytes.Equal(back, payload) {
			t.Fatalf("transport msg %d payload mismatch", i)
		}
		writerIsInit = !writerIsInit
	}
	if !hsDone {
		t.Fatal("handshake never completed")
	}
}

func checkHandshakeHash(t *testing.T, v vector, a, b *noise.HandshakeState) {
	t.Helper()
	if v.HandshakeHash == "" {
		return
	}
	want := decodeHex(t, v.HandshakeHash)
	if got := a.ChannelBinding(); !bytes.Equal(got, want) {
		t.Fatalf("handshake hash (writer) mismatch\n got %x\nwant %x", got, want)
	}
	if got := b.ChannelBinding(); !bytes.Equal(got, want) {
		t.Fatalf("handshake hash (reader) mismatch\n got %x\nwant %x", got, want)
	}
}

func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func keypair(t *testing.T, privHex string) noise.DHKey {
	t.Helper()
	priv := decodeHex(t, privHex)
	if len(priv) == 0 {
		return noise.DHKey{}
	}
	k, err := noise.DH25519.GenerateKeypair(bytes.NewReader(priv))
	if err != nil {
		t.Fatalf("keypair from %q: %v", privHex, err)
	}
	return k
}
