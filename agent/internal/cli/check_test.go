package cli

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
)

// go test ./internal/cli -update rewrites the golden file after an intended change.
var update = flag.Bool("update", false, "rewrite golden files")

// pin fixes time, report ID and sleeping so check output is identical every run.
func pin(t *testing.T) {
	t.Helper()
	oldNow, oldID, oldSleep := now, newID, sleep
	now = func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC) }
	newID = func() string { return "00000000000000000000000000000001" }
	sleep = func(time.Duration) {}
	t.Cleanup(func() { now, newID, sleep = oldNow, oldID, oldSleep })
}

func TestCheckFakeHealthyMatchesGolden(t *testing.T) {
	pin(t)
	out := filepath.Join(t.TempDir(), "report.json")
	code, _, stderr := run("check", "--backend=fake", "--samples=5", "--out", out)
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
		if r.SchemaVersion != 1 || r.Agent.Scenario != s || len(r.GPUs) != 1 || len(r.GPUs[0].Samples) != 20 {
			t.Fatalf("%s: unexpected report header/shape: %+v", s, r.Agent)
		}
		if _, err := report.Canonical(r); err != nil {
			t.Fatalf("%s: written report is not canonical: %v", s, err)
		}
	}
}

func TestCheckSummaryNamesTheProblem(t *testing.T) {
	pin(t)
	_, _, stderr := run("check", "--backend=fake", "--scenario=xid-storm", "--out=-")
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
	}
	for name, args := range cases {
		if code, _, _ := run(args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestCheckNVMLNotBuiltYet(t *testing.T) {
	code, _, stderr := run("check", "--backend=nvml")
	if code != 1 || !strings.Contains(stderr, "isn't built yet") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}
