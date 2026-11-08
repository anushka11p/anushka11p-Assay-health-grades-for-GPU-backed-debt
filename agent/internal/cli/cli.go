// Package cli is the command-line front end of the assay agent.
// It reads the subcommand (check, keygen, verify, ...) and hands off to it.
// The real work for each command lives in its own package, added day by day.
package cli

import (
	"fmt"
	"io"
)

// Version is the agent version written into every report.
const Version = "v0.0.1-dev"

const usage = `assay: GPU health checks with signed, verifiable reports.

Usage: assay <command> [flags]   (assay <command> -h for its flags)

Commands:
  check     Read GPU telemetry, run probes, write a signed report
  keygen    Create this install's Ed25519 signing key
  verify    Check a report's signature and log inclusion
  version   Print the agent version
  help      Show this message
`

// Run executes one assay command. It returns the process exit code:
// 0 = success, 1 = the command failed, 2 = the command line was wrong.
// stdout/stderr are passed in (instead of using os.Stdout) so tests can capture them.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version":
		fmt.Fprintln(stdout, "assay "+Version)
		return 0
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "keygen", "verify":
		fmt.Fprintf(stderr, "assay %s: not implemented yet\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "assay: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
