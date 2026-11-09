package cli

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/probe"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
)

// clock is the wall clock used to time probes. Unlike `now`, tests don't pin it.
var clock = time.Now

// busyUtilPct: above this GPU utilisation at baseline, something else is using
// the GPU and probes would both disturb it and measure the wrong thing.
const busyUtilPct = 10

// busyReason says why the GPUs are too busy to probe, or "" if they're idle.
func busyReason(sess *telemetry.Session) string {
	for i := range sess.Count() {
		s, ok := sess.Latest(i)
		if ok && s.UtilGPUPct != nil && *s.UtilGPUPct > busyUtilPct {
			return fmt.Sprintf("GPU %d was %d%% busy before probing: another workload is running, so probes were skipped to avoid disturbing it and measuring the wrong thing", i, *s.UtilGPUPct)
		}
	}
	return ""
}

// runProbes runs the planned probes while a background goroutine keeps sampling
// every GPU each interval. Per-GPU results go into the session; machine-wide
// results are returned. offsetMs is where the probe phase starts on the
// report's timeline (right after the baseline readings).
func runProbes(sess *telemetry.Session, runner probe.Runner, specs []probe.Spec, interval time.Duration, offsetMs int64, stderr io.Writer) []report.ProbeResult {
	start := clock()
	elapsed := func() int64 { return offsetMs + clock().Sub(start).Milliseconds() }

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				sess.SampleAt(elapsed())
			}
		}
	}()

	run := func(spec probe.Spec, device int) report.ProbeResult {
		where := "machine"
		if device >= 0 {
			where = fmt.Sprintf("GPU %d", device)
		}
		fmt.Fprintf(stderr, "assay check: %s: running %s ...", where, spec.Name)
		t0 := elapsed()
		r := runner.Run(context.Background(), spec, device)
		r.StartMs, r.EndMs = t0, elapsed()
		fmt.Fprintf(stderr, " %s (%.1fs)\n", r.Status, float64(r.EndMs-r.StartMs)/1000)
		return r
	}

	for i := range sess.Count() {
		for _, spec := range specs {
			if spec.PerGPU {
				sess.AddProbe(i, run(spec, i))
			}
		}
	}
	host := []report.ProbeResult{}
	for _, spec := range specs {
		if !spec.PerGPU {
			host = append(host, run(spec, -1))
		}
	}

	close(stop)
	wg.Wait()
	sess.SampleAt(elapsed()) // one last reading after everything finished
	return host
}

// skipAll records every planned probe as skipped, with the same reason.
func skipAll(sess *telemetry.Session, specs []probe.Spec, reason string) []report.ProbeResult {
	host := []report.ProbeResult{}
	for _, spec := range specs {
		r := report.ProbeResult{Name: spec.Name, Status: report.ProbeSkipped, Reason: reason}
		if !spec.PerGPU {
			host = append(host, r)
			continue
		}
		for i := range sess.Count() {
			sess.AddProbe(i, r)
		}
	}
	return host
}

// attachDuring adds a summary of each GPU's readings during each of its probes.
func attachDuring(gpus []report.GPU) {
	for gi := range gpus {
		g := &gpus[gi]
		for pi := range g.Probes {
			p := &g.Probes[pi]
			if p.Status == report.ProbeOK {
				p.During = telemetry.Summarize(g.Samples, p.StartMs, p.EndMs)
			}
		}
	}
}
