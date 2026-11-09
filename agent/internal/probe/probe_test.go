package probe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
)

func TestParseList(t *testing.T) {
	all, _ := ParseList("all")
	if !slices.Equal(all, Names) {
		t.Errorf("all = %v", all)
	}
	none, _ := ParseList("none")
	if len(none) != 0 {
		t.Errorf("none = %v", none)
	}
	some, _ := ParseList("soak, gemm,soak")
	if !slices.Equal(some, []string{"soak", "gemm"}) {
		t.Errorf("got %v", some)
	}
	if _, err := ParseList("gemm,warp-drive"); err == nil {
		t.Error("want error for unknown probe")
	}
}

func TestPlanKeepsStandardOrderAndSoakLast(t *testing.T) {
	specs := Plan(Config{Probes: []string{"allreduce", "soak", "gemm"}, Seconds: 10 * time.Second, Soak: 3 * time.Minute})
	var names []string
	for _, s := range specs {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"gemm", "soak", "allreduce"}) {
		t.Fatalf("order = %v", names)
	}
	soak := specs[1]
	if !slices.Equal(soak.Args, []string{"--seconds", "180", "--window", "10"}) || soak.Timeout != 5*time.Minute || !soak.PerGPU {
		t.Errorf("soak spec = %+v", soak)
	}
	if specs[2].PerGPU {
		t.Error("allreduce is machine-wide, not per GPU")
	}
}

func TestInterpret(t *testing.T) {
	cases := []struct {
		name, stdout, stderr string
		runErr               error
		status, reason       string
	}{
		{"ok", "warming up\n{\"probe\":\"gemm\",\"fp16_gflops\":41230}\n", "", nil, report.ProbeOK, ""},
		{"probe error", `{"probe":"gemm","error":"cudaMalloc failed: out of memory"}`, "", errors.New("exit status 1"), report.ProbeFailed, "out of memory"},
		{"skipped", `{"probe":"allreduce","skipped":"needs 2 or more GPUs, found 1"}`, "", nil, report.ProbeSkipped, "found 1"},
		{"crash", "", "Segmentation fault", errors.New("signal: segmentation fault"), report.ProbeFailed, "Segmentation fault"},
		{"float", `{"probe":"gemm","tflops":41.2}`, "", nil, report.ProbeFailed, "can't be signed"},
		{"garbage", "hello", "", nil, report.ProbeFailed, "not a JSON object"},
	}
	for _, c := range cases {
		r := Interpret("gemm", []byte(c.stdout), []byte(c.stderr), c.runErr)
		if r.Status != c.status || !strings.Contains(r.Reason, c.reason) {
			t.Errorf("%s: got %s / %q, want %s / ...%q...", c.name, r.Status, r.Reason, c.status, c.reason)
		}
	}
	ok := Interpret("gemm", []byte(`{"probe":"gemm","fp16_gflops":41230}`), nil, nil)
	if _, has := ok.Result["probe"]; has || ok.Result["fp16_gflops"] == nil {
		t.Errorf("result = %v", ok.Result)
	}
}

// script writes a tiny shell program that stands in for a CUDA probe binary.
func script(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestExecRunnerPassesDeviceAndArgs(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "gemm", `echo "{\"probe\":\"gemm\",\"argv\":\"$*\"}"`)
	r := ExecRunner{dir}.Run(context.Background(), Spec{Name: "gemm", Args: []string{"--seconds", "5"}, Timeout: 5 * time.Second, PerGPU: true}, 1)
	if r.Status != report.ProbeOK || r.Result["argv"] != "--device 1 --seconds 5" {
		t.Fatalf("got %+v", r)
	}
}

func TestExecRunnerMissingBinaryIsSkipped(t *testing.T) {
	r := ExecRunner{t.TempDir()}.Run(context.Background(), Spec{Name: "soak", Timeout: time.Second}, 0)
	if r.Status != report.ProbeSkipped || !strings.Contains(r.Reason, "make -C probes") {
		t.Fatalf("got %+v", r)
	}
}

func TestExecRunnerKillsHungProbe(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "soak", "sleep 10")
	start := time.Now()
	r := ExecRunner{dir}.Run(context.Background(), Spec{Name: "soak", Timeout: 200 * time.Millisecond, PerGPU: true}, 0)
	if r.Status != report.ProbeTimeout {
		t.Fatalf("got %+v", r)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("hung probe was not stopped promptly")
	}
}
