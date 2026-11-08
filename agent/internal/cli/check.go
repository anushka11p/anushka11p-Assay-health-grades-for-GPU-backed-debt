package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/sign"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry/fake"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry/nvmlbackend"
)

// These are variables (not direct calls) so tests can pin the time and report ID
// and get byte-identical output every run.
var (
	now   = func() time.Time { return time.Now().UTC() }
	newID = func() string {
		b := make([]byte, 16)
		_, _ = rand.Read(b) // crypto/rand never fails on supported platforms
		return hex.EncodeToString(b)
	}
	sleep = time.Sleep
)

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	backendName := fs.String("backend", "nvml", `telemetry source: "nvml" (real GPU) or "fake"`)
	scenario := fs.String("scenario", "healthy", "fake backend only: "+strings.Join(fake.Scenarios(), ", "))
	samples := fs.Int("samples", 20, "readings to take per GPU")
	interval := fs.Duration("interval", 250*time.Millisecond, "time between readings")
	out := fs.String("out", "report.json", `where to write the report ("-" for stdout)`)
	provider := fs.String("provider", "", `override the detected provider (e.g. "colab", "kaggle", "lab")`)
	keyPath := fs.String("key", filepath.Join(keyDir(), sign.PrivateKeyFile), "private key to sign the report with")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Load the key first: no point measuring a GPU for minutes if we can't sign the result.
	priv, err := sign.LoadPrivateKey(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "assay check: can't load signing key: %v\n  run `assay keygen` first (or pass --key)\n", err)
		return 1
	}

	var backend telemetry.Backend
	agent := report.Agent{Version: Version, Backend: *backendName}
	switch *backendName {
	case "fake":
		b, err := fake.New(*scenario)
		if err != nil {
			fmt.Fprintf(stderr, "assay check: %v\n", err)
			return 2
		}
		backend = b
		agent.Scenario = *scenario
	case "nvml":
		b, err := nvmlbackend.New()
		if err != nil {
			fmt.Fprintf(stderr, "assay check: %v\n", err)
			return 1
		}
		backend = b
	default:
		fmt.Fprintf(stderr, "assay check: unknown backend %q (use nvml or fake)\n", *backendName)
		return 2
	}
	defer backend.Close()

	host, err := backend.Host()
	if err != nil {
		fmt.Fprintf(stderr, "assay check: reading host info: %v\n", err)
		return 1
	}
	if *provider != "" {
		host.Provider = *provider
	}
	gpus, err := telemetry.Collect(backend, telemetry.Options{Samples: *samples, Interval: *interval, Sleep: sleep})
	if err != nil {
		fmt.Fprintf(stderr, "assay check: %v\n", err)
		return 1
	}

	r := report.Report{
		SchemaVersion: report.SchemaVersion,
		ReportID:      newID(),
		CreatedAt:     now().Format(time.RFC3339),
		Agent:         agent,
		Host:          host,
		GPUs:          gpus,
	}
	if err := sign.Sign(&r, priv); err != nil {
		fmt.Fprintf(stderr, "assay check: signing report: %v\n", err)
		return 1
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "assay check: %v\n", err)
		return 1
	}
	data = append(data, '\n')

	if *out == "-" {
		_, err = stdout.Write(data)
	} else {
		err = os.WriteFile(*out, data, 0o644)
	}
	if err != nil {
		fmt.Fprintf(stderr, "assay check: writing report: %v\n", err)
		return 1
	}

	where := *out
	if where == "-" {
		where = "stdout"
	}
	fmt.Fprintf(stderr, "assay check: wrote report %s to %s (signed by key %s)\n", r.ReportID, where, r.Signature.KeyID)
	for _, g := range gpus {
		writeSummary(stderr, g)
	}
	return 0
}

// writeSummary prints a short human-readable health summary for one GPU.
func writeSummary(w io.Writer, g report.GPU) {
	fmt.Fprintf(w, "\nGPU %d  %s  serial %s\n", g.Index, g.Identity.Name, g.Identity.Serial)

	m := g.Memory
	retired := "n/a"
	if m.RetiredPages != nil {
		retired = fmt.Sprintf("%d single-bit, %d double-bit", m.RetiredPages.SingleBit, m.RetiredPages.DoubleBit)
		if m.RetiredPages.PendingRetirement {
			retired += ", retirement pending"
		}
	}
	fmt.Fprintf(w, "  memory    ECC errors (lifetime): %s corrected, %s uncorrected; retired pages: %s\n",
		show(m.Aggregate.Corrected), show(m.Aggregate.Uncorrected), retired)

	ic := g.Interconnect
	fmt.Fprintf(w, "  pcie      Gen%s x%s (max Gen%s x%s), %s replays\n",
		show(ic.PCIeGenCurrent), show(ic.PCIeWidthCurrent), show(ic.PCIeGenMax), show(ic.PCIeWidthMax), show(ic.PCIeReplayCounter))

	var maxT int64 = -1
	throttled := 0
	for _, s := range g.Samples {
		if s.TempC != nil {
			maxT = max(maxT, *s.TempC)
		}
		if slices.ContainsFunc(s.ThrottleReasons, telemetry.IsSlowdown) {
			throttled++ // being idle isn't throttling; heat, power and hardware limits are
		}
	}
	maxTemp := "n/a"
	if maxT >= 0 {
		maxTemp = fmt.Sprintf("%d°C", maxT)
	}
	fmt.Fprintf(w, "  thermals  max %s (slowdown at %s°C), slowed down in %d/%d readings\n",
		maxTemp, show(g.Limits.SlowdownTempC), throttled, len(g.Samples))

	switch {
	case g.Events == nil:
		fmt.Fprintf(w, "  xid       couldn't watch: %s\n", g.Unsupported["events"])
	case len(g.Events) == 0:
		fmt.Fprintln(w, "  xid       none")
	}
	if len(g.Unsupported) > 0 {
		noun := "fields"
		if len(g.Unsupported) == 1 {
			noun = "field"
		}
		fmt.Fprintf(w, "  n/a       %d %s not available on this GPU (see \"unsupported\" in the report)\n", len(g.Unsupported), noun)
	}
	for _, e := range g.Events {
		fmt.Fprintf(w, "  xid       %d at %d ms: %s\n", e.XID, e.TMs, e.Description)
	}
}

// show prints an optional value, or "n/a" when the GPU couldn't report it.
func show[T any](p *T) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprint(*p)
}
