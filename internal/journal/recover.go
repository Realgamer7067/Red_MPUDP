package journal

import (
	"errors"
	"fmt"
)

// Host is the set of privileged operations recovery needs. The production
// implementation shells out to ip/sysctl/nft (see host_linux.go); tests use a
// fake. Every Delete* call must be idempotent: removing an object that is
// already gone is a success, not an error (JOURNAL-19, JOURNAL-20).
type Host interface {
	GetSysctl(key string) (string, error)
	SetSysctl(key, value string) error
	DeleteRoute(r RouteRecord) error
	DeleteRule(r RuleRecord) error
	DeleteRouteTable(id uint32) error
	DeleteNFTable(t NFTableRecord) error
	RestoreResolver(r ResolverRecord) error
}

// Logf receives human-readable progress and conflict notices.
type Logf func(format string, args ...any)

// Report summarizes what a Recover call did.
type Report struct {
	SysctlsRestored  []string
	SysctlConflicts  []string // key -> operator changed it; left as-is
	RoutesRemoved    int
	RulesRemoved     int
	TablesRemoved    int
	NFTablesRemoved  int
	ResolverRestored bool
}

// Recover undoes exactly the mutations named in j, in shutdown-safe order
// (design §11.5): tunnel routes and rules first, resolver next, owned tables
// and nftables last. It is idempotent — a second call with the same journal is
// a no-op success.
//
// role and instanceID identify the caller; a mismatch is refused before any
// change is made (JOURNAL-11).
func Recover(j *Journal, role Role, instanceID string, h Host, logf Logf) (Report, error) {
	var rep Report
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := j.Validate(); err != nil {
		return rep, err
	}
	if err := j.checkOwner(role, instanceID); err != nil {
		return rep, err
	}

	var errs []error

	// 1. Owned routes, then rules: stop steering traffic through the tunnel.
	for _, r := range j.Routes {
		if err := h.DeleteRoute(r); err != nil {
			errs = append(errs, fmt.Errorf("delete route %+v: %w", r, err))
			continue
		}
		rep.RoutesRemoved++
	}
	for _, r := range j.Rules {
		if err := h.DeleteRule(r); err != nil {
			errs = append(errs, fmt.Errorf("delete rule prio %d: %w", r.Priority, err))
			continue
		}
		rep.RulesRemoved++
	}

	// 2. Resolver state.
	if j.Resolver != nil {
		if err := h.RestoreResolver(*j.Resolver); err != nil {
			errs = append(errs, fmt.Errorf("restore resolver: %w", err))
		} else {
			rep.ResolverRestored = true
		}
	}

	// 3. Sysctls: restore only if the live value is still the one we installed
	// (JOURNAL-15); otherwise preserve the operator's value and report it
	// (JOURNAL-16).
	for _, s := range j.Sysctls {
		cur, err := h.GetSysctl(s.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("read sysctl %s: %w", s.Name, err))
			continue
		}
		if cur == s.Prior {
			continue // already restored; idempotent
		}
		if cur != s.Installed {
			rep.SysctlConflicts = append(rep.SysctlConflicts, s.Name)
			logf("sysctl %s = %q was changed after RED_MPUDP set it to %q; preserving operator value", s.Name, cur, s.Installed)
			continue
		}
		if err := h.SetSysctl(s.Name, s.Prior); err != nil {
			errs = append(errs, fmt.Errorf("restore sysctl %s: %w", s.Name, err))
			continue
		}
		rep.SysctlsRestored = append(rep.SysctlsRestored, s.Name)
	}

	// 4. Owned route tables and the fail-closed nftables table last.
	for _, id := range j.Tables {
		if err := h.DeleteRouteTable(id); err != nil {
			errs = append(errs, fmt.Errorf("delete route table %d: %w", id, err))
			continue
		}
		rep.TablesRemoved++
	}
	for _, t := range j.NFTables {
		if err := h.DeleteNFTable(t); err != nil {
			errs = append(errs, fmt.Errorf("delete nftables table %s/%s: %w", t.Family, t.Name, err))
			continue
		}
		rep.NFTablesRemoved++
	}

	if len(errs) > 0 {
		return rep, errors.Join(errs...)
	}
	return rep, nil
}
