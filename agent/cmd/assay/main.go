// Command assay is the GPU health-check agent.
// main stays tiny on purpose: all logic is in internal/cli, where it can be tested.
package main

import (
	"os"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
