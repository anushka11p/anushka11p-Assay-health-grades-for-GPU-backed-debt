package telemetry_test

import (
	"testing"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
)

func reading(tms, temp, sm, gen int64, reasons ...string) report.Sample {
	if reasons == nil {
		reasons = []string{}
	}
	return report.Sample{TMs: tms, TempC: &temp, SMClockMHz: &sm, PCIeGen: &gen, ThrottleReasons: reasons}
}

func TestSummarizeOnlyUsesReadingsInsideTheProbe(t *testing.T) {
	samples := []report.Sample{
		reading(0, 40, 300, 1, "gpu_idle"), // before the probe: idle, slow link
		reading(1000, 70, 1590, 3, "sw_power_cap"),
		reading(2000, 88, 1300, 3, "sw_thermal_slowdown"),
		reading(3000, 91, 585, 3, "sw_thermal_slowdown", "hw_thermal_slowdown"),
		reading(9000, 50, 300, 1, "gpu_idle"), // after the probe
	}
	got := telemetry.Summarize(samples, 1000, 3000)
	if got.Readings != 3 || *got.MaxTempC != 91 || *got.MinSMClockMHz != 585 || *got.MaxPCIeGen != 3 {
		t.Fatalf("got %+v", got)
	}
	// Power cap doesn't count; the two thermal readings do. First one is 1000 ms in.
	if got.SlowdownReadings != 2 || *got.FirstSlowdownMs != 1000 {
		t.Fatalf("slowdowns = %d, first at %v", got.SlowdownReadings, *got.FirstSlowdownMs)
	}
	if got.MaxPowerMW != nil {
		t.Error("no power readings: must stay null, not 0")
	}
}

func TestSummarizeEmptyWindowIsNil(t *testing.T) {
	if telemetry.Summarize([]report.Sample{reading(0, 40, 300, 1)}, 500, 900) != nil {
		t.Fatal("want nil when no readings fall inside the probe")
	}
}
