// Package probe plans and runs the CUDA stress tests (the programs in probes/).
//
// Each probe is a separate program that prints one line of JSON. Running probes as
// separate processes keeps the agent safe: if a probe crashes or hangs, only that
// probe fails, and a timeout kills it.
package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
)

// Names lists every probe in the order they run. soak runs last on each GPU
// because it heats the GPU up; allreduce runs once for the whole machine.
var Names = []string{"gemm", "membw", "memtest", "soak", "allreduce"}

// Spec is one planned probe run.
type Spec struct {
	Name    string
	Args    []string
	Timeout time.Duration
	PerGPU  bool // true: run once per GPU with --device; false: once for the machine
}

// Runner runs one probe. device is the GPU index, or -1 for machine-wide probes.
// It fills Name, Status, Reason and Result; the caller fills the timing fields.
type Runner interface {
	Run(ctx context.Context, spec Spec, device int) report.ProbeResult
}

// Config says which probes to run and for how long.
type Config struct {
	Probes    []string      // subset of Names
	Seconds   time.Duration // measuring time for gemm (per precision), membw and allreduce
	Soak      time.Duration // soak length
	MemtestMB int           // 0 = memtest's default (75% of free memory)
}

// ParseList turns "all", "none" or "gemm,soak" into probe names.
func ParseList(s string) ([]string, error) {
	switch strings.TrimSpace(s) {
	case "all":
		return append([]string(nil), Names...), nil
	case "none", "":
		return nil, nil
	}
	var out []string
	for _, name := range strings.Split(s, ",") {
		name = strings.TrimSpace(name)
		if !contains(Names, name) {
			return nil, fmt.Errorf("unknown probe %q (choose from %s, or all / none)", name, strings.Join(Names, ", "))
		}
		if !contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// Plan returns the probes to run, in the standard order, with arguments and timeouts.
func Plan(cfg Config) []Spec {
	secs := strconv.Itoa(max(1, int(cfg.Seconds.Seconds())))
	soak := max(1, int(cfg.Soak.Seconds()))
	window := min(10, soak)

	var specs []Spec
	for _, name := range Names {
		if !contains(cfg.Probes, name) {
			continue
		}
		switch name {
		case "gemm":
			specs = append(specs, Spec{name, []string{"--seconds", secs}, 2*cfg.Seconds + time.Minute, true})
		case "membw":
			specs = append(specs, Spec{name, []string{"--seconds", secs}, cfg.Seconds + time.Minute, true})
		case "memtest":
			var args []string
			if cfg.MemtestMB > 0 {
				args = []string{"--mb", strconv.Itoa(cfg.MemtestMB)}
			}
			specs = append(specs, Spec{name, args, 5 * time.Minute, true})
		case "soak":
			args := []string{"--seconds", strconv.Itoa(soak), "--window", strconv.Itoa(window)}
			specs = append(specs, Spec{name, args, cfg.Soak + 2*time.Minute, true})
		case "allreduce":
			specs = append(specs, Spec{name, []string{"--seconds", secs}, cfg.Seconds + 2*time.Minute, false})
		}
	}
	return specs
}

// ExecRunner runs real probe binaries from Dir (e.g. probes/bin).
type ExecRunner struct{ Dir string }

func (e ExecRunner) Run(ctx context.Context, spec Spec, device int) report.ProbeResult {
	path := filepath.Join(e.Dir, spec.Name)
	if _, err := os.Stat(path); err != nil {
		return report.ProbeResult{Name: spec.Name, Status: report.ProbeSkipped,
			Reason: fmt.Sprintf("probe binary not found at %s (build with: make -C probes)", path)}
	}
	args := spec.Args
	if spec.PerGPU {
		args = append([]string{"--device", strconv.Itoa(device)}, args...)
	}

	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// After a timeout kill, don't wait forever for output: a leftover child
	// process could keep the pipes open indefinitely.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return report.ProbeResult{Name: spec.Name, Status: report.ProbeTimeout,
			Reason: fmt.Sprintf("did not finish within %s and was stopped", spec.Timeout)}
	}
	return Interpret(spec.Name, stdout.Bytes(), stderr.Bytes(), err)
}

// Interpret turns a finished probe's output into a result. It is separate from
// ExecRunner so every outcome can be tested without running a GPU program.
func Interpret(name string, stdout, stderr []byte, runErr error) report.ProbeResult {
	res := report.ProbeResult{Name: name}
	out, perr := parseLastLine(stdout)
	switch {
	case perr != nil:
		res.Status = report.ProbeFailed
		res.Reason = "no JSON result: " + perr.Error()
		if runErr != nil {
			res.Reason += "; " + runErr.Error()
		}
		if tail := lastLine(stderr); tail != "" {
			res.Reason += "; stderr: " + tail
		}
	case out["error"] != nil:
		res.Status = report.ProbeFailed
		res.Reason = fmt.Sprint(out["error"])
	case out["skipped"] != nil:
		res.Status = report.ProbeSkipped
		res.Reason = fmt.Sprint(out["skipped"])
	case runErr != nil:
		res.Status = report.ProbeFailed
		res.Reason = "exited with an error after printing a result: " + runErr.Error()
	default:
		delete(out, "probe") // already in Name
		if _, err := report.Canonical(out); err != nil {
			res.Status = report.ProbeFailed
			res.Reason = "result can't be signed: " + err.Error()
			return res
		}
		res.Status = report.ProbeOK
		res.Result = out
	}
	return res
}

// parseLastLine decodes the last non-empty stdout line as a JSON object,
// keeping numbers exact.
func parseLastLine(stdout []byte) (map[string]any, error) {
	line := lastLine(stdout)
	if line == "" {
		return nil, errors.New("probe printed nothing")
	}
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("last line is not a JSON object: %q", truncate(line, 200))
	}
	return out, nil
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
