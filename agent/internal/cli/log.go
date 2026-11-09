package cli

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/sign"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/tlog"
)

const logUsage = `Usage: assay log <command> [flags]

Commands:
  append [--dir log] [--key ~/.assay/log.key] report.json...   add signed reports, re-sign the tree head
  prove  [--dir log] [--out file] report.json                  write an inclusion proof for a report
  head   [--dir log]                                           show and check the current signed tree head
  consistency [--dir log] old-head.json                        prove the log only grew since an old head
`

func runLog(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, logUsage)
		return 2
	}
	switch args[0] {
	case "append":
		return logAppend(args[1:], stdout, stderr)
	case "prove":
		return logProve(args[1:], stdout, stderr)
	case "head":
		return logHead(args[1:], stdout, stderr)
	case "consistency":
		return logConsistency(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, logUsage)
		return 0
	}
	fmt.Fprintf(stderr, "assay log: unknown command %q\n\n%s", args[0], logUsage)
	return 2
}

func logFlags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("log "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "log", "the log directory")
	return fs, dir
}

// readSignedReport loads a report and checks its signature (not pinned: the log
// accepts reports from any agent key and records which key signed each one).
func readSignedReport(path string) (report.Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report.Report{}, err
	}
	r, err := report.Parse(data)
	if err != nil {
		return report.Report{}, err
	}
	if _, err := sign.Verify(r, nil); err != nil {
		return report.Report{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// logPubKey reads the log's public key from the log directory, if present.
func logPubKey(dir string) (ed25519.PublicKey, error) {
	pub, err := sign.LoadPublicKey(filepath.Join(dir, tlog.PubKeyFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return pub, err
}

func logAppend(args []string, stdout, stderr io.Writer) int {
	fs, dir := logFlags("append", stderr)
	keyPath := fs.String("key", filepath.Join(keyDir(), "log.key"), "the log's private key (make one with: assay keygen --name log)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() == 0 {
		fmt.Fprint(stderr, logUsage)
		return 2
	}
	fail := func(err error) int { fmt.Fprintf(stderr, "assay log append: %v\n", err); return 1 }

	priv, err := sign.LoadPrivateKey(*keyPath)
	if err != nil {
		return fail(fmt.Errorf("%w\n  make a log key first: assay keygen --name log", err))
	}
	pub := priv.Public().(ed25519.PublicKey)
	// The log's public key lives in the log directory so verifiers can pin it.
	// Refuse to sign with a different key than the one already published.
	switch existing, err := logPubKey(*dir); {
	case err != nil:
		return fail(err)
	case existing == nil:
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			return fail(err)
		}
		src, err := os.ReadFile(strings.TrimSuffix(*keyPath, ".key") + ".pub")
		if err != nil {
			return fail(fmt.Errorf("need the log public key next to the private key: %w", err))
		}
		if err := os.WriteFile(filepath.Join(*dir, tlog.PubKeyFile), src, 0o644); err != nil {
			return fail(err)
		}
	case !existing.Equal(pub):
		return fail(fmt.Errorf("this log is signed by key %s, but --key is %s", sign.KeyID(existing), sign.KeyID(pub)))
	}

	l, err := tlog.Open(*dir)
	if err != nil {
		return fail(err)
	}
	// Check every report before changing anything: all or nothing.
	var reports []report.Report
	for _, path := range fs.Args() {
		r, err := readSignedReport(path)
		if err != nil {
			return fail(fmt.Errorf("refusing to log: %w", err))
		}
		reports = append(reports, r)
	}
	for i, r := range reports {
		e, added, err := l.Append(r)
		if err != nil {
			return fail(err)
		}
		state := "added"
		if !added {
			state = "already logged"
		}
		fmt.Fprintf(stdout, "%-14s #%d  %s  %s\n", state, e.Index, e.ReportID, fs.Arg(i))
	}
	if err := l.Save(); err != nil {
		return fail(err)
	}
	head, err := l.SignHead(priv, now().Format(time.RFC3339))
	if err != nil {
		return fail(err)
	}
	if err := tlog.WriteHead(*dir, head); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "tree head: %d entries, root %s (signed by log key %s)\n", head.TreeSize, head.RootHash, head.LogKeyID)
	return 0
}

func logProve(args []string, stdout, stderr io.Writer) int {
	fs, dir := logFlags("prove", stderr)
	out := fs.String("out", "", "where to write the proof (default: <report>.proof.json)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(stderr, logUsage)
		return 2
	}
	fail := func(err error) int { fmt.Fprintf(stderr, "assay log prove: %v\n", err); return 1 }

	path := fs.Arg(0)
	r, err := readSignedReport(path)
	if err != nil {
		return fail(err)
	}
	l, head, err := openVerifiedLog(*dir)
	if err != nil {
		return fail(err)
	}
	proof, err := l.Prove(r.Signature.Digest, head)
	if err != nil {
		return fail(err)
	}
	dest := *out
	if dest == "" {
		dest = strings.TrimSuffix(path, ".json") + ".proof.json"
	}
	data, _ := json.MarshalIndent(proof, "", "  ")
	if err := os.WriteFile(dest, append(data, '\n'), 0o644); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "wrote %s: entry #%d of %d, %d hashes\n", dest, proof.LeafIndex, head.TreeSize, len(proof.AuditPath))
	return 0
}

// openVerifiedLog opens the log and its head, and checks they agree and the head
// is signed by the log key published in the directory.
func openVerifiedLog(dir string) (*tlog.Log, tlog.TreeHead, error) {
	l, err := tlog.Open(dir)
	if err != nil {
		return nil, tlog.TreeHead{}, err
	}
	head, err := tlog.ReadHead(dir)
	if err != nil {
		return nil, tlog.TreeHead{}, fmt.Errorf("reading tree head: %w", err)
	}
	pub, err := logPubKey(dir)
	if err != nil {
		return nil, tlog.TreeHead{}, err
	}
	if err := tlog.VerifyHead(head, pub); err != nil {
		return nil, tlog.TreeHead{}, err
	}
	root := l.RootHash()
	if head.TreeSize != l.Size() || head.RootHash != hex.EncodeToString(root[:]) {
		return nil, tlog.TreeHead{}, errors.New("entries.jsonl doesn't match the signed tree head: the log was changed without re-signing")
	}
	return l, head, nil
}

func logHead(args []string, stdout, stderr io.Writer) int {
	fs, dir := logFlags("head", stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_, head, err := openVerifiedLog(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "assay log head: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "OK    %d entries, root %s\n  signed %s by log key %s; matches entries.jsonl\n",
		head.TreeSize, head.RootHash, head.Timestamp, head.LogKeyID)
	return 0
}

func logConsistency(args []string, stdout, stderr io.Writer) int {
	fs, dir := logFlags("consistency", stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(stderr, logUsage)
		return 2
	}
	fail := func(err error) int { fmt.Fprintf(stdout, "FAIL  %v\n", err); return 1 }

	l, head, err := openVerifiedLog(*dir)
	if err != nil {
		return fail(err)
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	var old tlog.TreeHead
	if err := json.Unmarshal(data, &old); err != nil {
		return fail(fmt.Errorf("%s is not a tree head: %w", fs.Arg(0), err))
	}
	pub, _ := logPubKey(*dir)
	if err := tlog.VerifyHead(old, pub); err != nil {
		return fail(fmt.Errorf("old head: %w", err))
	}
	if old.TreeSize > head.TreeSize {
		return fail(fmt.Errorf("old head has %d entries but the log now has %d: entries were removed", old.TreeSize, head.TreeSize))
	}
	proof, err := l.Consistency(old.TreeSize)
	if err != nil {
		return fail(err)
	}
	oldRoot, err1 := hex.DecodeString(old.RootHash)
	newRoot, err2 := hex.DecodeString(head.RootHash)
	if err1 != nil || err2 != nil || len(oldRoot) != 32 || len(newRoot) != 32 {
		return fail(errors.New("malformed root hash"))
	}
	if err := tlog.VerifyConsistency(old.TreeSize, head.TreeSize, tlog.Hash(oldRoot), tlog.Hash(newRoot), proof); err != nil {
		return fail(errors.New("the log's history changed since the old head: earlier entries were edited, removed or reordered"))
	}
	fmt.Fprintf(stdout, "OK    log grew from %d to %d entries; all %d earlier entries are unchanged\n",
		old.TreeSize, head.TreeSize, old.TreeSize)
	return 0
}
