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

// ErrResolverUnsupported is returned by a Host whose resolver-restore path is
// not yet implemented (the LinuxHost resolver manager lands in M20). Recover
// treats it as a reported gap, not a phase-1 failure: an unrestored resolver
// setting does not leak traffic, so it must not hold the kill switch closed.
var ErrResolverUnsupported = errors.New("journal: resolver restore not supported by this host")

// Report summarizes what a Recover call did.
type Report struct {
	SysctlsRestored  []string
	SysctlConflicts  []string // key -> operator changed it; left as-is
	RoutesRemoved    int
	RulesRemoved     int
	TablesRemoved    int
	NFTablesRemoved  int
	ResolverRestored bool

	// ResolverDeferred is true when the journal recorded resolver state but the
	// host cannot restore it yet (ErrResolverUnsupported). The operator must
	// restore resolver configuration manually or re-run once the manager lands.
	ResolverDeferred bool

	// KillSwitchRetained is true when an earlier phase failed and recovery
	// deliberately left the fail-closed nftables table (and owned route
	// tables) in place rather than opening traffic on a half-torn-down host
	// (design §11.5).
	KillSwitchRetained bool
}

// Recover undoes exactly the mutations named in j, in the shutdown-safe order
// of design §11.5, split into two phases with a hard gate between them:
//
//	Phase 1 — stop routing traffic through the tunnel and restore host state:
//	  owned routes, then owned rules, then resolver, then sysctls.
//	Gate    — if any Phase 1 step failed, STOP. The owned route tables and the
//	          fail-closed nftables kill-switch table are retained so the host
//	          stays fail-closed instead of leaking traffic while half
//	          recovered. Report.KillSwitchRetained is set and an error returned.
//	Phase 2 — only when Phase 1 was clean: flush owned route tables, then
//	          delete the nftables kill-switch table last.
//
// Recovery is idempotent: a second call with the same journal is a no-op
// success. role and instanceID identify the caller and are checked against the
// journal before any change is made (JOURNAL-11).
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

	var phase1 []error

	// Phase 1a: owned routes, then rules. A failure here is not itself
	// dangerous (the tunnel route staying in place keeps traffic captured),
	// but it blocks Phase 2.
	for _, r := range j.Routes {
		if err := h.DeleteRoute(r); err != nil {
			phase1 = append(phase1, fmt.Errorf("delete route %+v: %w", r, err))
			continue
		}
		rep.RoutesRemoved++
	}
	for _, r := range j.Rules {
		if err := h.DeleteRule(r); err != nil {
			phase1 = append(phase1, fmt.Errorf("delete rule prio %d: %w", r.Priority, err))
			continue
		}
		rep.RulesRemoved++
	}

	// Phase 1b: resolver state. A host that cannot restore it yet
	// (ErrResolverUnsupported) is a reported gap, not a phase-1 failure — an
	// unrestored resolver does not leak traffic.
	if j.Resolver != nil {
		switch err := h.RestoreResolver(*j.Resolver); {
		case err == nil:
			rep.ResolverRestored = true
		case errors.Is(err, ErrResolverUnsupported):
			rep.ResolverDeferred = true
			logf("resolver state recorded but not restored: %v", err)
		default:
			phase1 = append(phase1, fmt.Errorf("restore resolver: %w", err))
		}
	}

	// Phase 1c: sysctls. Restore only if the live value is still the one we
	// installed (JOURNAL-15); otherwise preserve the operator's value and
	// report it (JOURNAL-16). A read error is a Phase 1 failure; a preserved
	// conflict is not.
	for _, s := range j.Sysctls {
		cur, err := h.GetSysctl(s.Name)
		if err != nil {
			phase1 = append(phase1, fmt.Errorf("read sysctl %s: %w", s.Name, err))
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
			phase1 = append(phase1, fmt.Errorf("restore sysctl %s: %w", s.Name, err))
			continue
		}
		rep.SysctlsRestored = append(rep.SysctlsRestored, s.Name)
	}

	// Gate: do not open traffic on a half-recovered host.
	if len(phase1) > 0 {
		rep.KillSwitchRetained = len(j.NFTables) > 0
		logf("recovery stopped after a phase-1 failure; retaining %d route table(s) and %d nftables kill-switch table(s) fail-closed",
			len(j.Tables), len(j.NFTables))
		return rep, fmt.Errorf("journal: recovery halted before removing the kill switch: %w", errors.Join(phase1...))
	}

	// Phase 2a: flush owned route tables. If any fails, the kill switch stays
	// in place fail-closed — the tunnel route tables are still partly present,
	// so opening traffic now could route it wrong.
	var tableErrs []error
	for _, id := range j.Tables {
		if err := h.DeleteRouteTable(id); err != nil {
			tableErrs = append(tableErrs, fmt.Errorf("delete route table %d: %w", id, err))
			continue
		}
		rep.TablesRemoved++
	}
	if len(tableErrs) > 0 {
		rep.KillSwitchRetained = len(j.NFTables) > 0
		logf("route-table flush failed; retaining %d nftables kill-switch table(s) fail-closed", len(j.NFTables))
		return rep, fmt.Errorf("journal: recovery halted before removing the kill switch: %w", errors.Join(tableErrs...))
	}

	// Phase 2b: remove the fail-closed nftables kill-switch table last.
	var nftErrs []error
	for _, t := range j.NFTables {
		if err := h.DeleteNFTable(t); err != nil {
			nftErrs = append(nftErrs, fmt.Errorf("delete nftables table %s/%s: %w", t.Family, t.Name, err))
			continue
		}
		rep.NFTablesRemoved++
	}
	if len(nftErrs) > 0 {
		rep.KillSwitchRetained = rep.NFTablesRemoved < len(j.NFTables)
		return rep, errors.Join(nftErrs...)
	}
	return rep, nil
}
