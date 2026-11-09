package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logSetup makes a log key and two different signed reports.
func logSetup(t *testing.T) (dir, r1, r2 string) {
	t.Helper()
	pin(t)
	home := os.Getenv("ASSAY_HOME")
	if code, _, stderr := run("keygen", "--name", "log", "--dir", home); code != 0 {
		t.Fatal(stderr)
	}
	tmp := t.TempDir()
	dir = filepath.Join(tmp, "log")
	r1, r2 = filepath.Join(tmp, "r1.json"), filepath.Join(tmp, "r2.json")
	for path, scenario := range map[string]string{r1: "healthy", r2: "xid-storm"} {
		if code, _, stderr := run("check", "--backend=fake", "--scenario="+scenario, "--samples=2", "--probes=none", "--out", path); code != 0 {
			t.Fatal(stderr)
		}
	}
	return dir, r1, r2
}

func TestLogAppendProveVerify(t *testing.T) {
	dir, r1, r2 := logSetup(t)
	code, stdout, stderr := run("log", "append", "--dir", dir, r1, r2)
	if code != 0 || !strings.Contains(stdout, "tree head: 2 entries") {
		t.Fatalf("append exit %d\n%s%s", code, stdout, stderr)
	}
	// Appending again changes nothing.
	_, stdout, _ = run("log", "append", "--dir", dir, r1)
	if !strings.Contains(stdout, "already logged #0") || !strings.Contains(stdout, "2 entries") {
		t.Fatalf("re-append:\n%s", stdout)
	}
	if code, stdout, stderr := run("log", "head", "--dir", dir); code != 0 || !strings.HasPrefix(stdout, "OK") {
		t.Fatalf("head exit %d\n%s%s", code, stdout, stderr)
	}
	if code, _, stderr := run("log", "prove", "--dir", dir, r2); code != 0 {
		t.Fatal(stderr)
	}
	proof := strings.TrimSuffix(r2, ".json") + ".proof.json"
	code, stdout, _ = run("verify", "--proof", proof, "--log-pubkey", filepath.Join(dir, "log.pub"), r2)
	if code != 0 || !strings.Contains(stdout, "entry #1 of 2") || strings.Contains(stdout, "log key not checked") {
		t.Fatalf("verify exit %d\n%s", code, stdout)
	}
	// The proof for r2 must not vouch for r1.
	if code, _, _ := run("verify", "--proof", proof, r1); code != 1 {
		t.Fatal("proof for one report accepted for another")
	}
}

func TestLogRefusesTamperedReport(t *testing.T) {
	dir, r1, _ := logSetup(t)
	data, _ := os.ReadFile(r1)
	_ = os.WriteFile(r1, []byte(strings.Replace(string(data), `"temp_c": 38`, `"temp_c": 37`, 1)), 0o644)
	code, _, stderr := run("log", "append", "--dir", dir, r1)
	if code != 1 || !strings.Contains(stderr, "refusing to log") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestLogDetectsRewrittenHistory(t *testing.T) {
	dir, r1, r2 := logSetup(t)
	run("log", "append", "--dir", dir, r1)
	// An auditor saves the head after the first report...
	old := filepath.Join(t.TempDir(), "old-head.json")
	data, _ := os.ReadFile(filepath.Join(dir, "head.json"))
	_ = os.WriteFile(old, data, 0o644)

	// ...the log grows honestly: consistency holds.
	run("log", "append", "--dir", dir, r2)
	code, stdout, _ := run("log", "consistency", "--dir", dir, old)
	if code != 0 || !strings.Contains(stdout, "grew from 1 to 2") {
		t.Fatalf("honest growth: exit %d\n%s", code, stdout)
	}

	// Now the operator quietly rebuilds the log without the first report and re-signs.
	_ = os.Remove(filepath.Join(dir, "entries.jsonl"))
	run("log", "append", "--dir", dir, r2)
	code, stdout, _ = run("log", "consistency", "--dir", dir, old)
	if code != 1 || !strings.Contains(stdout, "history changed") {
		t.Fatalf("rewritten history not caught: exit %d\n%s", code, stdout)
	}
}

func TestLogNeedsALogKey(t *testing.T) {
	_, r1, _ := logSetup(t)
	code, _, stderr := run("log", "append", "--dir", t.TempDir(), "--key", "/nonexistent/log.key", r1)
	if code != 1 || !strings.Contains(stderr, "keygen --name log") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestLogUsage(t *testing.T) {
	if code, _, _ := run("log"); code != 2 {
		t.Error("bare `assay log` should print usage and exit 2")
	}
	if code, _, _ := run("log", "shred"); code != 2 {
		t.Error("unknown log command should exit 2")
	}
}
