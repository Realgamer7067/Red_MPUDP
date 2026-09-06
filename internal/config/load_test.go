package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Realgamer7067/Red_MPUDP/internal/config"
)

const validClient = `
server: "203.0.113.10:51820"

identity:
  private_key_file: "/etc/red-mpudp/client.key"
  server_public_key_file: "/etc/red-mpudp/server.pub"
  psk_file: "/etc/red-mpudp/client.psk"

interfaces:
  - name: "wlan0"
    fwmark: 0x524d0001
    routing_table: 201
    gateway: "auto"
    max_pacing_rate_mbps: 100
  - name: "wlan1"
    fwmark: 0x524d0002
    routing_table: 202
    gateway: "auto"
    max_pacing_rate_mbps: 50

tun:
  name: "red0"
  mtu: 1180

routing:
  manage: true
  full_tunnel: true
  tunnel_table: 200
  rule_priority_base: 10000
  allow_lan: false
  kill_switch: true
  ipv6_policy: "block"

dns:
  manage: true
  servers: ["1.1.1.1", "1.0.0.1"]
  strict: true

scheduler:
  mode: "adaptive-redundant"
  max_paths: 2
  latency_packet_max_bytes: 768
  latency_replica_budget_mbps: 5
  standard_replica_budget_mbps: 10
  replica_queue_budget: "5ms"
  primary_queue_deadline: "50ms"

health:
  probe_interval: "250ms"
  dead_after_missed_probes: 3

congestion:
  initial_pacing_rate_mbps: 1
  min_pacing_rate_kbps: 128
  max_pacing_rate_mbps: 100
  queue_delay_target: "15ms"

session:
  rekey_after: "1h"
  rekey_after_packets: 4294967296
  dedup_window_packets: 65536

metrics_addr: "127.0.0.1:9090"
log_level: "info"
`

const validServer = `
listen: "0.0.0.0:51820"

identity:
  private_key_file: "/etc/red-mpudp/server.key"

peers:
  - name: "laptop"
    public_key_file: "/etc/red-mpudp/peers/laptop.pub"
    psk_file: "/etc/red-mpudp/peers/laptop.psk"
    tunnel_ip: "10.9.0.2"
    max_sessions: 1

tun:
  name: "red0"
  address: "10.9.0.1/24"
  subnet: "10.9.0.0/24"
  mtu: 1180

network:
  manage_nat: true
  wan_interface: "eth0"

limits:
  max_paths_per_session: 4
  max_sessions: 64
  max_pending_handshakes: 256
  max_pending_per_source: 8
  session_idle_timeout: "120s"
  retry_mode: "always"

health:
  probe_interval: "250ms"
  dead_after_missed_probes: 3

congestion:
  initial_pacing_rate_mbps: 1
  min_pacing_rate_kbps: 128
  max_pacing_rate_mbps: 100
  queue_delay_target: "15ms"

metrics_addr: "127.0.0.1:9090"
log_level: "info"
`

func TestValidExamplesLoadDeterministically(t *testing.T) {
	c1, err := config.LoadClient([]byte(validClient))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	c2, _ := config.LoadClient([]byte(validClient))
	if !reflect.DeepEqual(c1, c2) {
		t.Fatal("client load not deterministic")
	}
	if c1.Server.String() != "203.0.113.10:51820" {
		t.Fatalf("server parsed as %v", c1.Server)
	}
	if c1.TUN.MTU != 1180 || len(c1.Interfaces) != 2 {
		t.Fatalf("unexpected client fields: mtu=%d ifaces=%d", c1.TUN.MTU, len(c1.Interfaces))
	}

	s1, err := config.LoadServer([]byte(validServer))
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	s2, _ := config.LoadServer([]byte(validServer))
	if !reflect.DeepEqual(s1, s2) {
		t.Fatal("server load not deterministic")
	}
}

func TestDefaultsApplied(t *testing.T) {
	minimal := `
server: "203.0.113.10:51820"
identity:
  private_key_file: "a"
  server_public_key_file: "b"
  psk_file: "c"
interfaces:
  - name: "wlan0"
    fwmark: 0x1
    routing_table: 201
    max_pacing_rate_mbps: 100
`
	c, err := config.LoadClient([]byte(minimal))
	if err != nil {
		t.Fatalf("minimal client rejected: %v", err)
	}
	if c.TUN.Name != "red0" || c.TUN.MTU != 1180 {
		t.Errorf("tun defaults not applied: %+v", c.TUN)
	}
	if c.Scheduler.MaxPaths != 4 {
		t.Errorf("max_paths default = %d, want 4", c.Scheduler.MaxPaths)
	}
	if c.Health.ProbeInterval.Std().String() != "250ms" {
		t.Errorf("probe_interval default = %v", c.Health.ProbeInterval)
	}
	if c.Session.DedupWindow != 65536 {
		t.Errorf("dedup default = %d", c.Session.DedupWindow)
	}
	if c.LogLevel != "info" {
		t.Errorf("log_level default = %q", c.LogLevel)
	}
}

func TestRejectUnknownField(t *testing.T) {
	in := validClient + "\nbogus_top_level_key: 1\n"
	if _, err := config.LoadClient([]byte(in)); err == nil {
		t.Fatal("unknown top-level field accepted")
	}
	in2 := strings.Replace(validClient, "  mtu: 1180", "  mtu: 1180\n  bogus_nested: true", 1)
	if _, err := config.LoadClient([]byte(in2)); err == nil {
		t.Fatal("unknown nested field accepted")
	}
}

func TestRejectDuplicateKey(t *testing.T) {
	in := validClient + "\nlog_level: \"debug\"\n" // log_level appears twice
	if _, err := config.LoadClient([]byte(in)); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestRejectMultipleDocuments(t *testing.T) {
	in := validClient + "\n---\n" + validClient
	if _, err := config.LoadClient([]byte(in)); err == nil {
		t.Fatal("multi-document YAML accepted")
	}
}

func TestRejectEmpty(t *testing.T) {
	if _, err := config.LoadClient([]byte("   \n")); err == nil {
		t.Fatal("empty config accepted")
	}
}
