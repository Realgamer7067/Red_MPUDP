// Package config parses and validates all RED_MPUDP operator input — client
// and server YAML plus the values derived from it — before any privileged or
// network activity occurs. Loading is strict: unknown fields and duplicate keys
// are errors, durations and addresses are typed, and every numeric bound from
// the design is enforced here rather than at the point of use.
package config

import (
	"fmt"
	"time"
)

// Duration is a time.Duration that unmarshals from a Go duration string
// ("250ms", "1h", "120s"). A bare number is rejected: the unit must be
// explicit. Negative and zero durations are rejected by field validation, not
// here, so callers can distinguish "unset" from "invalid".
type Duration time.Duration

// UnmarshalYAML implements yaml unmarshaling from a quoted duration string.
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"250ms\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// FWMark is a Linux firewall mark. Zero is reserved for "no mark" and is
// rejected wherever a mark is required.
type FWMark uint32

func (m FWMark) String() string { return fmt.Sprintf("0x%08x", uint32(m)) }

// RouteTable is a Linux routing-table identifier. The reserved tables
// (0 unspec, 253 default, 254 main, 255 local) are rejected.
type RouteTable uint32

const (
	rtUnspec  RouteTable = 0
	rtDefault RouteTable = 253
	rtMain    RouteTable = 254
	rtLocal   RouteTable = 255
	rtMax     RouteTable = 0x7fffffff
)

func (t RouteTable) reserved() bool {
	return t == rtUnspec || t == rtDefault || t == rtMain || t == rtLocal || t > rtMax
}

// RulePriority is an `ip rule` priority. It must sit below the kernel's default
// main-table fallback at 32766.
type RulePriority uint32

const rulePriorityCeiling RulePriority = 32765
