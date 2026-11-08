package telemetry_test

import (
	"testing"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry/fake"
)

func TestCollectTakesSamplesIntervalApart(t *testing.T) {
	b, _ := fake.New("healthy")
	var slept []time.Duration
	gpus, err := telemetry.Collect(b, telemetry.Options{
		Samples:  4,
		Interval: 250 * time.Millisecond,
		Sleep:    func(d time.Duration) { slept = append(slept, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	var times []int64
	for _, s := range gpus[0].Samples {
		times = append(times, s.TMs)
	}
	if len(times) != 4 || times[0] != 0 || times[3] != 750 {
		t.Fatalf("sample times = %v, want [0 250 500 750]", times)
	}
	// 4 readings need only 3 waits between them.
	if len(slept) != 3 {
		t.Fatalf("slept %d times, want 3", len(slept))
	}
}

func TestCollectRejectsZeroSamples(t *testing.T) {
	b, _ := fake.New("healthy")
	if _, err := telemetry.Collect(b, telemetry.Options{Samples: 0}); err == nil {
		t.Fatal("want error for 0 samples")
	}
}

func TestCollectUsesEmptyListsNotNull(t *testing.T) {
	b, _ := fake.New("healthy")
	gpus, _ := telemetry.Collect(b, telemetry.Options{Samples: 1, Sleep: func(time.Duration) {}})
	g := gpus[0]
	if g.Events == nil || g.Samples[0].ThrottleReasons == nil {
		t.Fatal("events and throttle reasons must be [] (checked, none), not null")
	}
}

func TestXIDDescriptionFallsBackForUnknownCodes(t *testing.T) {
	if telemetry.XIDDescription(79) != "GPU has fallen off the bus" {
		t.Error("wrong description for XID 79")
	}
	if telemetry.XIDDescription(9999) == "" {
		t.Error("unknown XIDs still need a description")
	}
}
