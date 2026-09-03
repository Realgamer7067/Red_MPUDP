// Command red-mpudp is the single entry point for the RED_MPUDP client,
// server, and operator tooling. v1 milestone M01 wires up only the process
// skeleton and the "version" subcommand; every other subcommand is added by a
// later milestone.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Realgamer7067/Red_MPUDP/internal/buildinfo"
)

const usage = `red-mpudp - latency-first multipath UDP VPN

usage:
  red-mpudp <subcommand> [options]

subcommands:
  version    print build version information

(other subcommands are added in later milestones)`

// Exit codes. M04 (CLI-10) defines the full stable set; M01 uses only these.
const (
	exitOK    = 0
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, buildinfo.String())
		return exitOK
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "red-mpudp: unknown subcommand %q\n\n%s\n", args[0], usage)
		return exitUsage
	}
}
