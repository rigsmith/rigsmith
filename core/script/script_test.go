package script

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/shellrun"
)

// stubHost records side effects so a script's behaviour can be asserted without
// a real runner.
type stubHost struct {
	sh   func(string) (string, error)
	ran  []string // shell commands passed to Sh
	ops  []string // "<name> <args>" passed to FileOp
	logs []string // lines passed to Report
}

func (h *stubHost) Sh(cmd string) (string, error) {
	h.ran = append(h.ran, cmd)
	if h.sh != nil {
		return h.sh(cmd)
	}
	return "", nil
}
func (h *stubHost) FileOp(name string, args []string) error {
	h.ops = append(h.ops, name+" "+strings.Join(args, " "))
	return nil
}
func (h *stubHost) Report(line string) { h.logs = append(h.logs, line) }

func TestEvalBoolAndString(t *testing.T) {
	ctx := map[string]interface{}{"version": "1.2.3", "count": 2}

	if got, err := EvalBool(`ctx.count > 1`, ctx); err != nil || !got {
		t.Errorf("EvalBool(count>1) = %v, %v; want true", got, err)
	}
	if got, err := EvalBool(`ctx.version == "9.9.9"`, ctx); err != nil || got {
		t.Errorf("EvalBool(version mismatch) = %v, %v; want false", got, err)
	}
	// A global module (text) is available without an import.
	if got, err := EvalString(`text.to_upper(ctx.version)`, ctx); err != nil || got != "1.2.3" {
		// to_upper of digits is a no-op, but proves text resolved and ran.
		t.Errorf("EvalString(text.to_upper) = %q, %v; want 1.2.3", got, err)
	}
}

func TestEvalErrorSurfaces(t *testing.T) {
	if _, err := EvalBool(`this is +/ not valid`, map[string]interface{}{}); err == nil {
		t.Error("a malformed expression should error")
	}
}

func TestRunShAndLog(t *testing.T) {
	h := &stubHost{}
	ctx := map[string]interface{}{"version": "1.0.0"}

	code := `
sh("echo building " + ctx.version)
log("done", 42)
`
	if err := Run(code, ctx, h); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(h.ran) != 1 || h.ran[0] != "echo building 1.0.0" {
		t.Errorf("sh calls = %v, want [echo building 1.0.0]", h.ran)
	}
	if len(h.logs) != 1 || h.logs[0] != "done 42" {
		t.Errorf("log lines = %v, want [\"done 42\"]", h.logs)
	}
}

func TestRunFileOpsDelegateToHost(t *testing.T) {
	h := &stubHost{}
	if err := Run(`mkdir("-p", "dist"); cp("a", "b")`, map[string]interface{}{}, h); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"mkdir -p dist", "cp a b"}
	if strings.Join(h.ops, ",") != strings.Join(want, ",") {
		t.Errorf("file ops = %v, want %v", h.ops, want)
	}
}

func TestRunFailAborts(t *testing.T) {
	err := Run(`fail("nope")`, map[string]interface{}{}, &stubHost{})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("Run(fail) err = %v, want it to carry the fail message", err)
	}
}

func TestRunShNonZeroAborts(t *testing.T) {
	h := &stubHost{sh: func(string) (string, error) { return "", os.ErrClosed }}
	// The second statement must not run once sh() aborts.
	if err := Run(`sh("boom"); log("after")`, map[string]interface{}{}, h); err == nil {
		t.Fatal("a failing sh() should abort the script")
	}
	if len(h.logs) != 0 {
		t.Errorf("statements after a failing sh() must not run; logs=%v", h.logs)
	}
}

// captureHost wires RunnerHost to a portable runner and a captured report sink,
// the canonical Host most callers will use.
func captureHost(dir string, dryRun bool) (Host, *[]string) {
	var logs []string
	h := RunnerHost(shellrun.NewPortableRunner(nil), dir, dryRun, func(l string) { logs = append(logs, l) })
	return h, &logs
}

func TestRunnerHostExecutesFileOpsAndSh(t *testing.T) {
	dir := t.TempDir()
	h, logs := captureHost(dir, false)

	if err := Run(`mkdir("-p", "a/b"); sh("echo hi")`, map[string]interface{}{}, h); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, "a", "b")); err != nil || !info.IsDir() {
		t.Errorf("mkdir did not create the nested path: %v", err)
	}
	if strings.Join(*logs, "\n") != "hi" {
		t.Errorf("reported output = %v, want [hi]", *logs)
	}
}

func TestRunnerHostPreviewsInDryRun(t *testing.T) {
	dir := t.TempDir()
	h, logs := captureHost(dir, true)

	if err := Run(`mkdir("-p", "dist"); sh("echo hi")`, map[string]interface{}{}, h); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist")); !os.IsNotExist(err) {
		t.Error("mkdir must not touch the disk in a dry run")
	}
	joined := strings.Join(*logs, "\n")
	if !strings.Contains(joined, "would mkdir -p dist") || !strings.Contains(joined, "would run: echo hi") {
		t.Errorf("dry-run previews = %v, want would-mkdir and would-run lines", *logs)
	}
}

func TestRunnerHostShNonZeroAborts(t *testing.T) {
	h, _ := captureHost(t.TempDir(), false)
	if err := Run(`sh("exit 7")`, map[string]interface{}{}, h); err == nil {
		t.Error("a non-zero sh() should abort the script")
	}
}
