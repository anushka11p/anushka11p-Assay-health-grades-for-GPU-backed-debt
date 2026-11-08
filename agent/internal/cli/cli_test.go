package cli

import (
	"bytes"
	"strings"
	"testing"
)

// run is a helper: it calls Run with fake stdout/stderr buffers so the test
// can check what the program printed and which exit code it returned.
func run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestNoArgsPrintsUsageAndFails(t *testing.T) {
	code, _, stderr := run()
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "Usage: assay <command>") {
		t.Fatalf("stderr should contain usage, got:\n%s", stderr)
	}
}

func TestHelpPrintsUsage(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, stdout, _ := run(arg)
		if code != 0 {
			t.Fatalf("%s: exit code = %d, want 0", arg, code)
		}
		for _, cmd := range []string{"check", "keygen", "verify", "version"} {
			if !strings.Contains(stdout, cmd) {
				t.Fatalf("%s: usage should list %q, got:\n%s", arg, cmd, stdout)
			}
		}
	}
}

func TestVersion(t *testing.T) {
	code, stdout, _ := run("version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if strings.TrimSpace(stdout) != "assay "+Version {
		t.Fatalf("stdout = %q, want %q", stdout, "assay "+Version)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	code, _, stderr := run("explode")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown command "explode"`) {
		t.Fatalf("stderr should name the bad command, got:\n%s", stderr)
	}
}

func TestStubCommandsSayNotImplemented(t *testing.T) {
	for _, cmd := range []string{"check", "keygen", "verify"} {
		code, _, stderr := run(cmd)
		if code != 1 {
			t.Fatalf("%s: exit code = %d, want 1", cmd, code)
		}
		if !strings.Contains(stderr, "not implemented yet") {
			t.Fatalf("%s: stderr = %q, want 'not implemented yet'", cmd, stderr)
		}
	}
}
