// Package journal implements the RED_MPUDP mutation journal (design §11.5): an
// atomic, mode-0600 record written under /run/red-mpudp/ before the first
// host-network mutation. It names exactly the routes, rules, route tables,
// nftables tables, sysctls, and resolver state this instance owns, so a later
// process — the daemon on restart, or `red-mpudp cleanup --state-file` — can
// undo precisely those changes and nothing else.
//
// The journal contains no cryptographic secrets: no private keys, pre-shared
// keys, join tokens, or packet contents. TestSchemaHasNoSecretFields enforces
// this by reflection.
package journal

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SchemaVersion is the on-disk format version. A journal with any other version
// is refused during recovery (JOURNAL-09) rather than acted on.
const SchemaVersion = 1

// Role is the process role that owns a journal.
type Role string

const (
	RoleClient Role = "client"
	RoleServer Role = "server"
)

// Journal is the complete owned-resource record for one instance.
type Journal struct {
	Schema     int       `json:"schema"`
	Role       Role      `json:"role"`
	InstanceID string    `json:"instance_id"`
	CreatedAt  time.Time `json:"created_at"`

	// Owned networking objects. Each record carries the exact identifiers
	// needed to remove that object and only that object (JOURNAL-14).
	Routes   []RouteRecord   `json:"routes,omitempty"`
	Rules    []RuleRecord    `json:"rules,omitempty"`
	Tables   []uint32        `json:"route_tables,omitempty"`
	NFTables []NFTableRecord `json:"nftables,omitempty"`
	Sysctls  []SysctlRecord  `json:"sysctls,omitempty"`
	Resolver *ResolverRecord `json:"resolver,omitempty"`
}

// RouteRecord identifies a single installed route.
type RouteRecord struct {
	Table    uint32 `json:"table"`
	Dst      string `json:"dst"` // CIDR or "default"
	Dev      string `json:"dev"`
	Via      string `json:"via,omitempty"`
	Priority uint32 `json:"priority,omitempty"`
}

// RuleRecord identifies a single installed policy-routing rule.
type RuleRecord struct {
	Family   string `json:"family"` // "ip" or "ip6"
	Priority uint32 `json:"priority"`
	FWMark   uint32 `json:"fwmark,omitempty"`
	Table    uint32 `json:"table"`
}

// NFTableRecord identifies one nftables table owned by the instance.
type NFTableRecord struct {
	Family string `json:"family"` // "inet", "ip", "ip6"
	Name   string `json:"name"`
}

// SysctlRecord records a sysctl this instance changed: the value found before
// the change (Prior) and the value written (Installed). Recovery restores Prior
// only if the live value still equals Installed (JOURNAL-15, JOURNAL-16).
type SysctlRecord struct {
	Name      string `json:"name"` // dotted sysctl name, e.g. net.ipv4.ip_forward
	Prior     string `json:"prior"`
	Installed string `json:"installed"`
}

// ResolverRecord is an opaque snapshot of resolver state taken before the
// instance changed it (JOURNAL-13). Manager identifies how to restore it;
// Prior is a manager-specific blob with no secret content.
type ResolverRecord struct {
	Manager string `json:"manager"` // "resolved-link", "resolv-conf"
	Link    string `json:"link,omitempty"`
	Prior   string `json:"prior"`
}

var (
	instanceIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,128}$`)
	sysctlNameRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)
	devNameRe    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,15}$`)
	nftNameRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Validate checks structural invariants: schema version (JOURNAL-09), role, and
// well-formed resource identifiers (JOURNAL-10).
func (j *Journal) Validate() error {
	if j.Schema != SchemaVersion {
		return fmt.Errorf("journal: unsupported schema version %d (want %d)", j.Schema, SchemaVersion)
	}
	if j.Role != RoleClient && j.Role != RoleServer {
		return fmt.Errorf("journal: invalid role %q", j.Role)
	}
	if !instanceIDRe.MatchString(j.InstanceID) {
		return fmt.Errorf("journal: malformed instance_id")
	}
	for i, r := range j.Routes {
		if r.Dst != "default" && !strings.Contains(r.Dst, "/") {
			return fmt.Errorf("journal: routes[%d]: dst %q must be a CIDR or \"default\"", i, r.Dst)
		}
		if !devNameRe.MatchString(r.Dev) {
			return fmt.Errorf("journal: routes[%d]: malformed dev %q", i, r.Dev)
		}
		if r.Table == 0 {
			return fmt.Errorf("journal: routes[%d]: table must be non-zero", i)
		}
	}
	for i, r := range j.Rules {
		if r.Family != "ip" && r.Family != "ip6" {
			return fmt.Errorf("journal: rules[%d]: family %q must be ip or ip6", i, r.Family)
		}
		if r.Table == 0 {
			return fmt.Errorf("journal: rules[%d]: table must be non-zero", i)
		}
	}
	for i, t := range j.Tables {
		if t == 0 {
			return fmt.Errorf("journal: route_tables[%d]: must be non-zero", i)
		}
	}
	for i, n := range j.NFTables {
		switch n.Family {
		case "inet", "ip", "ip6":
		default:
			return fmt.Errorf("journal: nftables[%d]: family %q invalid", i, n.Family)
		}
		if !nftNameRe.MatchString(n.Name) {
			return fmt.Errorf("journal: nftables[%d]: malformed name %q", i, n.Name)
		}
	}
	for i, s := range j.Sysctls {
		if !sysctlNameRe.MatchString(s.Name) {
			return fmt.Errorf("journal: sysctls[%d]: malformed name %q", i, s.Name)
		}
	}
	return nil
}

// checkOwner enforces JOURNAL-11: recovery must run against the same role and
// instance the journal was written for.
func (j *Journal) checkOwner(role Role, instanceID string) error {
	if j.Role != role {
		return fmt.Errorf("journal: role mismatch (journal %q, caller %q)", j.Role, role)
	}
	if instanceID != "" && j.InstanceID != instanceID {
		return fmt.Errorf("journal: instance mismatch")
	}
	return nil
}
