package reason_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/reason"
)

// BASE-23: every defined code has a stable, non-empty, unique string form, and
// String never returns Go's default "%!d(...)" style or an empty label. The
// table is checked exhaustively up to the sentinel so adding a code without a
// label fails here rather than silently rendering "unknown(N)".

func TestDropReasonStringsExhaustiveAndStable(t *testing.T) {
	want := map[reason.DropReason]string{
		reason.DropUnspecified:      "unspecified",
		reason.DropQueueFullPackets: "queue_full_packets",
		reason.DropQueueFullBytes:   "queue_full_bytes",
		reason.DropReplicaDeadline:  "replica_deadline",
		reason.DropDuplicate:        "duplicate",
		reason.DropOutsideWindow:    "outside_window",
		reason.DropDecryptFailed:    "decrypt_failed",
		reason.DropShortPacket:      "short_packet",
		reason.DropUnknownPath:      "unknown_path",
		reason.DropNoRoute:          "no_route",
		reason.DropInnerTooLarge:    "inner_too_large",
		reason.DropPathDown:         "path_down",
		reason.DropShuttingDown:     "shutting_down",
	}
	assertEnum(t, want, func(i int) (fmtStringer, bool) {
		r := reason.DropReason(i)
		return r, r.Valid()
	})
	if got := reason.DropReason(250).String(); got != "unknown(250)" {
		t.Fatalf("out-of-range DropReason = %q, want unknown(250)", got)
	}
	if reason.DropReason(250).Valid() {
		t.Fatal("out-of-range DropReason reported Valid")
	}
}

func TestHandshakeFailureStrings(t *testing.T) {
	want := map[reason.HandshakeFailure]string{
		reason.HandshakeUnspecified:   "unspecified",
		reason.HandshakeBadMagic:      "bad_magic",
		reason.HandshakeBadVersion:    "bad_version",
		reason.HandshakeMalformed:     "malformed",
		reason.HandshakeDecryptFailed: "decrypt_failed",
		reason.HandshakeUnknownPeer:   "unknown_peer",
		reason.HandshakeBadPSK:        "bad_psk",
		reason.HandshakeReplayed:      "replayed",
		reason.HandshakeTimeout:       "timeout",
		reason.HandshakeRateLimited:   "rate_limited",
		reason.HandshakeShuttingDown:  "shutting_down",
	}
	assertEnum(t, want, func(i int) (fmtStringer, bool) {
		f := reason.HandshakeFailure(i)
		return f, f.Valid()
	})
	if got := reason.HandshakeFailure(200).String(); got != "unknown(200)" {
		t.Fatalf("out-of-range = %q", got)
	}
}

func TestCloseReasonStrings(t *testing.T) {
	want := map[reason.CloseReason]string{
		reason.CloseUnspecified:     "unspecified",
		reason.CloseLocalShutdown:   "local_shutdown",
		reason.CloseConfigReload:    "config_reload",
		reason.ClosePeerGone:        "peer_gone",
		reason.CloseIdleTimeout:     "idle_timeout",
		reason.CloseKeyExpired:      "key_expired",
		reason.CloseHandshakeFailed: "handshake_failed",
		reason.CloseAllPathsDown:    "all_paths_down",
		reason.CloseProtocolError:   "protocol_error",
		reason.CloseNonceExhausted:  "nonce_exhausted",
	}
	assertEnum(t, want, func(i int) (fmtStringer, bool) {
		r := reason.CloseReason(i)
		return r, r.Valid()
	})
}

func TestHealthTransitionStrings(t *testing.T) {
	want := map[reason.HealthTransition]string{
		reason.HealthUnspecified: "unspecified",
		reason.HealthProbing:     "probing",
		reason.HealthUp:          "up",
		reason.HealthDegraded:    "degraded",
		reason.HealthDown:        "down",
		reason.HealthRecovered:   "recovered",
		reason.HealthRetired:     "retired",
	}
	assertEnum(t, want, func(i int) (fmtStringer, bool) {
		tr := reason.HealthTransition(i)
		return tr, tr.Valid()
	})
}

// BASE-24: no path from attacker-controlled text to a reason code. The package
// must expose no string->enum constructor (Parse/FromString/Unmarshal/etc.).
func TestNoStringToReasonConstructor(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	banned := []string{"parse", "fromstring", "unmarshal", "decode", "scan"}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || !fn.Name.IsExported() {
					continue
				}
				lname := strings.ToLower(fn.Name.Name)
				for _, b := range banned {
					if strings.Contains(lname, b) {
						t.Errorf("exported function %q looks like a string->reason constructor; BASE-24 forbids one", fn.Name.Name)
					}
				}
				// Any exported function taking a string parameter is susp
				// unless it is a well-known interface method.
				if fn.Recv == nil && takesString(fn) {
					t.Errorf("exported package function %q takes a string; reason codes must never be built from text", fn.Name.Name)
				}
			}
		}
	}
}

type fmtStringer interface{ String() string }

func assertEnum[E comparable](t *testing.T, want map[E]string, at func(int) (fmtStringer, bool)) {
	t.Helper()
	seen := map[string]bool{}
	for i := 0; ; i++ {
		v, valid := at(i)
		if !valid {
			break
		}
		s := v.String()
		if s == "" || strings.HasPrefix(s, "unknown(") || strings.Contains(s, "%!") {
			t.Fatalf("value %d has bad String() %q", i, s)
		}
		if ev, ok := any(v).(E); ok {
			if w, present := want[ev]; !present {
				t.Fatalf("value %d (%q) missing from the test's expected map", i, s)
			} else if w != s {
				t.Fatalf("value %d String() = %q, want %q", i, s, w)
			}
		}
		if seen[s] {
			t.Fatalf("duplicate string form %q", s)
		}
		seen[s] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("defined codes = %d, expected map has %d", len(seen), len(want))
	}
}

func takesString(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, p := range fn.Type.Params.List {
		if id, ok := p.Type.(*ast.Ident); ok && id.Name == "string" {
			return true
		}
	}
	return false
}
