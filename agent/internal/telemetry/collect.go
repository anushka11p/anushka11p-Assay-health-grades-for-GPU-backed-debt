package telemetry

import (
	"fmt"
	"sync"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
)

// Session holds every GPU the backend can see while a check is running.
// Static facts are read once by Open; readings are added by SampleAt, which is
// safe to call from a background goroutine while probes run.
//
// A field the GPU can't report is not an error; it lands in GPU.Unsupported.
// Only failing to find or identify a GPU is an error.
type Session struct {
	mu      sync.Mutex
	devices []Device
	gpus    []report.GPU
}

// Open reads identity, memory health, interconnect and limits for every GPU.
func Open(b Backend) (*Session, error) {
	count, err := b.DeviceCount()
	if err != nil {
		return nil, fmt.Errorf("counting GPUs: %w", err)
	}
	if count == 0 {
		return nil, fmt.Errorf("no GPUs found")
	}
	s := &Session{devices: make([]Device, count), gpus: make([]report.GPU, count)}
	for i := range count {
		d, err := b.Device(i)
		if err != nil {
			return nil, fmt.Errorf("opening GPU %d: %w", i, err)
		}
		id, u, err := d.Identity()
		if err != nil {
			return nil, fmt.Errorf("identifying GPU %d: %w", i, err)
		}
		g := report.GPU{
			Index:    i,
			Identity: id,
			Samples:  []report.Sample{},
			Events:   []report.XIDEvent{},
			Probes:   []report.ProbeResult{},
		}
		g.Unsupported = merge(g.Unsupported, u)
		g.Memory, u = d.Memory()
		g.Unsupported = merge(g.Unsupported, u)
		g.Interconnect, u = d.Interconnect()
		g.Unsupported = merge(g.Unsupported, u)
		g.Limits, u = d.Limits()
		g.Unsupported = merge(g.Unsupported, u)
		s.devices[i], s.gpus[i] = d, g
	}
	return s, nil
}

// Count is the number of GPUs in the session.
func (s *Session) Count() int { return len(s.gpus) }

// SampleAt takes one reading of every GPU and polls driver events, stamping
// both with tms (milliseconds since the check started).
func (s *Session) SampleAt(tms int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.devices {
		sample, u := d.Sample()
		sample.TMs = tms
		if sample.ThrottleReasons == nil {
			sample.ThrottleReasons = []string{} // JSON [] not null: "checked, none"
		}
		s.gpus[i].Samples = append(s.gpus[i].Samples, sample)
		s.gpus[i].Unsupported = merge(s.gpus[i].Unsupported, u)

		events, u := d.PollEvents()
		s.gpus[i].Unsupported = merge(s.gpus[i].Unsupported, u)
		for _, e := range events {
			e.TMs = tms
			s.gpus[i].Events = append(s.gpus[i].Events, e)
		}
	}
}

// Latest returns GPU i's most recent reading (ok=false if none yet).
func (s *Session) Latest(i int) (report.Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	samples := s.gpus[i].Samples
	if len(samples) == 0 {
		return report.Sample{}, false
	}
	return samples[len(samples)-1], true
}

// AddProbe records a stress-test result for GPU i.
func (s *Session) AddProbe(i int, r report.ProbeResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gpus[i].Probes = append(s.gpus[i].Probes, r)
}

// GPUs returns the finished records. If events couldn't be watched on a GPU,
// its events are null ("couldn't check"), not [] ("none").
func (s *Session) GPUs() []report.GPU {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]report.GPU, len(s.gpus))
	copy(out, s.gpus)
	for i := range out {
		if _, ok := out[i].Unsupported["events"]; ok {
			out[i].Events = nil
		}
	}
	return out
}

// Options controls a fixed-length, telemetry-only collection.
type Options struct {
	Samples  int           // how many readings to take per GPU (at least 1)
	Interval time.Duration // time between readings
	// Sleep waits between readings. Defaults to time.Sleep; tests pass a no-op.
	Sleep func(time.Duration)
}

// Collect opens every GPU and takes Samples readings, Interval apart, with no
// probes. Reading times are the scheduled times (0, Interval, 2*Interval, ...).
func Collect(b Backend, opt Options) ([]report.GPU, error) {
	if opt.Samples < 1 {
		return nil, fmt.Errorf("samples must be at least 1, got %d", opt.Samples)
	}
	if opt.Sleep == nil {
		opt.Sleep = time.Sleep
	}
	s, err := Open(b)
	if err != nil {
		return nil, err
	}
	for n := range opt.Samples {
		s.SampleAt(int64(n) * opt.Interval.Milliseconds())
		if n < opt.Samples-1 {
			opt.Sleep(opt.Interval)
		}
	}
	return s.GPUs(), nil
}
