// Package noisehandshake will hold the RED_MPUDP OPEN/JOIN Noise IKpsk2
// handshake state machine (milestone M11).
//
// During M02 Phase 0 it contains only spike tests. They validate that
// github.com/flynn/noise satisfies the requirements the design places on the
// handshake and transport cipher (design §4.4, §6.2, §7.1) without a private
// fork:
//
//   - pattern_test.go  - IKpsk2 handshake, directional key separation, and the
//     negative cases (wrong static key, wrong PSK, wrong prologue, tampered
//     transcript).
//   - nonce_test.go    - explicit per-packet nonce control for out-of-order
//     transport datagrams.
//   - vectors_test.go  - flynn/noise run against an independent implementation's
//     published test vectors.
package noisehandshake
