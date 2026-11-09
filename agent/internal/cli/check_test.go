package cli

import (
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/probe"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/sign"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry/fake"
)

// go test ./internal/cli -update rewrites the golden file after an intended change.
var update = flag.Bool("update", false, "rewrite golden files")

// pin fixes time, report ID, sleeping and the signing key so check output is
// identical every run. The key lives in a temp ASSAY_HOME, never in ~/.assay.
func pin(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ASSAY_HOME", home)
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 42
	if err := sign.SaveKeyPair(home, ed25519.NewKeyFromSeed(seed), false); err != nil {
		t.Fatal(err)
	}
	oldNow, oldID, oldSleep := now, newID, sleep
	now = func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }
	newID = func() string { return "00000000000000000000000000000001" }
	sleep = func(time.Duration) {}
	t.Cleanup(func() { now, newID, sleep = oldNow, oldID, oldSleep })
}

func TestCheckFakeHealthyMatchesGolden(t *testing.T) {
	pin(t)
	out := filepath.Join(t.TempDir(), "report.json")
	code, _, stderr := run("check", "--backend=fake", "--samples=5", "--probes=none", "--out", out)
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "check_fake_healthy.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/cli -update)", err)
	}
	if string(got) != string(want) {
		t.Fatalf("report differs from %s.\nIf the change is intended, run: go test ./internal/cli -update", golden)
	}
}

func TestCheckEveryScenarioWritesAValidReport(t *testing.T) {
	pin(t)
	for _, s := range []string{"healthy", "hbm-degrading", "thermal-throttle", "xid-storm", "pcie-flaky"} {
		code, stdout, stderr := run("check", "--backend=fake", "--scenario="+s, "--out=-")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", s, code, stderr)
		}
		var r report.Report
		if err := json.Unmarshal([]byte(stdout), &r); err != nil {
			t.Fatalf("%s: output is not a report: %v", s, err)
		}
		if r.SchemaVersion != 1 || r.Agent.Scenario != s || len(r.GPUs) != 1 || len(r.GPUs[0].Samples) < 20 {
			t.Fatalf("%s: unexpected report header/shape: %+v", s, r.Agent)
		}
		if _, err := sign.Verify(r, nil); err != nil {
			t.Fatalf("%s: written report doesn't verify: %v", s, err)
		}
	}
}

func TestCheckSummaryNamesTheProblem(t *testing.T) {
	pin(t)
	_, _, stderr := run("check", "--backend=fake", "--scenario=xid-storm", "--probes=none", "--out=-")
	if !strings.Contains(stderr, "48 at 1750 ms: Double-bit ECC error") {
		t.Fatalf("summary should list XID 48, got:\n%s", stderr)
	}
}

func TestCheckRejectsBadInput(t *testing.T) {
	pin(t)
	cases := map[string][]string{
		"unknown backend":  {"check", "--backend=quantum"},
		"unknown scenario": {"check", "--backend=fake", "--scenario=on-fire"},
		"unknown flag":     {"check", "--colour=blue"},
		"unknown probe":    {"check", "--backend=fake", "--probes=gemm,warp-drive"},
		"zero interval":    {"check", "--backend=fake", "--interval=0s"},
	}
	for name, args := range cases {
		if code, _, _ := run(args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestCheckWithoutKeyTellsYouToRunKeygen(t *testing.T) {
	t.Setenv("ASSAY_HOME", t.TempDir()) // empty: no key
	code, _, stderr := run("check", "--backend=fake")
	if code != 1 || !strings.Contains(stderr, "assay keygen") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestCheckNVMLWithoutDriverExplainsWhy(t *testing.T) {
	pin(t)
	// This Mac / CI box has no NVIDIA driver, so NVML can't start.
	code, _, stderr := run("check", "--backend=nvml")
	if code != 1 || !strings.Contains(stderr, "libnvidia-ml") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestCheckProviderOverride(t *testing.T) {
	pin(t)
	_, stdout, _ := run("check", "--backend=fake", "--provider=lab", "--samples=1", "--out=-")
	if !strings.Contains(stdout, `"provider": "lab"`) {
		t.Fatal("provider override not written to report")
	}
}

// probeRun runs a fake check with probes, in real time with 1 ms readings so the
// background sampler sees the fake GPU under load. Takes well under a second.
func probeRun(t *testing.T, scenario string) report.Report {
	t.Helper()
	pin(t)
	sleep = time.Sleep
	code, stdout, stderr := run("check", "--backend=fake", "--scenario="+scenario, "--interval=1ms", "--samples=5", "--out=-")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	r, err := report.Parse([]byte(stdout))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sign.Verify(r, nil); err != nil {
		t.Fatalf("report with probes doesn't verify: %v", err)
	}
	return r
}

func TestCheckRunsProbesInOrderWithTelemetry(t *testing.T) {
	r := probeRun(t, "healthy")
	g := r.GPUs[0]
	var names []string
	for _, p := range g.Probes {
		names = append(names, p.Name)
		if p.Status != report.ProbeOK {
			t.Errorf("%s: %s %s", p.Name, p.Status, p.Reason)
		}
		if p.During == nil || p.During.Readings == 0 {
			t.Errorf("%s: no readings taken while it ran", p.Name)
		}
		if p.EndMs < p.StartMs || p.StartMs < 5 {
			t.Errorf("%s: bad timing %d..%d (must start after the 5 baseline readings)", p.Name, p.StartMs, p.EndMs)
		}
	}
	if strings.Join(names, ",") != "gemm,membw,memtest,soak" {
		t.Errorf("probe order = %v", names)
	}
	if len(r.HostProbes) != 1 || r.HostProbes[0].Status != report.ProbeSkipped {
		t.Errorf("host probes = %+v, want allreduce skipped (1 GPU)", r.HostProbes)
	}
	// The lesson from the real Colab T4: idle link is Gen1, under load it's Gen3.
	gen, width := maxLinkUnderLoad(g.Samples)
	if gen == nil || *gen != 3 || *width != 16 || *g.Samples[0].PCIeGen != 1 {
		t.Errorf("want Gen1 idle and Gen3 x16 under load")
	}
}

func TestCheckSoakShowsThermalSlowdown(t *testing.T) {
	r := probeRun(t, "thermal-throttle")
	soak := r.GPUs[0].Probes[3]
	if soak.Name != "soak" || soak.During == nil || soak.During.SlowdownReadings == 0 || soak.During.FirstSlowdownMs == nil {
		t.Fatalf("soak = %+v, during %+v", soak, soak.During)
	}
}

func TestCheckSelectedProbesOnly(t *testing.T) {
	pin(t)
	_, stdout, _ := run("check", "--backend=fake", "--probes=memtest", "--samples=1", "--out=-")
	r, _ := report.Parse([]byte(stdout))
	if len(r.GPUs[0].Probes) != 1 || r.GPUs[0].Probes[0].Name != "memtest" || len(r.HostProbes) != 0 {
		t.Fatalf("probes = %+v / host %+v", r.GPUs[0].Probes, r.HostProbes)
	}
}

func TestBusyGPUSkipsProbes(t *testing.T) {
	b, _ := fake.New("healthy")
	b.SetLoad(true) // someone else's job is using the GPU
	sess, _ := telemetry.Open(b)
	sess.SampleAt(0)
	why := busyReason(sess)
	if !strings.Contains(why, "100% busy") {
		t.Fatalf("busy reason = %q", why)
	}
	host := skipAll(sess, probe.Plan(probe.Config{Probes: probe.Names, Seconds: time.Second, Soak: time.Second}), why)
	if g := sess.GPUs()[0]; len(g.Probes) != 4 || g.Probes[0].Status != report.ProbeSkipped || len(host) != 1 {
		t.Fatalf("probes = %+v", g.Probes)
	}
}
