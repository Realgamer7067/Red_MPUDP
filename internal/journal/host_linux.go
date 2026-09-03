//go:build linux

package journal

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// LinuxHost implements Host by invoking ip(8), sysctl(8), and nft(8). Every
// delete is treated as idempotent: a "no such file or directory" / "does not
// exist" result from the tool is reported as success.
//
// This type is exercised only by the privileged integration suite; unit tests
// use a fake Host.
type LinuxHost struct {
	// IPPath, SysctlPath, NFTPath override tool locations for testing. Empty
	// means look up on PATH.
	IPPath, SysctlPath, NFTPath string
}

func (h LinuxHost) ipCmd() string {
	if h.IPPath != "" {
		return h.IPPath
	}
	return "ip"
}

func run(name string, args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String() + errb.String(), err
}

func idempotentDelete(combined string, err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(combined)
	for _, ok := range []string{"no such", "does not exist", "cannot find", "not found", "no such process"} {
		if strings.Contains(low, ok) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", err, strings.TrimSpace(combined))
}

// GetSysctl reads /proc/sys directly to avoid depending on sysctl(8).
func (h LinuxHost) GetSysctl(key string) (string, error) {
	p := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// SetSysctl writes /proc/sys directly.
func (h LinuxHost) SetSysctl(key, value string) error {
	p := "/proc/sys/" + strings.ReplaceAll(key, ".", "/")
	return os.WriteFile(p, []byte(value+"\n"), 0)
}

// DeleteRoute removes exactly one route, matching on every identifier the
// journal recorded — destination, table, device, gateway, and metric — so a
// route that differs only by metric from an operator's own route is not
// removed by mistake.
func (h LinuxHost) DeleteRoute(r RouteRecord) error {
	args := []string{"route", "del", r.Dst, "table", strconv.FormatUint(uint64(r.Table), 10)}
	if r.Dev != "" {
		args = append(args, "dev", r.Dev)
	}
	if r.Via != "" {
		args = append(args, "via", r.Via)
	}
	if r.Priority != 0 {
		args = append(args, "metric", strconv.FormatUint(uint64(r.Priority), 10))
	}
	return idempotentDelete(run(h.ipCmd(), args...))
}

func (h LinuxHost) DeleteRule(r RuleRecord) error {
	fam := "-4"
	if r.Family == "ip6" {
		fam = "-6"
	}
	args := []string{fam, "rule", "del", "priority", strconv.FormatUint(uint64(r.Priority), 10),
		"table", strconv.FormatUint(uint64(r.Table), 10)}
	if r.FWMark != 0 {
		args = append(args, "fwmark", fmt.Sprintf("0x%x", r.FWMark))
	}
	return idempotentDelete(run(h.ipCmd(), args...))
}

// DeleteRouteTable flushes every route in the given table. The table itself is
// a kernel-side namespace and needs no explicit removal once empty.
func (h LinuxHost) DeleteRouteTable(id uint32) error {
	return idempotentDelete(run(h.ipCmd(), "route", "flush", "table", strconv.FormatUint(uint64(id), 10)))
}

func (h LinuxHost) DeleteNFTable(t NFTableRecord) error {
	nft := h.NFTPath
	if nft == "" {
		nft = "nft"
	}
	return idempotentDelete(run(nft, "delete", "table", t.Family, t.Name))
}

// RestoreResolver is implemented in M20 alongside the resolver manager; until
// then it refuses rather than silently doing nothing.
func (h LinuxHost) RestoreResolver(r ResolverRecord) error {
	return fmt.Errorf("journal: resolver restore for manager %q is implemented in a later milestone", r.Manager)
}
