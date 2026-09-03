package journal

import (
	"errors"
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

	failDeleteRoute bool
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
	h.tablesDeleted = append(h.tablesDeleted, id)
	return nil
}
func (h *fakeHost) DeleteNFTable(t NFTableRecord) error {
	h.nftDeleted = append(h.nftDeleted, t)
	return nil
}
func (h *fakeHost) RestoreResolver(ResolverRecord) error { h.resolver++; return nil }

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

func TestRecoverAggregatesErrors(t *testing.T) {
	j := validJournal()
	h := newFakeHost()
	h.sysctl["net.ipv4.ip_forward"] = "1"
	h.failDeleteRoute = true

	rep, err := Recover(j, RoleClient, j.InstanceID, h, nil)
	if err == nil {
		t.Fatal("expected an aggregated error")
	}
	// Other work still proceeded.
	if rep.RulesRemoved != 1 || rep.TablesRemoved != 2 {
		t.Fatalf("recovery stopped early on a route error: %+v", rep)
	}
}
