// Package reason defines the closed sets of reason codes RED_MPUDP uses for
// packet drops, handshake failures, session closes, and path health
// transitions.
//
// Every reason is a small integer constant with a fixed, hand-written string
// form. There is deliberately no function that turns a string into a reason:
// a value that crosses the wire or comes from a peer can never become a reason
// code, so logs and metrics labels never carry attacker-controlled text. An
// unknown numeric value renders as "unknown(N)" rather than panicking, so a
// forward-compatible peer cannot crash an older build.
package reason

import "strconv"

// DropReason explains why a packet was not delivered.
type DropReason uint8

const (
	DropUnspecified      DropReason = iota // no reason recorded
	DropQueueFullPackets                   // egress queue at its packet limit
	DropQueueFullBytes                     // egress queue at its byte budget
	DropReplicaDeadline                    // replica passed its send deadline (§9.4)
	DropDuplicate                          // first-copy-wins dedup rejected a later copy
	DropOutsideWindow                      // sequence number outside the dedup window
	DropDecryptFailed                      // AEAD authentication failed
	DropShortPacket                        // packet shorter than the transport header
	DropUnknownPath                        // no live path incarnation for this packet
	DropNoRoute                            // inner destination not routable
	DropInnerTooLarge                      // inner packet exceeds negotiated inner MTU
	DropPathDown                           // selected path is not currently healthy
	DropShuttingDown                       // session is draining or closed

	dropMax // sentinel; keep last
)

var dropText = [dropMax]string{
	DropUnspecified:      "unspecified",
	DropQueueFullPackets: "queue_full_packets",
	DropQueueFullBytes:   "queue_full_bytes",
	DropReplicaDeadline:  "replica_deadline",
	DropDuplicate:        "duplicate",
	DropOutsideWindow:    "outside_window",
	DropDecryptFailed:    "decrypt_failed",
	DropShortPacket:      "short_packet",
	DropUnknownPath:      "unknown_path",
	DropNoRoute:          "no_route",
	DropInnerTooLarge:    "inner_too_large",
	DropPathDown:         "path_down",
	DropShuttingDown:     "shutting_down",
}

// String returns the stable snake_case label, or "unknown(N)".
func (r DropReason) String() string { return lookup(uint8(r), dropText[:]) }

// Valid reports whether r is a defined reason code.
func (r DropReason) Valid() bool { return r < dropMax }

// HandshakeFailure explains why a Noise handshake did not complete.
type HandshakeFailure uint8

const (
	HandshakeUnspecified   HandshakeFailure = iota
	HandshakeBadMagic                       // wrong 4-byte magic
	HandshakeBadVersion                     // unsupported version byte
	HandshakeMalformed                      // message could not be parsed
	HandshakeDecryptFailed                  // Noise ReadMessage rejected the message
	HandshakeUnknownPeer                    // client static key not in the allow list
	HandshakeBadPSK                         // pre-shared key mismatch
	HandshakeReplayed                       // handshake nonce or timestamp replay
	HandshakeTimeout                        // no response within the handshake deadline
	HandshakeRateLimited                    // too many attempts from this source
	HandshakeShuttingDown                   // listener draining

	handshakeMax
)

var handshakeText = [handshakeMax]string{
	HandshakeUnspecified:   "unspecified",
	HandshakeBadMagic:      "bad_magic",
	HandshakeBadVersion:    "bad_version",
	HandshakeMalformed:     "malformed",
	HandshakeDecryptFailed: "decrypt_failed",
	HandshakeUnknownPeer:   "unknown_peer",
	HandshakeBadPSK:        "bad_psk",
	HandshakeReplayed:      "replayed",
	HandshakeTimeout:       "timeout",
	HandshakeRateLimited:   "rate_limited",
	HandshakeShuttingDown:  "shutting_down",
}

// String returns the stable snake_case label, or "unknown(N)".
func (f HandshakeFailure) String() string { return lookup(uint8(f), handshakeText[:]) }

// Valid reports whether f is a defined failure code.
func (f HandshakeFailure) Valid() bool { return f < handshakeMax }

// CloseReason explains why a session or path incarnation was torn down.
type CloseReason uint8

const (
	CloseUnspecified     CloseReason = iota
	CloseLocalShutdown               // operator asked the daemon to stop
	CloseConfigReload                // configuration change forced a rebuild
	ClosePeerGone                    // peer sent a close or became unreachable
	CloseIdleTimeout                 // no traffic within the idle limit
	CloseKeyExpired                  // rekey deadline passed without renewal
	CloseHandshakeFailed             // path incarnation never established
	CloseAllPathsDown                // no usable path remained
	CloseProtocolError               // unrecoverable wire-protocol violation
	CloseNonceExhausted              // AEAD nonce space for a path was exhausted

	closeMax
)

var closeText = [closeMax]string{
	CloseUnspecified:     "unspecified",
	CloseLocalShutdown:   "local_shutdown",
	CloseConfigReload:    "config_reload",
	ClosePeerGone:        "peer_gone",
	CloseIdleTimeout:     "idle_timeout",
	CloseKeyExpired:      "key_expired",
	CloseHandshakeFailed: "handshake_failed",
	CloseAllPathsDown:    "all_paths_down",
	CloseProtocolError:   "protocol_error",
	CloseNonceExhausted:  "nonce_exhausted",
}

// String returns the stable snake_case label, or "unknown(N)".
func (r CloseReason) String() string { return lookup(uint8(r), closeText[:]) }

// Valid reports whether r is a defined close code.
func (r CloseReason) Valid() bool { return r < closeMax }

// HealthTransition names an edge in the per-path health state machine (§10).
type HealthTransition uint8

const (
	HealthUnspecified HealthTransition = iota
	HealthProbing                      // new incarnation, probing
	HealthUp                           // probes/traffic succeeding, path usable
	HealthDegraded                     // loss or latency past the degraded threshold
	HealthDown                         // consecutive probe failures, path unusable
	HealthRecovered                    // returned to Up from Degraded or Down
	HealthRetired                      // incarnation replaced or abandoned

	healthMax
)

var healthText = [healthMax]string{
	HealthUnspecified: "unspecified",
	HealthProbing:     "probing",
	HealthUp:          "up",
	HealthDegraded:    "degraded",
	HealthDown:        "down",
	HealthRecovered:   "recovered",
	HealthRetired:     "retired",
}

// String returns the stable snake_case label, or "unknown(N)".
func (t HealthTransition) String() string { return lookup(uint8(t), healthText[:]) }

// Valid reports whether t is a defined transition code.
func (t HealthTransition) Valid() bool { return t < healthMax }

// lookup returns table[v] if v is in range, else "unknown(v)".
func lookup(v uint8, table []string) string {
	if int(v) < len(table) {
		return table[v]
	}
	return "unknown(" + strconv.Itoa(int(v)) + ")"
}
