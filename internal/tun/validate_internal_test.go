package tun

import (
	"errors"
	"net/netip"
	"testing"
)

func validBase() Config {
	return Config{
		Name:    "red0",
		Address: netip.MustParsePrefix("10.9.0.2/24"),
		MTU:     DefaultMTU,
	}
}

// TUN-06 / TUN-15: validate is the single gate Open runs before any descriptor
// is opened. Exercise its rules directly rather than inferring them from Open's
// behaviour on an unprivileged host.
func TestValidateConfig(t *testing.T) {
	if err := validBase().validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	// An empty name is valid: the kernel assigns one (TUN-07).
	c := validBase()
	c.Name = ""
	if err := c.validate(); err != nil {
		t.Fatalf("empty (kernel-assigned) name rejected: %v", err)
	}

	// Zero MaxPacket is valid and means "MTU".
	c = validBase()
	c.MaxPacket = 0
	if err := c.validate(); err != nil {
		t.Fatalf("zero MaxPacket rejected: %v", err)
	}
	if c.maxPacket() != DefaultMTU {
		t.Fatalf("maxPacket() = %d, want %d", c.maxPacket(), DefaultMTU)
	}

	bad := []struct {
		name string
		mut  func(*Config)
		want error // nil = any error is acceptable
	}{
		{"name too long", func(c *Config) { c.Name = "an-interface-name-way-too-long" }, nil},
		{"name with slash", func(c *Config) { c.Name = "red/0" }, nil},
		{"name with space", func(c *Config) { c.Name = "red 0" }, nil},
		{"name with NUL", func(c *Config) { c.Name = "red\x000" }, nil},
		{"name is dotdot", func(c *Config) { c.Name = ".." }, nil},
		{"mtu below range", func(c *Config) { c.MTU = MinMTU - 1 }, ErrMTURange},
		{"mtu above range", func(c *Config) { c.MTU = MaxMTU + 1 }, ErrMTURange},
		{"no address", func(c *Config) { c.Address = netip.Prefix{} }, nil},
		{"ipv6 address", func(c *Config) { c.Address = netip.MustParsePrefix("fd00::1/64") }, nil},
		{"negative MaxPacket", func(c *Config) { c.MaxPacket = -1 }, nil},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			c := validBase()
			tc.mut(&c)
			err := c.validate()
			if err == nil {
				t.Fatalf("invalid config accepted")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TUN-16 / TUN-17: reductions always apply; a live increase needs an
// authenticated committed value; an out-of-range target is refused regardless.
func TestGuardMTU(t *testing.T) {
	if got, err := guardMTU(DefaultMTU, DefaultMTU-40, false); err != nil || got != DefaultMTU-40 {
		t.Fatalf("reduction: (%d, %v)", got, err)
	}
	if _, err := guardMTU(DefaultMTU-40, DefaultMTU, false); !errors.Is(err, ErrMTUIncrease) {
		t.Fatalf("unauthenticated increase: %v", err)
	}
	if got, err := guardMTU(DefaultMTU-40, DefaultMTU, true); err != nil || got != DefaultMTU {
		t.Fatalf("authenticated increase: (%d, %v)", got, err)
	}
	if _, err := guardMTU(DefaultMTU, MinMTU-1, true); !errors.Is(err, ErrMTURange) {
		t.Fatalf("below range even authenticated: %v", err)
	}
	if _, err := guardMTU(DefaultMTU, MaxMTU+1, true); !errors.Is(err, ErrMTURange) {
		t.Fatalf("above range even authenticated: %v", err)
	}
	if got, err := guardMTU(DefaultMTU, DefaultMTU, false); err != nil || got != DefaultMTU {
		t.Fatalf("no-op change: (%d, %v)", got, err)
	}
}
