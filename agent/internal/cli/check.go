package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry/fake"
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
	if err := fs.Parse(args); err != nil {
		return 2
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
		fmt.Fprintln(stderr, "assay check: the nvml (real GPU) backend isn't built yet; use --backend=fake")
		return 1
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
	// Canonicalising now catches anything unsignable (like a float) before we write.
	if _, err := report.Canonical(r); err != nil {
		fmt.Fprintf(stderr, "assay check: report is not canonical: %v\n", err)
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
	fmt.Fprintf(stderr, "assay check: wrote report %s to %s\n", r.ReportID, where)
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
		if len(s.ThrottleReasons) > 0 {
			throttled++
		}
	}
	maxTemp := "n/a"
	if maxT >= 0 {
		maxTemp = fmt.Sprintf("%d°C", maxT)
	}
	fmt.Fprintf(w, "  thermals  max %s (slowdown at %s°C), throttled in %d/%d readings\n",
		maxTemp, show(g.Limits.SlowdownTempC), throttled, len(g.Samples))

	if len(g.Events) == 0 {
		fmt.Fprintln(w, "  xid       none")
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
