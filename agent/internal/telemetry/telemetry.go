// Package telemetry reads GPU health data through a Backend and collects it into report.GPU records.
//
// A Backend is anything that can answer questions about GPUs. The real one ("nvml")
// talks to NVIDIA's driver; the fake one invents realistic data so everything can be
// built and tested on a laptop without a GPU. The rest of Assay only sees this interface.
package telemetry

import "github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"

// Backend is one source of GPU telemetry.
type Backend interface {
	// Name is "nvml" or "fake"; it is written into the report.
	Name() string
	// Host describes the machine and driver.
	Host() (report.Host, error)
	// DeviceCount is the number of GPUs visible.
	DeviceCount() (int, error)
	// Device returns GPU number i (0-based).
	Device(i int) (Device, error)
	// Close releases the backend (e.g. shuts NVML down).
	Close() error
}

// Device is one GPU. Slow-changing facts (identity, memory health, links, limits)
// are read once; fast-changing values come from Sample, called repeatedly.
//
// Methods that can partially fail return what they could read, plus an
// Unsupported map of field name -> reason for what they couldn't.
type Device interface {
	Identity() (report.Identity, error)
	Memory() (report.MemoryHealth, Unsupported)
	Interconnect() (report.Interconnect, Unsupported)
	Limits() (report.Limits, Unsupported)
	// Sample reads temperature, power, clocks, utilisation and throttle reasons right now.
	Sample() (report.Sample, Unsupported)
	// PollEvents returns driver error events (XIDs) seen since the last call, without blocking.
	PollEvents() []report.XIDEvent
}

// Unsupported maps a report field name to why it could not be read.
type Unsupported map[string]string

// merge copies src into dst, creating dst if needed.
func merge(dst map[string]string, src Unsupported) map[string]string {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]string{}
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
