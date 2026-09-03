// Package buildinfo exposes version, commit, and build-date values for the
// red-mpudp binary. The three exported vars are set at link time by
// "make build" (-ldflags -X); when the binary is built without those flags
// (e.g. "go run", "go test", "go build" with no ldflags) Get falls back to the
// module version and VCS stamps recorded by the Go toolchain.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Linker-injected values. Defaults are used for un-stamped builds.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info is an immutable snapshot of the build identity.
type Info struct {
	Version   string
	Commit    string
	Date      string
	GoVersion string
}

// Get returns the current build identity, preferring linker-injected values
// and filling any that are still at their default from the Go build info.
func Get() Info {
	i := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
	}

	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return i
	}

	if i.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		i.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if i.Commit == "unknown" && s.Value != "" {
				i.Commit = shortCommit(s.Value)
			}
		case "vcs.time":
			if i.Date == "unknown" && s.Value != "" {
				i.Date = s.Value
			}
		}
	}
	return i
}

func shortCommit(rev string) string {
	const n = 12
	if len(rev) > n {
		return rev[:n]
	}
	return rev
}

// Format renders an Info as a single line. It is pure: identical input always
// produces identical output, which is what the deterministic-output test
// (BOOT-08) depends on.
func Format(i Info) string {
	return "red-mpudp " + i.Version +
		" (commit " + i.Commit +
		", built " + i.Date +
		", " + i.GoVersion + ")"
}

// String is the human-readable form of the current build.
func String() string { return Format(Get()) }
