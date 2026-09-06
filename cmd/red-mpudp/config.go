package main

import (
	"fmt"
	"io"

	"github.com/Realgamer7067/Red_MPUDP/internal/config"
)

// cmdCheckConfig implements `check-config client|server <file>`. It performs no
// network or filesystem mutation (CLI-07): it only reads and validates.
func cmdCheckConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "red-mpudp: usage: check-config client|server <file>")
		return exitUsage
	}
	role, path := args[0], args[1]
	switch role {
	case "client":
		if _, err := config.LoadClientFile(path); err != nil {
			fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
			return exitConfig
		}
	case "server":
		if _, err := config.LoadServerFile(path); err != nil {
			fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
			return exitConfig
		}
	default:
		fmt.Fprintf(stderr, "red-mpudp: check-config: unknown role %q (want client or server)\n", role)
		return exitUsage
	}
	fmt.Fprintf(stdout, "%s configuration %s is valid\n", role, path)
	return exitOK
}

// cmdRunPrepare implements `client|server <file>` for M04: it parses arguments
// and loads the configuration but does not start the data plane (CLI-08,
// CLI-09).
func cmdRunPrepare(role string, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "red-mpudp: usage: %s <file>\n", role)
		return exitUsage
	}
	path := args[0]
	switch role {
	case "client":
		if _, err := config.LoadClientFile(path); err != nil {
			fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
			return exitConfig
		}
	case "server":
		if _, err := config.LoadServerFile(path); err != nil {
			fmt.Fprintf(stderr, "red-mpudp: %v\n", err)
			return exitConfig
		}
	}
	fmt.Fprintf(stdout, "%s configuration %s loaded; data plane startup is implemented in a later milestone\n", role, path)
	return exitOK
}
