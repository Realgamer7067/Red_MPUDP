package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"

	"github.com/Realgamer7067/Red_MPUDP/internal/journal"
)

// cmdCleanup implements
//
//	red-mpudp cleanup --state-file <path> [--role client|server] [--instance <id>]
//
// (design §11.5, JOURNAL-21..23). It performs the same ownership-checked
// recovery the daemon runs on restart.
//
// The journal is trusted only after journal.Load has verified — from an
// O_NOFOLLOW descriptor — that the file is a single-hard-link regular file,
// mode 0600 or tighter, owned by the caller or root: a non-privileged user
// cannot plant such a file under /run/red-mpudp. The optional --role and
// --instance flags let an operator additionally pin the expected owner; a
// mismatch is refused before any mutation. A missing, malformed, or
// mismatched journal is refused rather than acted on.
func cmdCleanup(args []string, stdout, stderr io.Writer) int {
	fs_ := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	fs_.SetOutput(stderr)
	stateFile := fs_.String("state-file", "", "path to the mutation journal (required)")
	role := fs_.String("role", "", "require the journal to record this role: client or server (optional)")
	instance := fs_.String("instance", "", "require the journal to record this instance ID (optional)")
	if err := fs_.Parse(args); err != nil {
		return exitUsage
	}
	if *stateFile == "" {
		fmt.Fprintln(stderr, "red-mpudp: cleanup requires --state-file <path>")
		return exitUsage
	}
	var wantRole journal.Role
	switch *role {
	case "":
		// no explicit pin; trust the validated journal's own role
	case "client":
		wantRole = journal.RoleClient
	case "server":
		wantRole = journal.RoleServer
	default:
		fmt.Fprintf(stderr, "red-mpudp: cleanup: --role %q must be client or server\n", *role)
		return exitUsage
	}

	j, err := journal.Load(*stateFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr, "red-mpudp: no journal at %s; refusing to guess what to clean up\n", *stateFile)
			return exitConfig
		}
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitConfig
	}

	if wantRole != "" && j.Role != wantRole {
		fmt.Fprintf(stderr, "red-mpudp: journal at %s records role %q, but --role %s was given; refusing\n", *stateFile, j.Role, *role)
		return exitConfig
	}
	if *instance != "" && j.InstanceID != *instance {
		fmt.Fprintf(stderr, "red-mpudp: journal instance %q does not match --instance %q; refusing\n", j.InstanceID, *instance)
		return exitConfig
	}

	logf := func(format string, a ...any) { fmt.Fprintf(stderr, "red-mpudp: "+format+"\n", a...) }
	rep, err := journal.Recover(j, j.Role, *instance, journal.LinuxHost{}, logf)
	fmt.Fprintf(stdout, "recovered instance %s (%s): %d routes, %d rules, %d tables, %d nftables, %d sysctls restored\n",
		j.InstanceID, j.Role, rep.RoutesRemoved, rep.RulesRemoved, rep.TablesRemoved, rep.NFTablesRemoved, len(rep.SysctlsRestored))
	for _, k := range rep.SysctlConflicts {
		fmt.Fprintf(stdout, "  sysctl %s preserved (operator-modified)\n", k)
	}
	if rep.ResolverDeferred {
		fmt.Fprintln(stderr, "red-mpudp: resolver state was recorded but not restored automatically; restore /etc/resolv.conf or the systemd-resolved link manually")
	}
	if rep.KillSwitchRetained {
		fmt.Fprintln(stderr, "red-mpudp: kill switch retained fail-closed; re-run cleanup after resolving the errors above")
	}
	if err != nil {
		fmt.Fprintf(stderr, "red-mpudp: recovery incomplete: %v\n", err)
		return exitRuntime
	}
	return exitOK
}
