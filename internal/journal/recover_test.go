package journal

import (
	"errors"
	"fmt"
	"testing"
)

// fakeHost records every mutating call and simulates live sysctl state.
type fakeHost struct {
	sysctl map[string]string

	routesDeleted []RouteRecord
	rulesDeleted  []RuleRecord
	tablesDeleted []uint32
	nftDeleted    []NFTableRecord
	sysctlSet     map[string]string
	resolver      int

	failDeleteRoute      bool
	failDeleteRouteTable bool
	failDeleteNFTable    bool
	resolverUnsupported  bool
	failResolverHard     bool
}

func newFakeHost() *fakeHost {
	return &fakeHost{sysctl: map[string]string{}, sysctlSet: map[string]string{}}
}

func (h *fakeHost) GetSysctl(k string) (string, error) {
	v, ok := h.sysctl[k]
	if !ok {
		return "", errors.New("no such key")
	}
	return v, nil
}
func (h *fakeHost) SetSysctl(k, v string) error {
	h.sysctl[k] = v
	h.sysctlSet[k] = v
	return nil
}
func (h *fakeHost) DeleteRoute(r RouteRecord) error {
	if h.failDeleteRoute {
		return errors.New("boom")
	}
	h.routesDeleted = append(h.routesDeleted, r)
	return nil
}
func (h *fakeHost) DeleteRule(r RuleRecord) error {
	h.rulesDeleted = append(h.rulesDeleted, r)
	return nil
}
func (h *fakeHost) DeleteRouteTable(id uint32) error {
	if h.failDeleteRouteTable {
		return errors.New("boom")
	}
	h.tablesDeleted = append(h.tablesDeleted, id)
	return nil
}
func (h *fakeHost) DeleteNFTable(t NFTableRecord) error {
	if h.failDeleteNFTable {
		return errors.New("boom")
	}
	h.nftDeleted = append(h.nftDeleted, t)
	return nil
}
func (h *fakeHost) RestoreResolver(ResolverRecord) error {
	if h.resolverUnsupported {
		return fmt.Errorf("%w (test)", ErrResolverUnsupported)
	}
	if h.failResolverHard {
		return errors.New("resolver boom")
	}
	h.resolver++
	return nil
}

func TestRecoverHappyPath(t *testing.T) {
	j := validJournal()
	j.Resolver = &ResolverRecord{Manager: "resolv-conf", Prior: "nameserver 192.0.2.1\n"}

	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1" // still the value we installed

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep.RoutesRemoved != 1 || rep.RulesRemoved != 1 || rep.TablesRemoved != 2 || rep.NFTablesRemoved != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !rep.ResolverRestored || h.resolver != 1 {
		t.Fatal("resolver not restored")
	}
	if h.sysctl["net.ipv4.ip_forward"] != "0" {
		t.Fatalf("sysctl not restored to prior: %q", h.sysctl["net.ipv4.ip_forward"])
	}
	if len(rep.SysctlsRestored) != 1 {
		t.Fatalf("SysctlsRestored = %v", rep.SysctlsRestored)
	}
}

// JOURNAL-16: an operator-modified sysctl is preserved and reported.
func TestRecoverPreservesOperatorSysctl(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "2" // neither Prior("0") nor Installed("1")

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(rep.SysctlConflicts) != 1 || rep.SysctlConflicts[0] != "net.ipv4.ip_forward" {
		t.Fatalf("conflict not reported: %+v", rep)
	}
	if h.sysctl["net.ipv4.ip_forward"] != "2" {
		t.Fatal("operator sysctl value was overwritten")
	}
	if len(h.sysctlSet) != 0 {
		t.Fatalf("SetSysctl called despite conflict: %v", h.sysctlSet)
	}
}

// JOURNAL-19, JOURNAL-20: idempotent; a second run is a no-op success.
func TestRecoverIdempotent(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"

	if _, err := Recover(j, RoleClient, j.InstanceID, h, nil); err != nil {
		t.Fatalf("first Recover: %v", err)
	}
	// sysctl now "0" == Prior; second run must not try to set it again.
	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if len(rep.SysctlsRestored) != 0 || len(rep.SysctlConflicts) != 0 {
		t.Fatalf("second run was not a no-op for sysctls: %+v", rep)
	}
}

// JOURNAL-11: role/instance mismatch is refused before any mutation.
func TestRecoverRefusesMismatch(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	if _, err := Recover(j, RoleServer, "", h, nil); err == nil {
		t.Fatal("role mismatch accepted")
	}
	if len(h.routesDeleted)+len(h.rulesDeleted)+len(h.tablesDeleted) != 0 {
		t.Fatal("mutations happened despite a refused owner check")
	}
}

// The gate: recovery only ever touches resources named in the journal.
func TestRecoverTouchesOnlyOwnedResources(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"

	if _, err := Recover(j, RoleClient, j.InstanceID, h, nil); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(h.routesDeleted) != 1 || h.routesDeleted[0] != j.Routes[0] {
		t.Fatalf("routes touched: %+v, journal: %+v", h.routesDeleted, j.Routes)
	}
	if len(h.tablesDeleted) != 2 {
		t.Fatalf("tables touched: %v", h.tablesDeleted)
	}
	for _, id := range h.tablesDeleted {
		if id != 200 && id != 201 {
			t.Fatalf("recovery deleted unlisted route table %d", id)
		}
	}
	for k := range h.sysctlSet {
		if k != "net.ipv4.ip_forward" {
			t.Fatalf("recovery wrote unlisted sysctl %q", k)
		}
	}
}

// A phase-1 failure aggregates the errors from the rest of phase 1 but stops
// before phase 2, keeping the host fail-closed.
func TestRecoverAggregatesPhase1Errors(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"
	h.failDeleteRoute = true

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err == nil {
		t.Fatal("expected an aggregated error")
	}
	// The rest of phase 1 still ran.
	if rep.RulesRemoved != 1 || len(rep.SysctlsRestored) != 1 {
		t.Fatalf("phase 1 stopped early on a route error: %+v", rep)
	}
}

// Blocker 4: a phase-1 failure must NOT remove the nftables kill-switch table
// or flush owned route tables (design §11.5 fail-closed on unexpected exit).
func TestRecoverRetainsKillSwitchOnPhase1Failure(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"
	h.failDeleteRoute = true

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err == nil {
		t.Fatal("expected recovery to halt")
	}
	if !rep.KillSwitchRetained {
		t.Fatal("KillSwitchRetained not set after a phase-1 failure")
	}
	if len(h.nftDeleted) != 0 {
		t.Fatalf("nftables kill-switch table deleted despite a phase-1 failure: %+v", h.nftDeleted)
	}
	if len(h.tablesDeleted) != 0 {
		t.Fatalf("owned route tables flushed despite a phase-1 failure: %v", h.tablesDeleted)
	}
	if rep.NFTablesRemoved != 0 || rep.TablesRemoved != 0 {
		t.Fatalf("phase 2 ran despite a phase-1 failure: %+v", rep)
	}
}

// Final review: a resolver restore that cannot run yet (ErrResolverUnsupported)
// is a phase-1 failure — design §11.5 restores the resolver before removing
// routes or the kill switch, so an unrestored resolver must not let the kill
// switch come down.
func TestRecoverResolverUnsupportedIsPhase1Failure(t *testing.T) {
	j := validJournal()
	j.Resolver = &ResolverRecord{Manager: "resolv-conf", Prior: "nameserver 192.0.2.1\n"}
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"
	h.resolverUnsupported = true

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err == nil {
		t.Fatal("expected recovery to halt on an unrestorable resolver")
	}
	if !errors.Is(err, ErrResolverUnsupported) {
		t.Fatalf("error should wrap ErrResolverUnsupported: %v", err)
	}
	if rep.ResolverRestored {
		t.Fatal("ResolverRestored set despite the failure")
	}
	if rep.NFTablesRemoved != 0 || len(h.nftDeleted) != 0 || rep.TablesRemoved != 0 {
		t.Fatalf("phase 2 ran despite an unrestorable resolver: %+v", rep)
	}
	if !rep.KillSwitchRetained {
		t.Fatal("kill switch not retained on an unrestorable resolver")
	}
}

// A hard resolver-restore failure is likewise a phase-1 failure.
func TestRecoverHardResolverFailureGatesPhase2(t *testing.T) {
	j := validJournal()
	j.Resolver = &ResolverRecord{Manager: "resolv-conf", Prior: "x"}
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"
	h.failResolverHard = true

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err == nil {
		t.Fatal("expected a phase-1 failure")
	}
	if rep.NFTablesRemoved != 0 || !rep.KillSwitchRetained {
		t.Fatalf("kill switch not retained on a hard resolver failure: %+v", rep)
	}
}

// Second review, blocker 2: a route-table flush failure must gate nftables
// removal — the kill switch stays fail-closed.
func TestRecoverRouteTableFailureGatesNFTables(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"
	h.failDeleteRouteTable = true

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err == nil {
		t.Fatal("expected recovery to halt on a route-table flush failure")
	}
	if len(h.nftDeleted) != 0 || rep.NFTablesRemoved != 0 {
		t.Fatalf("nftables kill switch removed despite a route-table failure: %+v", rep)
	}
	if !rep.KillSwitchRetained {
		t.Fatalf("KillSwitchRetained not set: %+v", rep)
	}
}

// The nftables kill-switch removal itself failing is reported and the switch is
// recorded as retained (it is still installed).
func TestRecoverNFTableRemovalFailure(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"
	h.failDeleteNFTable = true

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err == nil {
		t.Fatal("expected a phase-2 error")
	}
	if rep.RoutesRemoved != 1 || rep.RulesRemoved != 1 || rep.TablesRemoved != 2 {
		t.Fatalf("phase 1 and route-table flush should have completed: %+v", rep)
	}
	if !rep.KillSwitchRetained {
		t.Fatal("KillSwitchRetained should be set when the nftables delete fails")
	}
}
