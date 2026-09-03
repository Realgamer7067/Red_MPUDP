package config

import (
	"net/netip"
)

// Server is the fully parsed server configuration (design §14.2).
type Server struct {
	Listen   netip.AddrPort `yaml:"listen"`
	Identity struct {
		PrivateKeyFile string `yaml:"private_key_file"`
	} `yaml:"identity"`

	Peers []Peer `yaml:"peers"`

	TUN struct {
		Name    string       `yaml:"name"`
		Address netip.Prefix `yaml:"address"`
		Subnet  netip.Prefix `yaml:"subnet"`
		MTU     int          `yaml:"mtu"`
	} `yaml:"tun"`

	Network struct {
		ManageNAT    bool   `yaml:"manage_nat"`
		WANInterface string `yaml:"wan_interface"`
	} `yaml:"network"`

	Limits struct {
		MaxPathsPerSession   int      `yaml:"max_paths_per_session"`
		MaxSessions          int      `yaml:"max_sessions"`
		MaxPendingHandshakes int      `yaml:"max_pending_handshakes"`
		MaxPendingPerSource  int      `yaml:"max_pending_per_source"`
		SessionIdleTimeout   Duration `yaml:"session_idle_timeout"`
		RetryMode            string   `yaml:"retry_mode"` // always | auto | never (never is test-only)
	} `yaml:"limits"`

	Health     HealthConfig     `yaml:"health"`
	Congestion CongestionConfig `yaml:"congestion"`

	MetricsAddr        netip.AddrPort `yaml:"metrics_addr"`
	MetricsAllowPublic bool           `yaml:"metrics_allow_public"`
	LogLevel           string         `yaml:"log_level"`
}

// Peer is one authorized client (design §14.2).
type Peer struct {
	Name          string     `yaml:"name"`
	PublicKeyFile string     `yaml:"public_key_file"`
	PSKFile       string     `yaml:"psk_file"`
	TunnelIP      netip.Addr `yaml:"tunnel_ip"`
	MaxSessions   int        `yaml:"max_sessions"`
}

func (s *Server) applyDefaults() {
	if s.TUN.Name == "" {
		s.TUN.Name = "red0"
	}
	if s.TUN.MTU == 0 {
		s.TUN.MTU = 1180
	}
	if s.Limits.MaxPathsPerSession == 0 {
		s.Limits.MaxPathsPerSession = 4
	}
	if s.Limits.MaxSessions == 0 {
		s.Limits.MaxSessions = 64
	}
	if s.Limits.MaxPendingHandshakes == 0 {
		s.Limits.MaxPendingHandshakes = 256
	}
	if s.Limits.MaxPendingPerSource == 0 {
		s.Limits.MaxPendingPerSource = 8
	}
	if s.Limits.SessionIdleTimeout == 0 {
		s.Limits.SessionIdleTimeout = Duration(120_000_000_000) // 120s
	}
	if s.Limits.RetryMode == "" {
		s.Limits.RetryMode = "always"
	}
	applyHealthDefaults(&s.Health)
	applyCongestionDefaults(&s.Congestion)
	if s.LogLevel == "" {
		s.LogLevel = "info"
	}
}

// Validate performs all server-side checks (CONF-09..30). It never touches the
// filesystem or the network.
func (s *Server) Validate() error {
	e := &errList{}

	checkListenAddr("listen", s.Listen, e)

	if s.Identity.PrivateKeyFile == "" {
		e.addf("identity.private_key_file is required")
	}

	checkIfaceName(s.TUN.Name, e)
	checkTunMTU("tun.mtu", s.TUN.MTU, e)

	if !s.TUN.Subnet.IsValid() || !s.TUN.Subnet.Addr().Is4() {
		e.addf("tun.subnet: must be a valid IPv4 CIDR")
	}
	if !s.TUN.Address.IsValid() || !s.TUN.Address.Addr().Is4() {
		e.addf("tun.address: must be a valid IPv4 CIDR")
	}
	if s.TUN.Subnet.IsValid() && s.TUN.Address.IsValid() {
		if !s.TUN.Subnet.Contains(s.TUN.Address.Addr()) {
			e.addf("tun.address %s is not inside tun.subnet %s", s.TUN.Address, s.TUN.Subnet)
		}
		if s.TUN.Address.Bits() != s.TUN.Subnet.Bits() {
			e.addf("tun.address prefix length /%d must match tun.subnet /%d", s.TUN.Address.Bits(), s.TUN.Subnet.Bits())
		}
	}

	if s.Limits.MaxPathsPerSession < minPaths || s.Limits.MaxPathsPerSession > maxPaths {
		e.addf("limits.max_paths_per_session: %d outside [%d, %d]", s.Limits.MaxPathsPerSession, minPaths, maxPaths)
	}
	for _, f := range []struct {
		name string
		v    int
	}{
		{"limits.max_sessions", s.Limits.MaxSessions},
		{"limits.max_pending_handshakes", s.Limits.MaxPendingHandshakes},
		{"limits.max_pending_per_source", s.Limits.MaxPendingPerSource},
	} {
		if f.v < 1 {
			e.addf("%s: must be at least 1", f.name)
		}
	}
	if s.Limits.MaxPendingPerSource > s.Limits.MaxPendingHandshakes {
		e.addf("limits.max_pending_per_source (%d) exceeds limits.max_pending_handshakes (%d)", s.Limits.MaxPendingPerSource, s.Limits.MaxPendingHandshakes)
	}
	requireDur("limits.session_idle_timeout", s.Limits.SessionIdleTimeout, e)
	switch s.Limits.RetryMode {
	case "always", "auto", "never":
	default:
		e.addf("limits.retry_mode: %q must be always, auto, or never", s.Limits.RetryMode)
	}

	if s.Network.ManageNAT && s.Network.WANInterface == "" {
		e.addf("network.wan_interface is required when network.manage_nat is true")
	}
	if s.Network.WANInterface != "" {
		checkIfaceName(s.Network.WANInterface, e)
	}

	validatePeers(s.Peers, s.TUN.Subnet, e)

	validateHealth(&s.Health, e)
	validateCongestion(&s.Congestion, e)

	checkMetricsAddr("metrics_addr", s.MetricsAddr, s.MetricsAllowPublic, e)

	return e.err()
}

func validatePeers(peers []Peer, subnet netip.Prefix, e *errList) {
	if len(peers) == 0 {
		e.addf("peers: at least one peer is required")
		return
	}
	seenName := map[string]bool{}
	seenIP := map[netip.Addr]bool{}
	for i := range peers {
		p := &peers[i]
		if p.Name == "" {
			e.addf("peers[%d]: name is required", i)
		} else if seenName[p.Name] {
			e.addf("peers: duplicate name %q", p.Name)
		}
		seenName[p.Name] = true

		if p.PublicKeyFile == "" {
			e.addf("peers[%s]: public_key_file is required", p.Name)
		}
		if p.PSKFile == "" {
			e.addf("peers[%s]: psk_file is required", p.Name)
		}

		if !p.TunnelIP.IsValid() || !p.TunnelIP.Is4() {
			e.addf("peers[%s]: tunnel_ip must be a valid IPv4 address", p.Name)
		} else {
			if subnet.IsValid() && !subnet.Contains(p.TunnelIP) {
				e.addf("peers[%s]: tunnel_ip %s is outside tun.subnet %s", p.Name, p.TunnelIP, subnet)
			}
			if seenIP[p.TunnelIP] {
				e.addf("peers: duplicate tunnel_ip %s", p.TunnelIP)
			}
			seenIP[p.TunnelIP] = true
		}

		if p.MaxSessions < 1 {
			e.addf("peers[%s]: max_sessions must be at least 1", p.Name)
		}
	}
}
