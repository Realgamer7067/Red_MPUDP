package journal

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func validJournal() *Journal {
	return &Journal{
		Schema:     SchemaVersion,
		Role:       RoleClient,
		InstanceID: "red-mpudp-01HXYZ",
		CreatedAt:  time.Unix(1_700_000_000, 0).UTC(),
		Routes:     []RouteRecord{{Table: 200, Dst: "default", Dev: "red0"}},
		Rules:      []RuleRecord{{Family: "ip", Priority: 11000, Table: 200}},
		Tables:     []uint32{200, 201},
		NFTables:   []NFTableRecord{{Family: "inet", Name: "red_mpudp_ks"}},
		Sysctls:    []SysctlRecord{{Name: "net.ipv4.ip_forward", Prior: "0", Installed: "1"}},
	}
}

func TestValidateAcceptsGoodJournal(t *testing.T) {
	if err := validJournal().Validate(); err != nil {
		t.Fatalf("valid journal rejected: %v", err)
	}
}

func TestValidateRejections(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Journal)
	}{
		{"bad schema", func(j *Journal) { j.Schema = 999 }},
		{"empty role", func(j *Journal) { j.Role = "" }},
		{"bad role", func(j *Journal) { j.Role = "router" }},
		{"short instance id", func(j *Journal) { j.InstanceID = "abc" }},
		{"instance id with slash", func(j *Journal) { j.InstanceID = "../../etc/passwd" }},
		{"route dst not cidr", func(j *Journal) { j.Routes[0].Dst = "10.0.0.1" }},
		{"route bad dev", func(j *Journal) { j.Routes[0].Dev = "eth0; rm -rf /" }},
		{"route zero table", func(j *Journal) { j.Routes[0].Table = 0 }},
		{"rule bad family", func(j *Journal) { j.Rules[0].Family = "arp" }},
		{"table zero", func(j *Journal) { j.Tables[0] = 0 }},
		{"nft bad family", func(j *Journal) { j.NFTables[0].Family = "bridge" }},
		{"nft bad name", func(j *Journal) { j.NFTables[0].Name = "bad name!" }},
		{"sysctl bad name", func(j *Journal) { j.Sysctls[0].Name = "net/ipv4/ip_forward" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := validJournal()
			tc.mut(j)
			if err := j.Validate(); err == nil {
				t.Fatalf("%s: expected rejection", tc.name)
			}
		})
	}
}

func TestCheckOwner(t *testing.T) {
	j := validJournal()
	if err := j.checkOwner(RoleClient, "red-mpudp-01HXYZ"); err != nil {
		t.Fatalf("matching owner rejected: %v", err)
	}
	if err := j.checkOwner(RoleServer, ""); err == nil {
		t.Fatal("role mismatch accepted")
	}
	if err := j.checkOwner(RoleClient, "different-instance"); err == nil {
		t.Fatal("instance mismatch accepted")
	}
	if err := j.checkOwner(RoleClient, ""); err != nil {
		t.Fatalf("empty instance filter rejected: %v", err)
	}
}

// JOURNAL-02: no field anywhere in the schema may carry secret material.
func TestSchemaHasNoSecretFields(t *testing.T) {
	banned := []string{"key", "psk", "secret", "token", "password", "passwd", "packet", "plaintext", "ciphertext", "nonce", "cookie"}
	var walk func(rt reflect.Type, path string)
	seen := map[reflect.Type]bool{}
	walk = func(rt reflect.Type, path string) {
		if seen[rt] {
			return
		}
		seen[rt] = true
		switch rt.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(rt.Elem(), path)
		case reflect.Struct:
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				lname := strings.ToLower(f.Name)
				for _, b := range banned {
					if strings.Contains(lname, b) {
						t.Errorf("%s.%s: field name contains %q; the journal must hold no secret material", path, f.Name, b)
					}
				}
				walk(f.Type, path+"."+f.Name)
			}
		}
	}
	walk(reflect.TypeOf(Journal{}), "Journal")
}
