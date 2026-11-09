package fake_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/probe"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry/fake"
)

// collect runs a scenario under load for 20 readings with no real waiting.
func collect(t *testing.T, scenario string) report.GPU {
	t.Helper()
	return collectWith(t, scenario, true)
}

func collectWith(t *testing.T, scenario string, loaded bool) report.GPU {
	t.Helper()
	b, err := fake.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	b.SetLoad(loaded)
	gpus, err := telemetry.Collect(b, telemetry.Options{Samples: 20, Interval: 250 * time.Millisecond, Sleep: func(time.Duration) {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 1 {
		t.Fatalf("got %d GPUs, want 1", len(gpus))
	}
	return gpus[0]
}

func maxTemp(g report.GPU) int64 {
	var m int64
	for _, s := range g.Samples {
		m = max(m, *s.TempC)
	}
	return m
}

func throttled(g report.GPU) int {
	n := 0
	for _, s := range g.Samples {
		if slices.ContainsFunc(s.ThrottleReasons, telemetry.IsSlowdown) {
			n++
		}
	}
	return n
}

func TestUnknownScenarioIsRejected(t *testing.T) {
	if _, err := fake.New("on-fire"); err == nil {
		t.Fatal("want error for unknown scenario")
	}
}

func TestHealthyHasNoProblems(t *testing.T) {
	g := collect(t, "healthy")
	if *g.Memory.Volatile.Uncorrected != 0 || g.Memory.RetiredPages.DoubleBit != 0 {
		t.Error("healthy GPU should have no uncorrected errors or retired pages")
	}
	if len(g.Events) != 0 {
		t.Errorf("healthy GPU has %d XID events", len(g.Events))
	}
	if n := throttled(g); n != 0 {
		t.Errorf("healthy GPU throttled in %d samples", n)
	}
	if *g.Samples[0].PCIeGen != int64(*g.Interconnect.PCIeGenMax) || *g.Samples[0].PCIeWidth != 16 {
		t.Error("healthy GPU under load should run PCIe at max generation and width")
	}
	if maxTemp(g) > 75 {
		t.Errorf("healthy max temp %d°C, want <= 75", maxTemp(g))
	}
}

func TestHBMDegradingShowsMemoryWear(t *testing.T) {
	g := collect(t, "hbm-degrading")
	if *g.Memory.Aggregate.Uncorrected == 0 {
		t.Error("want uncorrected ECC errors")
	}
	if g.Memory.RetiredPages.SingleBit+g.Memory.RetiredPages.DoubleBit == 0 || !g.Memory.RetiredPages.PendingRetirement {
		t.Error("want retired pages and a pending retirement")
	}
}

func TestThermalThrottleHeatsUpAndSlowsDown(t *testing.T) {
	g := collect(t, "thermal-throttle")
	if maxTemp(g) < 90 {
		t.Errorf("max temp %d°C, want >= 90", maxTemp(g))
	}
	last := g.Samples[len(g.Samples)-1]
	if !slices.Contains(last.ThrottleReasons, "hw_thermal_slowdown") {
		t.Errorf("last sample reasons %v, want hw_thermal_slowdown", last.ThrottleReasons)
	}
	if *last.SMClockMHz >= *g.Samples[0].SMClockMHz {
		t.Error("clocks should drop once throttling")
	}
}

func TestXIDStormReportsEventsWithTimes(t *testing.T) {
	g := collect(t, "xid-storm")
	if len(g.Events) != 5 {
		t.Fatalf("got %d events, want 5", len(g.Events))
	}
	first := g.Events[0]
	if first.XID != 13 || first.TMs != 500 || first.Description == "" {
		t.Errorf("first event = %+v, want XID 13 at 500 ms with a description", first)
	}
}

func TestPCIeFlakyShowsDegradedLink(t *testing.T) {
	g := collect(t, "pcie-flaky")
	ic := g.Interconnect
	if *ic.PCIeGenCurrent >= *ic.PCIeGenMax || *ic.PCIeWidthCurrent >= *ic.PCIeWidthMax {
		t.Error("want link below max generation and width")
	}
	if *ic.PCIeReplayCounter == 0 {
		t.Error("want PCIe replays")
	}
	if *g.Samples[0].PCIeGen != 1 || *g.Samples[0].PCIeWidth != 8 {
		t.Error("a flaky link stays degraded even under load")
	}
}

func TestIdleT4DropsItsLinkAndClocks(t *testing.T) {
	// Matches what the real Colab T4 reported on 8 Nov 2026.
	g := collectWith(t, "healthy", false)
	s := g.Samples[0]
	if *s.SMClockMHz != 300 || *s.UtilGPUPct != 0 || *s.PCIeGen != 1 || !slices.Equal(s.ThrottleReasons, []string{"gpu_idle"}) {
		t.Fatalf("idle sample = sm %d util %d gen %d reasons %v", *s.SMClockMHz, *s.UtilGPUPct, *s.PCIeGen, s.ThrottleReasons)
	}
	if throttled(g) != 0 {
		t.Error("idling is not a slowdown")
	}
}

func TestPretendProbesMatchTheScenario(t *testing.T) {
	run := func(scenario, name string) report.ProbeResult {
		b, _ := fake.New(scenario)
		b.SetProbeTiming(0, func(time.Duration) {})
		return b.Runner().Run(context.Background(), probe.Spec{Name: name}, 0)
	}
	if r := run("healthy", "memtest"); r.Status != report.ProbeOK || r.Result["mismatches"] != int64(0) {
		t.Errorf("healthy memtest = %+v", r)
	}
	if r := run("hbm-degrading", "memtest"); r.Result["mismatches"] != int64(3) {
		t.Errorf("hbm-degrading memtest = %+v", r)
	}
	if r := run("thermal-throttle", "soak"); r.Result["drop_pct"].(int64) < 50 {
		t.Errorf("thermal soak should sag badly, got %+v", r.Result)
	}
	if r := run("healthy", "allreduce"); r.Status != report.ProbeSkipped {
		t.Errorf("one fake GPU: allreduce should be skipped, got %s", r.Status)
	}
	for _, name := range probe.Names {
		if _, err := report.Canonical(run("healthy", name)); err != nil {
			t.Errorf("%s result not signable: %v", name, err)
		}
	}
}

func TestRunnerLoadsTheGPUOnlyWhileRunning(t *testing.T) {
	b, _ := fake.New("healthy")
	dev, _ := b.Device(0)
	var during report.Sample
	b.SetProbeTiming(0, func(time.Duration) { during, _ = dev.Sample() })
	b.Runner().Run(context.Background(), probe.Spec{Name: "gemm"}, 0)
	after, _ := dev.Sample()
	if *during.UtilGPUPct != 100 || *after.UtilGPUPct != 0 {
		t.Fatalf("util during %d, after %d; want 100 then 0", *during.UtilGPUPct, *after.UtilGPUPct)
	}
}

func TestEveryScenarioIsDeterministicAndCanonical(t *testing.T) {
	for _, s := range fake.Scenarios() {
		a, err := report.Canonical(collect(t, s))
		if err != nil {
			t.Fatalf("%s: %v", s, err) // also proves there are no floats
		}
		b, _ := report.Canonical(collect(t, s))
		if string(a) != string(b) {
			t.Errorf("%s: two runs gave different data", s)
		}
	}
}

func TestTuringMarksRowRemapUnsupported(t *testing.T) {
	g := collect(t, "healthy")
	if g.Memory.RowRemap != nil || g.Unsupported["row_remap"] == "" {
		t.Error("row_remap should be null with a reason in unsupported")
	}
}
