package telemetry_test

import (
	"slices"
	"testing"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
)

func TestThrottleReasons(t *testing.T) {
	cases := map[uint64][]string{
		0:      {},
		0x1:    {"gpu_idle"},
		0x60:   {"sw_thermal_slowdown", "hw_thermal_slowdown"},
		0x4000: {"unknown_0x4000"},
	}
	for mask, want := range cases {
		if got := telemetry.ThrottleReasons(mask); !slices.Equal(got, want) {
			t.Errorf("%#x: got %v, want %v", mask, got, want)
		}
	}
}

func TestIdleIsNotASlowdown(t *testing.T) {
	if telemetry.IsSlowdown("gpu_idle") || !telemetry.IsSlowdown("hw_thermal_slowdown") {
		t.Fatal("idle must not count as a slowdown; thermal must")
	}
}

func TestDetectProvider(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if p := telemetry.DetectProvider(env(map[string]string{"COLAB_RELEASE_TAG": "release-colab_2026"})); p != "colab" {
		t.Errorf("got %q, want colab", p)
	}
	if p := telemetry.DetectProvider(env(map[string]string{"KAGGLE_KERNEL_RUN_TYPE": "Interactive"})); p != "kaggle" {
		t.Errorf("got %q, want kaggle", p)
	}
	if p := telemetry.DetectProvider(env(nil)); p != "unknown" {
		t.Errorf("got %q, want unknown", p)
	}
}
