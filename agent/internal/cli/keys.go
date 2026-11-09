package cli

import (
	"crypto/ed25519"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/sign"
)

// keyDir is where keys live: $ASSAY_HOME if set, otherwise ~/.assay.
func keyDir() string {
	if d := os.Getenv("ASSAY_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".assay"
	}
	return filepath.Join(home, ".assay")
}

func runKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", keyDir(), "where to write the key files")
	name := fs.String("name", "agent", `"agent" signs reports; "log" signs the transparency log's tree heads`)
	force := fs.Bool("force", false, "replace an existing key (old reports stay valid, but no longer match this install)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	priv, err := sign.GenerateKey()
	if err != nil {
		fmt.Fprintf(stderr, "assay keygen: %v\n", err)
		return 1
	}
	if *name != "agent" && *name != "log" {
		fmt.Fprintf(stderr, "assay keygen: --name must be agent or log\n")
		return 2
	}
	if err := sign.SaveKeyPairAs(*dir, *name, priv, *force); err != nil {
		fmt.Fprintf(stderr, "assay keygen: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "key %s\n  private: %s  (keep secret, never commit)\n  public:  %s  (share this; verifiers pin it)\n",
		sign.KeyID(priv.Public().(ed25519.PublicKey)), filepath.Join(*dir, *name+".key"), filepath.Join(*dir, *name+".pub"))
	return 0
}
