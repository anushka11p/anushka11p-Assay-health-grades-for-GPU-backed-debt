package telemetry

import (
	"fmt"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
)

// Options controls how long and how often telemetry is sampled.
type Options struct {
	Samples  int           // how many readings to take per GPU (at least 1)
	Interval time.Duration // time between readings
	// Sleep waits between readings. Defaults to time.Sleep; tests pass a no-op.
	Sleep func(time.Duration)
}

// Collect reads every GPU the backend can see: slow-changing facts once, then
// Samples readings of the fast-changing values, Interval apart. Driver error
// events (XIDs) are polled at every reading and stamped with that reading's time.
//
// A field the GPU can't report is not an error; it lands in GPU.Unsupported.
// Only failing to find or identify a GPU is an error.
func Collect(b Backend, opt Options) ([]report.GPU, error) {
	if opt.Samples < 1 {
		return nil, fmt.Errorf("samples must be at least 1, got %d", opt.Samples)
	}
	if opt.Sleep == nil {
		opt.Sleep = time.Sleep
	}

	count, err := b.DeviceCount()
	if err != nil {
		return nil, fmt.Errorf("counting GPUs: %w", err)
	}
	if count == 0 {
		return nil, fmt.Errorf("no GPUs found")
	}

	devices := make([]Device, count)
	gpus := make([]report.GPU, count)
	for i := range count {
		d, err := b.Device(i)
		if err != nil {
			return nil, fmt.Errorf("opening GPU %d: %w", i, err)
		}
		id, u, err := d.Identity()
		if err != nil {
			return nil, fmt.Errorf("identifying GPU %d: %w", i, err)
		}
		g := report.GPU{Index: i, Identity: id, Samples: []report.Sample{}, Events: []report.XIDEvent{}}
		g.Unsupported = merge(g.Unsupported, u)
		g.Memory, u = d.Memory()
		g.Unsupported = merge(g.Unsupported, u)
		g.Interconnect, u = d.Interconnect()
		g.Unsupported = merge(g.Unsupported, u)
		g.Limits, u = d.Limits()
		g.Unsupported = merge(g.Unsupported, u)
		devices[i], gpus[i] = d, g
	}

	for s := range opt.Samples {
		// Scheduled time of this reading, in ms since collection started.
		tms := int64(s) * opt.Interval.Milliseconds()
		for i, d := range devices {
			sample, u := d.Sample()
			sample.TMs = tms
			if sample.ThrottleReasons == nil {
				sample.ThrottleReasons = []string{} // JSON [] not null: "checked, none"
			}
			gpus[i].Samples = append(gpus[i].Samples, sample)
			gpus[i].Unsupported = merge(gpus[i].Unsupported, u)

			events, u := d.PollEvents()
			gpus[i].Unsupported = merge(gpus[i].Unsupported, u)
			for _, e := range events {
				e.TMs = tms
				gpus[i].Events = append(gpus[i].Events, e)
			}
		}
		if s < opt.Samples-1 {
			opt.Sleep(opt.Interval)
		}
	}
	// If events couldn't be watched, report null ("couldn't check"), not [] ("none").
	for i := range gpus {
		if _, ok := gpus[i].Unsupported["events"]; ok {
			gpus[i].Events = nil
		}
	}
	return gpus, nil
}
