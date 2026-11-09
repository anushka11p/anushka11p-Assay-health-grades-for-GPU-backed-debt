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
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/tlog"
)

func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pubPath := fs.String("pubkey", "", "trusted public key (agent.pub); checks WHO signed, not just that it's unmodified")
	proofPath := fs.String("proof", "", "inclusion proof from `assay log prove`; also checks the report is in the transparency log")
	logPubPath := fs.String("log-pubkey", "", "trusted log public key (log/log.pub); checks WHICH log the proof comes from")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: assay verify [--pubkey agent.pub] [--proof report.proof.json --log-pubkey log.pub] report.json")
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
	r, err := report.Parse(data)
	if err != nil {
		fmt.Fprintf(stderr, "assay verify: %s: %v\n", path, err)
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
	if *proofPath == "" {
		return 0
	}

	data, err = os.ReadFile(*proofPath)
	if err != nil {
		fmt.Fprintf(stderr, "assay verify: %v\n", err)
		return 1
	}
	var proof tlog.Proof
	if err := json.Unmarshal(data, &proof); err != nil {
		fmt.Fprintf(stderr, "assay verify: %s is not a proof: %v\n", *proofPath, err)
		return 1
	}
	var trustedLog ed25519.PublicKey
	if *logPubPath != "" {
		if trustedLog, err = sign.LoadPublicKey(*logPubPath); err != nil {
			fmt.Fprintf(stderr, "assay verify: %v\n", err)
			return 1
		}
	}
	if err := tlog.VerifyProof(r.Signature.Digest, proof, trustedLog); err != nil {
		fmt.Fprintf(stdout, "FAIL  %s: not proven to be in the log: %v\n", path, err)
		return 1
	}
	h := proof.TreeHead
	fmt.Fprintf(stdout, "  log     entry #%d of %d in the log signed by key %s at %s\n", proof.LeafIndex, h.TreeSize, h.LogKeyID, h.Timestamp)
	if trustedLog == nil {
		fmt.Fprintln(stdout, "  note    log key not checked; pass --log-pubkey to check which log this is")
	}
	return 0
}
