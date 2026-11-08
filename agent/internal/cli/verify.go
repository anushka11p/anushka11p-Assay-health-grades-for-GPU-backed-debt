package cli

import (
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/sign"
)

func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pubPath := fs.String("pubkey", "", "trusted public key (agent.pub); checks WHO signed, not just that it's unmodified")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: assay verify [--pubkey agent.pub] report.json")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	path := fs.Arg(0)

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "assay verify: %v\n", err)
		return 1
	}
	var r report.Report
	if err := json.Unmarshal(data, &r); err != nil {
		fmt.Fprintf(stderr, "assay verify: %s is not a valid report: %v\n", path, err)
		return 1
	}

	var trusted ed25519.PublicKey
	if *pubPath != "" {
		if trusted, err = sign.LoadPublicKey(*pubPath); err != nil {
			fmt.Fprintf(stderr, "assay verify: %v\n", err)
			return 1
		}
	}

	res, err := sign.Verify(r, trusted)
	if err != nil {
		fmt.Fprintf(stdout, "FAIL  %s: %v\n", path, err)
		return 1
	}
	fmt.Fprintf(stdout, "OK    %s\n  report  %s (%s)\n  key     %s\n  digest  %s\n", path, r.ReportID, r.CreatedAt, res.KeyID, res.Digest)
	if !res.Pinned {
		fmt.Fprintln(stdout, "  note    unmodified since signed, but signer not checked; pass --pubkey to check who signed it")
	}
	return 0
}
