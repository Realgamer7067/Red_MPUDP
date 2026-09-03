package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/Realgamer7067/Red_MPUDP/internal/journal"
)

// cmdCleanup implements
//
//	red-mpudp cleanup --role client|server [--state-file <path>] [--instance <id>]
//
// (design §11.5, JOURNAL-21..23). It performs the same ownership-checked
// recovery the daemon runs on restart. The operator must state which role's
// state they are recovering; that assertion is checked against the journal
// before any mutation, so a journal at an unexpected path (or one that was
// swapped) cannot direct recovery on its own say-so. A missing, malformed, or
// mismatched journal is refused rather than acted on.
func cmdCleanup(args []string, stdout, stderr io.Writer) int {
	fs_ := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	fs_.SetOutput(stderr)
	role := fs_.String("role", "", "role whose host state to recover: client or server (required)")
	stateFile := fs_.String("state-file", "", "path to the mutation journal (default: /run/red-mpudp/<role>.journal)")
	instance := fs_.String("instance", "", "require the journal to record this instance ID (optional)")
	if err := fs_.Parse(args); err != nil {
		return exitUsage
	}

	var wantRole journal.Role
	switch *role {
	case "client":
		wantRole = journal.RoleClient
	case "server":
		wantRole = journal.RoleServer
	default:
		fmt.Fprintln(stderr, "red-mpudp: cleanup requires --role client|server")
		return exitUsage
	}

	path := *stateFile
	if path == "" {
		path = filepath.Join(journal.DefaultDir, string(wantRole)+".journal")
	}

	j, err := journal.Load(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr, "red-mpudp: no journal at %s; refusing to guess what to clean up\n", path)
			return exitConfig
		}
		fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
		return exitConfig
	}

	// Independent ownership assertion: the operator said which role this is;
	// the journal must agree before anything is removed.
	if j.Role != wantRole {
		fmt.Fprintf(stderr, "red-mpudp: journal at %s records role %q, but --role %s was given; refusing\n", path, j.Role, *role)
		return exitConfig
	}
	if *instance != "" && j.InstanceID != *instance {
		fmt.Fprintf(stderr, "red-mpudp: journal instance %q does not match --instance %q; refusing\n", j.InstanceID, *instance)
		return exitConfig
	}

	logf := func(format string, a ...any) { fmt.Fprintf(stderr, "red-mpudp: "+format+"\n", a...) }
	rep, err := journal.Recover(j, wantRole, *instance, journal.LinuxHost{}, logf)
	fmt.Fprintf(stdout, "recovered instance %s (%s): %d routes, %d rules, %d tables, %d nftables, %d sysctls restored\n",
		j.InstanceID, j.Role, rep.RoutesRemoved, rep.RulesRemoved, rep.TablesRemoved, rep.NFTablesRemoved, len(rep.SysctlsRestored))
	for _, k := range rep.SysctlConflicts {
		fmt.Fprintf(stdout, "  sysctl %s preserved (operator-modified)\n", k)
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
