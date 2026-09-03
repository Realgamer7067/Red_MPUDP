package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"

	"github.com/Realgamer7067/Red_MPUDP/internal/journal"
)

// cmdCleanup implements `red-mpudp cleanup --state-file <path>` (design §11.5,
// JOURNAL-21..23). It performs the same ownership-checked recovery the daemon
// runs on restart, and refuses a missing, malformed, or mismatched journal
// rather than deleting broad networking state.
func cmdCleanup(args []string, stdout, stderr io.Writer) int {
	fs_ := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	fs_.SetOutput(stderr)
	stateFile := fs_.String("state-file", "", "path to the mutation journal to recover from")
	if err := fs_.Parse(args); err != nil {
		return exitUsage
	}
	if *stateFile == "" {
		fmt.Fprintln(stderr, "red-mpudp: cleanup requires --state-file <path>")
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

	logf := func(format string, a ...any) { fmt.Fprintf(stderr, "red-mpudp: "+format+"\n", a...) }
	rep, err := journal.Recover(j, j.Role, j.InstanceID, journal.LinuxHost{}, logf)
	fmt.Fprintf(stdout, "recovered instance %s (%s): %d routes, %d rules, %d tables, %d nftables, %d sysctls restored\n",
		j.InstanceID, j.Role, rep.RoutesRemoved, rep.RulesRemoved, rep.TablesRemoved, rep.NFTablesRemoved, len(rep.SysctlsRestored))
	for _, k := range rep.SysctlConflicts {
		fmt.Fprintf(stdout, "  sysctl %s preserved (operator-modified)\n", k)
	}
	if err != nil {
		fmt.Fprintf(stderr, "red-mpudp: recovery incomplete: %v\n", err)
		return exitRuntime
	}
	return exitOK
}
