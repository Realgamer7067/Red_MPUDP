package config

import (
	"net/netip"
)

// Client is the fully parsed client configuration (design §14.1).
type Client struct {
	Server   netip.AddrPort `yaml:"server"`
	Identity ClientIdentity `yaml:"identity"`

	Interfaces []Interface `yaml:"interfaces"`

	TUN struct {
		Name string `yaml:"name"`
		MTU  int    `yaml:"mtu"`
	} `yaml:"tun"`

	Routing struct {
		Manage           bool         `yaml:"manage"`
		FullTunnel       bool         `yaml:"full_tunnel"`
		TunnelTable      RouteTable   `yaml:"tunnel_table"`
		RulePriorityBase RulePriority `yaml:"rule_priority_base"`
		AllowLAN         bool         `yaml:"allow_lan"`
		KillSwitch       bool         `yaml:"kill_switch"`
		IPv6Policy       string       `yaml:"ipv6_policy"` // block | passthrough
	} `yaml:"routing"`

	DNS struct {
		Manage  bool         `yaml:"manage"`
		Servers []netip.Addr `yaml:"servers"`
		Strict  bool         `yaml:"strict"`
	} `yaml:"dns"`

	Scheduler struct {
		Mode                      string   `yaml:"mode"`
		MaxPaths                  int      `yaml:"max_paths"`
		LatencyPacketMaxBytes     int      `yaml:"latency_packet_max_bytes"`
		LatencyReplicaBudgetMbps  int      `yaml:"latency_replica_budget_mbps"`
		StandardReplicaBudgetMbps int      `yaml:"standard_replica_budget_mbps"`
		ReplicaQueueBudget        Duration `yaml:"replica_queue_budget"`
		PrimaryQueueDeadline      Duration `yaml:"primary_queue_deadline"`
	} `yaml:"scheduler"`

	Health     HealthConfig     `yaml:"health"`
	Congestion CongestionConfig `yaml:"congestion"`

	Session struct {
		RekeyAfter        Duration `yaml:"rekey_after"`
		RekeyAfterPackets uint64   `yaml:"rekey_after_packets"`
		DedupWindow       int      `yaml:"dedup_window_packets"`
	} `yaml:"session"`

	MetricsAddr        netip.AddrPort `yaml:"metrics_addr"`
	MetricsAllowPublic bool           `yaml:"metrics_allow_public"`
	LogLevel           string         `yaml:"log_level"`
}

// ClientIdentity holds file references to secret and pinned key material.
type ClientIdentity struct {
	PrivateKeyFile      string `yaml:"private_key_file"`
	ServerPublicKeyFile string `yaml:"server_public_key_file"`
	PSKFile             string `yaml:"psk_file"`
}

// Interface is one physical uplink the client can send a path over.
type Interface struct {
	Name              string     `yaml:"name"`
	FWMark            FWMark     `yaml:"fwmark"`
	RoutingTable      RouteTable `yaml:"routing_table"`
	Gateway           string     `yaml:"gateway"` // "auto" or a literal IP
	MaxPacingRateMbps int        `yaml:"max_pacing_rate_mbps"`
}

// HealthConfig is shared by client and server (design §14).
type HealthConfig struct {
	ProbeInterval         Duration `yaml:"probe_interval"`
	DeadAfterMissedProbes int      `yaml:"dead_after_missed_probes"`
}

// CongestionConfig is shared by client and server (design §14).
type CongestionConfig struct {
	InitialPacingRateMbps int      `yaml:"initial_pacing_rate_mbps"`
	MinPacingRateKbps     int      `yaml:"min_pacing_rate_kbps"`
	MaxPacingRateMbps     int      `yaml:"max_pacing_rate_mbps"`
	QueueDelayTarget      Duration `yaml:"queue_delay_target"`
}

func (c *Client) applyDefaults() {
	if c.TUN.Name == "" {
		c.TUN.Name = "red0"
	}
	if c.TUN.MTU == 0 {
		c.TUN.MTU = 1180
	}
	if c.Routing.IPv6Policy == "" {
		c.Routing.IPv6Policy = "block"
	}
	if c.Routing.TunnelTable == 0 {
		c.Routing.TunnelTable = 200
	}
	if c.Routing.RulePriorityBase == 0 {
		c.Routing.RulePriorityBase = 10000
	}
	if c.Scheduler.Mode == "" {
		c.Scheduler.Mode = "adaptive-redundant"
	}
	if c.Scheduler.MaxPaths == 0 {
		c.Scheduler.MaxPaths = 4
	}
	if c.Scheduler.LatencyPacketMaxBytes == 0 {
		c.Scheduler.LatencyPacketMaxBytes = 768
	}
	if c.Scheduler.LatencyReplicaBudgetMbps == 0 {
		c.Scheduler.LatencyReplicaBudgetMbps = 5
	}
	if c.Scheduler.StandardReplicaBudgetMbps == 0 {
		c.Scheduler.StandardReplicaBudgetMbps = 10
	}
	if c.Scheduler.ReplicaQueueBudget == 0 {
		c.Scheduler.ReplicaQueueBudget = Duration(5_000_000) // 5ms
	}
	if c.Scheduler.PrimaryQueueDeadline == 0 {
		c.Scheduler.PrimaryQueueDeadline = Duration(50_000_000) // 50ms
	}
	applyHealthDefaults(&c.Health)
	applyCongestionDefaults(&c.Congestion)
	if c.Session.RekeyAfter == 0 {
		c.Session.RekeyAfter = Duration(3_600_000_000_000) // 1h
	}
	if c.Session.RekeyAfterPackets == 0 {
		c.Session.RekeyAfterPackets = 1 << 32
	}
	if c.Session.DedupWindow == 0 {
		c.Session.DedupWindow = 65536
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
}

func applyHealthDefaults(h *HealthConfig) {
	if h.ProbeInterval == 0 {
		h.ProbeInterval = Duration(250_000_000) // 250ms
	}
	if h.DeadAfterMissedProbes == 0 {
		h.DeadAfterMissedProbes = 3
	}
}

func applyCongestionDefaults(c *CongestionConfig) {
	if c.InitialPacingRateMbps == 0 {
		c.InitialPacingRateMbps = 1
	}
	if c.MinPacingRateKbps == 0 {
		c.MinPacingRateKbps = 128
	}
	if c.MaxPacingRateMbps == 0 {
		c.MaxPacingRateMbps = 100
	}
	if c.QueueDelayTarget == 0 {
		c.QueueDelayTarget = Duration(15_000_000) // 15ms
	}
}

// Validate performs all client-side checks (CONF-08..30). It never touches the
// filesystem or the network.
func (c *Client) Validate() error {
	e := &errList{}

	checkServerAddr("server", c.Server, e)

	if c.Identity.PrivateKeyFile == "" {
		e.addf("identity.private_key_file is required")
	}
	if c.Identity.ServerPublicKeyFile == "" {
		e.addf("identity.server_public_key_file is required")
	}
	if c.Identity.PSKFile == "" {
		e.addf("identity.psk_file is required")
	}

	validateInterfaces(c.Interfaces, c.Routing.TunnelTable, e)

	checkIfaceName(c.TUN.Name, e)
	checkTunMTU("tun.mtu", c.TUN.MTU, e)

	switch c.Routing.IPv6Policy {
	case "block", "passthrough":
	default:
		e.addf("routing.ipv6_policy: %q must be \"block\" or \"passthrough\"", c.Routing.IPv6Policy)
	}
	if c.Routing.TunnelTable.reserved() {
		e.addf("routing.tunnel_table: %d is reserved", c.Routing.TunnelTable)
	}
	base := c.Routing.RulePriorityBase
	if base < 1 || base+1000 > rulePriorityCeiling {
		e.addf("routing.rule_priority_base: %d must be in [1, %d]", base, rulePriorityCeiling-1000)
	}

	if c.Scheduler.MaxPaths < minPaths || c.Scheduler.MaxPaths > maxPaths {
		e.addf("scheduler.max_paths: %d outside [%d, %d]", c.Scheduler.MaxPaths, minPaths, maxPaths)
	}
	if c.Scheduler.LatencyPacketMaxBytes <= 0 {
		e.addf("scheduler.latency_packet_max_bytes: must be positive")
	}
	requireDur("scheduler.replica_queue_budget", c.Scheduler.ReplicaQueueBudget, e)
	requireDur("scheduler.primary_queue_deadline", c.Scheduler.PrimaryQueueDeadline, e)

	validateHealth(&c.Health, e)
	validateCongestion(&c.Congestion, e)

	checkDedupWindow(c.Session.DedupWindow, e)
	requireDur("session.rekey_after", c.Session.RekeyAfter, e)
	if c.Session.RekeyAfterPackets == 0 {
		e.addf("session.rekey_after_packets: must be positive")
	}

	for i, s := range c.DNS.Servers {
		if !s.IsValid() {
			e.addf("dns.servers[%d]: invalid address", i)
		}
	}

	checkMetricsAddr("metrics_addr", c.MetricsAddr, c.MetricsAllowPublic, e)

	return e.err()
}

func validateInterfaces(ifaces []Interface, tunnelTable RouteTable, e *errList) {
	if len(ifaces) == 0 {
		e.addf("interfaces: at least one uplink is required")
		return
	}
	if len(ifaces) > maxPaths {
		e.addf("interfaces: %d configured, at most %d are usable", len(ifaces), maxPaths)
	}
	seenName := map[string]bool{}
	seenMark := map[FWMark]bool{}
	seenTable := map[RouteTable]bool{}
	for i := range ifaces {
		in := &ifaces[i]
		checkIfaceName(in.Name, e)
		if seenName[in.Name] {
			e.addf("interfaces: duplicate name %q", in.Name)
		}
		seenName[in.Name] = true

		if in.FWMark == 0 {
			e.addf("interfaces[%s]: fwmark must be non-zero", in.Name)
		} else if seenMark[in.FWMark] {
			e.addf("interfaces: duplicate fwmark %s", in.FWMark)
		}
		seenMark[in.FWMark] = true

		if in.RoutingTable.reserved() {
			e.addf("interfaces[%s]: routing_table %d is reserved", in.Name, in.RoutingTable)
		}
		if in.RoutingTable == tunnelTable {
			e.addf("interfaces[%s]: routing_table %d collides with routing.tunnel_table", in.Name, in.RoutingTable)
		}
		if seenTable[in.RoutingTable] {
			e.addf("interfaces: duplicate routing_table %d", in.RoutingTable)
		}
		seenTable[in.RoutingTable] = true

		if in.Gateway != "" && in.Gateway != "auto" {
			if _, err := netip.ParseAddr(in.Gateway); err != nil {
				e.addf("interfaces[%s]: gateway %q is not \"auto\" or a literal IP", in.Name, in.Gateway)
			}
		}
		if in.MaxPacingRateMbps <= 0 {
			e.addf("interfaces[%s]: max_pacing_rate_mbps must be positive", in.Name)
		}
	}
}

func validateHealth(h *HealthConfig, e *errList) {
	checkProbeInterval("health.probe_interval", h.ProbeInterval, e)
	if h.DeadAfterMissedProbes < 1 {
		e.addf("health.dead_after_missed_probes: must be at least 1")
	}
}

func validateCongestion(c *CongestionConfig, e *errList) {
	if c.InitialPacingRateMbps <= 0 || c.MinPacingRateKbps <= 0 || c.MaxPacingRateMbps <= 0 {
		e.addf("congestion: all pacing rates must be positive")
		return
	}
	minMbps := float64(c.MinPacingRateKbps) / 1000
	if minMbps > float64(c.InitialPacingRateMbps) {
		e.addf("congestion: min_pacing_rate_kbps (%d) exceeds initial_pacing_rate_mbps (%d)", c.MinPacingRateKbps, c.InitialPacingRateMbps)
	}
	if c.InitialPacingRateMbps > c.MaxPacingRateMbps {
		e.addf("congestion: initial_pacing_rate_mbps (%d) exceeds max_pacing_rate_mbps (%d)", c.InitialPacingRateMbps, c.MaxPacingRateMbps)
	}
	requireDur("congestion.queue_delay_target", c.QueueDelayTarget, e)
}
