package fake_test

import (
	"slices"
	"testing"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry/fake"
)

// collect runs a scenario for 20 readings with no real waiting.
func collect(t *testing.T, scenario string) report.GPU {
	t.Helper()
	b, err := fake.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
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
		if len(s.ThrottleReasons) > 0 {
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
	if *g.Interconnect.PCIeGenCurrent != *g.Interconnect.PCIeGenMax {
		t.Error("healthy GPU should run PCIe at max generation")
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
