package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// signedReport runs a fake check and returns the report path and ASSAY_HOME.
func signedReport(t *testing.T) (reportPath, home string) {
	t.Helper()
	pin(t)
	reportPath = filepath.Join(t.TempDir(), "report.json")
	if code, _, stderr := run("check", "--backend=fake", "--samples=3", "--probes=none", "--out", reportPath); code != 0 {
		t.Fatalf("check failed: %s", stderr)
	}
	return reportPath, os.Getenv("ASSAY_HOME")
}

func TestKeygenCreatesKeyAndRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	code, stdout, stderr := run("keygen", "--dir", dir)
	if code != 0 || !strings.Contains(stdout, "never commit") {
		t.Fatalf("exit %d\n%s%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent.key")); err != nil {
		t.Fatal("agent.key not written")
	}
	if code, _, _ := run("keygen", "--dir", dir); code != 1 {
		t.Fatalf("second keygen exit %d, want 1 (refuse overwrite)", code)
	}
}

func TestVerifyAcceptsUntouchedReport(t *testing.T) {
	path, home := signedReport(t)
	code, stdout, _ := run("verify", "--pubkey", filepath.Join(home, "agent.pub"), path)
	if code != 0 || !strings.HasPrefix(stdout, "OK") || strings.Contains(stdout, "signer not checked") {
		t.Fatalf("exit %d:\n%s", code, stdout)
	}
}

func TestVerifyWithoutPubkeyWarns(t *testing.T) {
	path, _ := signedReport(t)
	_, stdout, _ := run("verify", path)
	if !strings.Contains(stdout, "signer not checked") {
		t.Fatalf("want a note that the signer wasn't checked:\n%s", stdout)
	}
}

func TestVerifyCatchesAnEditedFile(t *testing.T) {
	path, _ := signedReport(t)
	data, _ := os.ReadFile(path)
	// Someone "improves" the GPU: lower every 38°C reading to 37°C.
	edited := strings.ReplaceAll(string(data), `"temp_c": 38`, `"temp_c": 37`)
	if edited == string(data) {
		t.Fatal("test setup: nothing was edited")
	}
	_ = os.WriteFile(path, []byte(edited), 0o644)

	code, stdout, _ := run("verify", path)
	if code != 1 || !strings.Contains(stdout, "modified since it was signed") {
		t.Fatalf("exit %d:\n%s", code, stdout)
	}
}

func TestVerifyIgnoresFormattingChanges(t *testing.T) {
	// Re-indenting the file changes bytes but not data, so it must still verify.
	path, _ := signedReport(t)
	data, _ := os.ReadFile(path)
	_ = os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "  ", "\t")), 0o644)
	if code, stdout, _ := run("verify", path); code != 0 {
		t.Fatalf("exit %d:\n%s", code, stdout)
	}
}

func TestVerifyUsage(t *testing.T) {
	if code, _, _ := run("verify"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}
