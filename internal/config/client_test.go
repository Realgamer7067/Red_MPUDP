package config_test

import (
	"strings"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/config"
)

// mutClient returns validClient with each old->new replacement applied.
func mutClient(t *testing.T, repl ...string) []byte {
	t.Helper()
	s := validClient
	if len(repl)%2 != 0 {
		t.Fatal("repl must be pairs")
	}
	for i := 0; i < len(repl); i += 2 {
		if !strings.Contains(s, repl[i]) {
			t.Fatalf("fragment %q not found in base client config", repl[i])
		}
		s = strings.Replace(s, repl[i], repl[i+1], 1)
	}
	return []byte(s)
}

func TestClientBoundaries(t *testing.T) {
	tests := []struct {
		name string
		repl []string
	}{
		// CONF-08: non-literal server endpoint.
		{"server hostname", []string{`server: "203.0.113.10:51820"`, `server: "vpn.example.com:51820"`}},
		{"server no port", []string{`server: "203.0.113.10:51820"`, `server: "203.0.113.10"`}},
		{"server zero port", []string{`server: "203.0.113.10:51820"`, `server: "203.0.113.10:0"`}},
		{"server ipv6", []string{`server: "203.0.113.10:51820"`, `server: "[2001:db8::1]:51820"`}},
		// CONF-09: unspecified / multicast / broadcast.
		{"server unspecified", []string{`server: "203.0.113.10:51820"`, `server: "0.0.0.0:51820"`}},
		{"server multicast", []string{`server: "203.0.113.10:51820"`, `server: "224.0.0.1:51820"`}},
		{"server broadcast", []string{`server: "203.0.113.10:51820"`, `server: "255.255.255.255:51820"`}},
		// CONF-12: TUN MTU range.
		{"mtu too low", []string{"  mtu: 1180", "  mtu: 1111"}},
		{"mtu too high", []string{"  mtu: 1180", "  mtu: 1401"}},
		// CONF-13: max paths range.
		{"max_paths negative", []string{"max_paths: 2", "max_paths: -1"}},
		{"max_paths 5", []string{"max_paths: 2", "max_paths: 5"}},
		// CONF-14/15: dedup window.
		{"dedup too small", []string{"dedup_window_packets: 65536", "dedup_window_packets: 2048"}},
		{"dedup too big", []string{"dedup_window_packets: 65536", "dedup_window_packets: 2097152"}},
		{"dedup not pow2", []string{"dedup_window_packets: 65536", "dedup_window_packets: 65535"}},
		// CONF-16: probe interval.
		{"probe too fast", []string{`probe_interval: "250ms"`, `probe_interval: "50ms"`}},
		{"probe too slow", []string{`probe_interval: "250ms"`, `probe_interval: "6s"`}},
		// CONF-17: pacing ordering.
		{"min > initial", []string{"min_pacing_rate_kbps: 128", "min_pacing_rate_kbps: 5000"}},
		{"initial > max", []string{"initial_pacing_rate_mbps: 1", "initial_pacing_rate_mbps: 200"}},
		{"negative initial", []string{"initial_pacing_rate_mbps: 1", "initial_pacing_rate_mbps: -1"}},
		// CONF-19: interface name truncation.
		{"iface name too long", []string{`name: "wlan0"`, `name: "verylonginterfacename0"`}},
		// CONF-20: duplicate interface names.
		{"dup iface name", []string{`name: "wlan1"`, `name: "wlan0"`}},
		// CONF-21: duplicate marks.
		{"dup fwmark", []string{"fwmark: 0x524d0002", "fwmark: 0x524d0001"}},
		// CONF-22: duplicate route tables.
		{"dup routing_table", []string{"routing_table: 202", "routing_table: 201"}},
		// CONF-23: table collides with tunnel table.
		{"iface table == tunnel table", []string{"routing_table: 201", "routing_table: 200"}},
		// CONF-24: rule priority base out of range.
		{"rule base too high", []string{"rule_priority_base: 10000", "rule_priority_base: 32000"}},
		// CONF-29: public metrics listener without opt-in.
		{"public metrics no opt-in", []string{`metrics_addr: "127.0.0.1:9090"`, `metrics_addr: "0.0.0.0:9090"`}},
		// ipv6_policy enum.
		{"bad ipv6_policy", []string{`ipv6_policy: "block"`, `ipv6_policy: "drop"`}},
		// missing identity.
		{"no private key", []string{`  private_key_file: "/etc/red-mpudp/client.key"`, ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := config.LoadClient(mutClient(t, tc.repl...)); err == nil {
				t.Fatalf("%s: expected rejection", tc.name)
			}
		})
	}
}

// CONF-29 opt-in path accepts the public listener.
func TestClientPublicMetricsWithOptIn(t *testing.T) {
	in := mutClient(t,
		`metrics_addr: "127.0.0.1:9090"`,
		"metrics_addr: \"0.0.0.0:9090\"\nmetrics_allow_public: true",
	)
	if _, err := config.LoadClient(in); err != nil {
		t.Fatalf("public metrics with opt-in rejected: %v", err)
	}
}

// CONF-03: a duration without a unit is rejected.
func TestClientDurationRequiresUnit(t *testing.T) {
	if _, err := config.LoadClient(mutClient(t, `probe_interval: "250ms"`, `probe_interval: "250"`)); err == nil {
		t.Fatal("unit-less duration accepted")
	}
}

// CONF-30: loading never touches the filesystem for the referenced key files
// (they do not exist here, yet load succeeds).
func TestClientValidationDoesNotOpenKeyFiles(t *testing.T) {
	if _, err := config.LoadClient([]byte(validClient)); err != nil {
		t.Fatalf("load failed even though referenced key files are absent: %v", err)
	}
}
