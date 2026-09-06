package config_test

import (
	"strings"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/config"
)

func mutServer(t *testing.T, repl ...string) []byte {
	t.Helper()
	s := validServer
	for i := 0; i < len(repl); i += 2 {
		if !strings.Contains(s, repl[i]) {
			t.Fatalf("fragment %q not found in base server config", repl[i])
		}
		s = strings.Replace(s, repl[i], repl[i+1], 1)
	}
	return []byte(s)
}

func TestServerBoundaries(t *testing.T) {
	tests := []struct {
		name string
		repl []string
	}{
		// CONF-25: invalid subnet / address.
		{"bad subnet", []string{`subnet: "10.9.0.0/24"`, `subnet: "not-a-cidr"`}},
		{"bad address", []string{`address: "10.9.0.1/24"`, `address: "10.9.0.1"`}},
		{"address outside subnet", []string{`address: "10.9.0.1/24"`, `address: "10.8.0.1/24"`}},
		// CONF-08 equivalent for listen: needs a port.
		{"listen no port", []string{`listen: "0.0.0.0:51820"`, `listen: "0.0.0.0"`}},
		{"listen ipv6", []string{`listen: "0.0.0.0:51820"`, `listen: "[::]:51820"`}},
		// CONF-26: peer tunnel IP outside subnet.
		{"peer ip outside subnet", []string{`tunnel_ip: "10.9.0.2"`, `tunnel_ip: "10.20.0.2"`}},
		// CONF-13: path cap.
		{"max_paths 5", []string{"max_paths_per_session: 4", "max_paths_per_session: 5"}},
		// limits sanity.
		{"pending_per_source > pending", []string{"max_pending_per_source: 8", "max_pending_per_source: 9999"}},
		{"negative max_sessions", []string{"max_sessions: 64", "max_sessions: -1"}},
		{"bad retry_mode", []string{`retry_mode: "always"`, `retry_mode: "sometimes"`}},
		// CONF-12: MTU.
		{"mtu too high", []string{"  mtu: 1180", "  mtu: 1500"}},
		// NAT needs WAN interface.
		{"nat without wan", []string{`  wan_interface: "eth0"`, ""}},
		// identity required.
		{"no private key", []string{`  private_key_file: "/etc/red-mpudp/server.key"`, ""}},
		// CONF-29.
		{"public metrics no opt-in", []string{`metrics_addr: "127.0.0.1:9090"`, `metrics_addr: "192.0.2.5:9090"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := config.LoadServer(mutServer(t, tc.repl...)); err == nil {
				t.Fatalf("%s: expected rejection", tc.name)
			}
		})
	}
}

func twoPeerServer(name2, ip2 string) []byte {
	extra := "\n  - name: \"" + name2 + "\"" +
		"\n    public_key_file: \"/etc/red-mpudp/peers/p2.pub\"" +
		"\n    psk_file: \"/etc/red-mpudp/peers/p2.psk\"" +
		"\n    tunnel_ip: \"" + ip2 + "\"" +
		"\n    max_sessions: 1"
	return []byte(strings.Replace(validServer,
		"    tunnel_ip: \"10.9.0.2\"\n    max_sessions: 1",
		"    tunnel_ip: \"10.9.0.2\"\n    max_sessions: 1"+extra, 1))
}

// CONF-27: duplicate peer tunnel IPs.
func TestServerDuplicatePeerIP(t *testing.T) {
	if _, err := config.LoadServer(twoPeerServer("phone", "10.9.0.2")); err == nil {
		t.Fatal("duplicate peer tunnel_ip accepted")
	}
}

// CONF-28: duplicate peer names.
func TestServerDuplicatePeerName(t *testing.T) {
	if _, err := config.LoadServer(twoPeerServer("laptop", "10.9.0.3")); err == nil {
		t.Fatal("duplicate peer name accepted")
	}
}

// Two distinct peers load fine.
func TestServerTwoDistinctPeers(t *testing.T) {
	if _, err := config.LoadServer(twoPeerServer("phone", "10.9.0.3")); err != nil {
		t.Fatalf("two distinct peers rejected: %v", err)
	}
}

func TestServerValidExampleLoads(t *testing.T) {
	if _, err := config.LoadServer([]byte(validServer)); err != nil {
		t.Fatalf("valid server example rejected: %v", err)
	}
}
