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

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/probe"
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
	samples := fs.Int("samples", 20, "idle baseline readings per GPU, taken before any probe")
	interval := fs.Duration("interval", 250*time.Millisecond, "time between readings")
	probeList := fs.String("probes", "all", "stress tests to run: all, none, or a list like gemm,soak ("+strings.Join(probe.Names, ", ")+")")
	probeDir := fs.String("probe-dir", filepath.Join("probes", "bin"), "where the built CUDA probes are (nvml backend)")
	probeSeconds := fs.Duration("probe-seconds", 10*time.Second, "measuring time for gemm (per precision), membw and allreduce")
	soak := fs.Duration("soak", 3*time.Minute, "how long the soak test runs")
	memtestMB := fs.Int("memtest-mb", 0, "memory for memtest in MiB (0 = 75% of free memory)")
	out := fs.String("out", "report.json", `where to write the report ("-" for stdout)`)
	provider := fs.String("provider", "", `override the detected provider (e.g. "colab", "kaggle", "lab")`)
	keyPath := fs.String("key", filepath.Join(keyDir(), sign.PrivateKeyFile), "private key to sign the report with")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	probes, err := probe.ParseList(*probeList)
	if err != nil {
		fmt.Fprintf(stderr, "assay check: %v\n", err)
		return 2
	}
	if *samples < 1 || *interval <= 0 {
		fmt.Fprintln(stderr, "assay check: --samples must be at least 1 and --interval positive")
		return 2
	}
	specs := probe.Plan(probe.Config{Probes: probes, Seconds: *probeSeconds, Soak: *soak, MemtestMB: *memtestMB})

	// Load the key first: no point measuring a GPU for minutes if we can't sign the result.
	priv, err := sign.LoadPrivateKey(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "assay check: can't load signing key: %v\n  run `assay keygen` first (or pass --key)\n", err)
		return 1
	}

	var backend telemetry.Backend
	var runner probe.Runner
	agent := report.Agent{Version: Version, Backend: *backendName}
	switch *backendName {
	case "fake":
		b, err := fake.New(*scenario)
		if err != nil {
			fmt.Fprintf(stderr, "assay check: %v\n", err)
			return 2
		}
		// Pretend probes last 8 readings (soak 24) so sampling sees the GPU under load.
		b.SetProbeTiming(8*(*interval), sleep)
		backend, runner = b, b.Runner()
		agent.Scenario = *scenario
	case "nvml":
		b, err := nvmlbackend.New()
		if err != nil {
			fmt.Fprintf(stderr, "assay check: %v\n", err)
			return 1
		}
		backend, runner = b, probe.ExecRunner{Dir: *probeDir}
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
	sess, err := telemetry.Open(backend)
	if err != nil {
		fmt.Fprintf(stderr, "assay check: %v\n", err)
		return 1
	}

	// 1. Idle baseline readings, at fixed times on the report's timeline.
	for n := range *samples {
		sess.SampleAt(int64(n) * interval.Milliseconds())
		if n < *samples-1 {
			sleep(*interval)
		}
	}
	baselineEnd := int64(*samples-1)*interval.Milliseconds() + interval.Milliseconds()

	// 2. Stress tests, unless something else is already using the GPU.
	hostProbes := []report.ProbeResult{}
	if len(specs) > 0 {
		if why := busyReason(sess); why != "" {
			fmt.Fprintf(stderr, "assay check: %s\n", why)
			hostProbes = skipAll(sess, specs, why)
		} else {
			hostProbes = runProbes(sess, runner, specs, *interval, baselineEnd, stderr)
		}
	}
	gpus := sess.GPUs()
	attachDuring(gpus)

	r := report.Report{
		SchemaVersion: report.SchemaVersion,
		ReportID:      newID(),
		CreatedAt:     now().Format(time.RFC3339),
		Agent:         agent,
		Host:          host,
		GPUs:          gpus,
		HostProbes:    hostProbes,
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
	for _, p := range hostProbes {
		fmt.Fprintf(stderr, "\nmachine\n")
		writeProbeLine(stderr, p)
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
	loadGen, loadWidth := maxLinkUnderLoad(g.Samples)
	underLoad := "not measured (no readings under load; run probes)"
	if loadGen != nil {
		underLoad = fmt.Sprintf("Gen%d x%d", *loadGen, *loadWidth)
	}
	fmt.Fprintf(w, "  pcie      under load: %s; max Gen%s x%s; %s replays (idle at start: Gen%s x%s, normal power saving)\n",
		underLoad, show(ic.PCIeGenMax), show(ic.PCIeWidthMax), show(ic.PCIeReplayCounter),
		show(ic.PCIeGenCurrent), show(ic.PCIeWidthCurrent))

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
	for _, p := range g.Probes {
		writeProbeLine(w, p)
	}
}

// headline lists the most telling numbers from each probe's result.
var headline = map[string][]string{
	"gemm":      {"fp16_gflops", "fp32_gflops"},
	"membw":     {"copy_mb_per_s", "triad_mb_per_s"},
	"memtest":   {"mismatches", "mib_tested"},
	"soak":      {"first_gflops", "last_gflops", "drop_pct"},
	"allreduce": {"busbw_mb_per_s", "gpus"},
}

// writeProbeLine prints one probe's outcome on one line.
func writeProbeLine(w io.Writer, p report.ProbeResult) {
	fmt.Fprintf(w, "  probe     %-9s %-8s", p.Name, p.Status)
	if p.Status != report.ProbeOK {
		fmt.Fprintf(w, "%s\n", p.Reason)
		return
	}
	for _, k := range headline[p.Name] {
		if v, ok := p.Result[k]; ok {
			fmt.Fprintf(w, "%s=%v ", k, v)
		}
	}
	if d := p.During; d != nil {
		fmt.Fprintf(w, "(max %s°C, %d/%d readings slowed down)", show(d.MaxTempC), d.SlowdownReadings, d.Readings)
	}
	fmt.Fprintln(w)
}

// maxLinkUnderLoad is the best PCIe link seen in readings with the GPU busy
// (>= 50% utilisation). Idle readings don't count: idle GPUs slow their link.
func maxLinkUnderLoad(samples []report.Sample) (gen, width *int64) {
	for _, s := range samples {
		if s.UtilGPUPct == nil || *s.UtilGPUPct < 50 || s.PCIeGen == nil || s.PCIeWidth == nil {
			continue
		}
		if gen == nil || *s.PCIeGen > *gen || (*s.PCIeGen == *gen && *s.PCIeWidth > *width) {
			gen, width = report.Ptr(*s.PCIeGen), report.Ptr(*s.PCIeWidth)
		}
	}
	return gen, width
}

// show prints an optional value, or "n/a" when the GPU couldn't report it.
func show[T any](p *T) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprint(*p)
}
