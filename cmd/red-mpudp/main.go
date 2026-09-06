// Command red-mpudp is the single entry point for the RED_MPUDP client,
// server, and operator tooling. As of milestone M04 the CLI parses and
// validates all operator input — configuration and key material — but does not
// yet start the data plane; that arrives in later milestones.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Realgamer7067/Red_MPUDP/internal/buildinfo"
)

const usage = `red-mpudp - latency-first multipath UDP VPN

usage:
  red-mpudp <subcommand> [options]

subcommands:
  version                       print build version information
  keygen <path>                 write a new X25519 private key to <path> (mode 0600)
  public-key <private-key-file>  print the X25519 public key for a private key file
  psk <path>                    write a new 32-byte pre-shared key to <path> (mode 0600)
  check-config client <file>    parse and validate a client configuration
  check-config server <file>    parse and validate a server configuration
  client <file>                 parse a client configuration and prepare to run
  server <file>                 parse a server configuration and prepare to run
  cleanup --state-file <path>   undo host changes recorded in a mutation journal

Secrets are always file references. They are never accepted as command-line
flags.`

// Stable process exit codes (CLI-10). Callers and scripts may depend on these.
const (
	exitOK         = 0
	exitUsage      = 2
	exitConfig     = 3
	exitPermission = 4
	exitRuntime    = 5
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return exitUsage
	}

	if code, bad := rejectSecretFlags(args, stderr); bad {
		return code
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, buildinfo.String())
		return exitOK
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, usage)
		return exitOK
	case "keygen":
		return cmdKeygen(args[1:], stdout, stderr)
	case "public-key":
		return cmdPublicKey(args[1:], stdout, stderr)
	case "psk":
		return cmdPSK(args[1:], stdout, stderr)
	case "check-config":
		return cmdCheckConfig(args[1:], stdout, stderr)
	case "client", "server":
		return cmdRunPrepare(args[0], args[1:], stdout, stderr)
	case "cleanup":
		return cmdCleanup(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "red-mpudp: unknown subcommand %q\n\n%s\n", args[0], usage)
		return exitUsage
	}
}

// rejectSecretFlags enforces CLI-11: key material is never passed on the
// command line. Any flag whose name suggests a secret value is refused.
func rejectSecretFlags(args []string, stderr io.Writer) (int, bool) {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.ToLower(strings.TrimLeft(a, "-"))
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		for _, banned := range []string{"psk", "key", "secret", "private-key", "preshared"} {
			if name == banned || strings.Contains(name, banned) {
				fmt.Fprintf(stderr, "red-mpudp: %s: secrets must be file references, not command-line flags\n", a)
				return exitUsage, true
			}
		}
	}
	return 0, false
}
