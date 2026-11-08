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

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/sign"
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
		if _, err := sign.Verify(r, nil); err != nil {
			t.Fatalf("%s: written report doesn't verify: %v", s, err)
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
