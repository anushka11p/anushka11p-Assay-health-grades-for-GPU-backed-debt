package telemetry

import "fmt"

// throttleBits are NVML's "clocks event reasons": one bit per reason the GPU is
// running below its maximum clock. Values come from nvml.h and are stable ABI.
var throttleBits = []struct {
	bit  uint64
	name string
}{
	{0x1, "gpu_idle"},
	{0x2, "applications_clocks_setting"},
	{0x4, "sw_power_cap"},
	{0x8, "hw_slowdown"},
	{0x10, "sync_boost"},
	{0x20, "sw_thermal_slowdown"},
	{0x40, "hw_thermal_slowdown"},
	{0x80, "hw_power_brake_slowdown"},
	{0x100, "display_clock_setting"},
}

// ThrottleReasons turns an NVML reasons bitmask into names, e.g. 0x21 ->
// ["gpu_idle", "sw_thermal_slowdown"]. Unknown bits are kept as "unknown_0x...".
func ThrottleReasons(mask uint64) []string {
	names := []string{}
	for _, b := range throttleBits {
		if mask&b.bit != 0 {
			names = append(names, b.name)
			mask &^= b.bit
		}
	}
	for bit := uint64(1); mask != 0; bit <<= 1 {
		if mask&bit != 0 {
			names = append(names, fmt.Sprintf("unknown_%#x", bit))
			mask &^= bit
		}
	}
	return names
}

// IsSlowdown reports whether a reason means the GPU is being held back by a
// problem (heat, hardware), as opposed to idling, configuration, or running at
// its power limit. Hitting the power cap under full load is normal: a 70 W T4
// does it on every heavy matrix multiply.
func IsSlowdown(reason string) bool {
	switch reason {
	case "gpu_idle", "applications_clocks_setting", "display_clock_setting", "sync_boost", "sw_power_cap":
		return false
	}
	return true
}

// DetectProvider guesses the cloud notebook provider from environment variables.
// getenv is os.Getenv in real use; tests pass a map lookup.
func DetectProvider(getenv func(string) string) string {
	switch {
	case getenv("COLAB_RELEASE_TAG") != "" || getenv("COLAB_GPU") != "":
		return "colab"
	case getenv("KAGGLE_KERNEL_RUN_TYPE") != "" || getenv("KAGGLE_URL_BASE") != "":
		return "kaggle"
	}
	return "unknown"
}
