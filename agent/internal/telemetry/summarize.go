package telemetry

import (
	"slices"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
)

// Summarize describes the readings taken between startMs and endMs (inclusive):
// peaks, the lowest clock, the best PCIe link seen, and slowdowns.
// Returns nil if there were no readings in that window.
func Summarize(samples []report.Sample, startMs, endMs int64) *report.ProbeTelemetry {
	t := &report.ProbeTelemetry{}
	for _, s := range samples {
		if s.TMs < startMs || s.TMs > endMs {
			continue
		}
		t.Readings++
		t.MaxTempC = maxPtr(t.MaxTempC, s.TempC)
		t.MaxPowerMW = maxPtr(t.MaxPowerMW, s.PowerMW)
		t.MaxPCIeGen = maxPtr(t.MaxPCIeGen, s.PCIeGen)
		t.MaxPCIeWidth = maxPtr(t.MaxPCIeWidth, s.PCIeWidth)
		if s.SMClockMHz != nil && (t.MinSMClockMHz == nil || *s.SMClockMHz < *t.MinSMClockMHz) {
			t.MinSMClockMHz = report.Ptr(*s.SMClockMHz)
		}
		if slices.ContainsFunc(s.ThrottleReasons, IsSlowdown) {
			t.SlowdownReadings++
			if t.FirstSlowdownMs == nil {
				t.FirstSlowdownMs = report.Ptr(s.TMs - startMs)
			}
		}
	}
	if t.Readings == 0 {
		return nil
	}
	return t
}

func maxPtr(cur, v *int64) *int64 {
	if v == nil {
		return cur
	}
	if cur == nil || *v > *cur {
		return report.Ptr(*v)
	}
	return cur
}
