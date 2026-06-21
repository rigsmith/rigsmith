package script

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubHost records side effects so a script's behaviour can be asserted without
// a real runner.
type stubHost struct {
	dir    string
	dryRun bool
	sh     func(string) (string, error)
	ran    []string // shell commands passed to Sh
	logs   []string // lines passed to Report
}

func (h *stubHost) Sh(cmd string) (string, error) {
	h.ran = append(h.ran, cmd)
	if h.sh != nil {
		return h.sh(cmd)
	}
	return "", nil
}
func (h *stubHost) Report(line string) { h.logs = append(h.logs, line) }
func (h *stubHost) Dir() string        { return h.dir }
func (h *stubHost) DryRun() bool       { return h.dryRun }

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
	h := &stubHost{dir: t.TempDir()}
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

func TestRunFileOpsExecuteWhenNotDryRun(t *testing.T) {
	dir := t.TempDir()
	h := &stubHost{dir: dir}

	if err := Run(`mkdir("-p", "a/b/c")`, map[string]interface{}{}, h); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dir, "a", "b", "c")); err != nil || !info.IsDir() {
		t.Errorf("mkdir did not create the nested path: %v", err)
	}
	if len(h.logs) != 0 {
		t.Errorf("a real run should not emit would-do previews; got %v", h.logs)
	}
}

func TestRunFileOpsPreviewInDryRun(t *testing.T) {
	dir := t.TempDir()
	h := &stubHost{dir: dir, dryRun: true}

	if err := Run(`mkdir("-p", "dist")`, map[string]interface{}{}, h); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist")); !os.IsNotExist(err) {
		t.Error("mkdir must not touch the disk in a dry run")
	}
	if len(h.logs) != 1 || !strings.Contains(h.logs[0], "would mkdir -p dist") {
		t.Errorf("dry-run preview = %v, want a 'would mkdir' line", h.logs)
	}
}

func TestRunFailAborts(t *testing.T) {
	h := &stubHost{dir: t.TempDir()}
	err := Run(`fail("nope")`, map[string]interface{}{}, h)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("Run(fail) err = %v, want it to carry the fail message", err)
	}
}

func TestRunShNonZeroAborts(t *testing.T) {
	h := &stubHost{
		dir: t.TempDir(),
		sh:  func(cmd string) (string, error) { return "", fmt.Errorf("sh: command exited 1: %s", cmd) },
	}
	// The second statement must not run once sh() aborts.
	err := Run(`sh("boom"); log("after")`, map[string]interface{}{}, h)
	if err == nil {
		t.Fatal("a failing sh() should abort the script")
	}
	if len(h.logs) != 0 {
		t.Errorf("statements after a failing sh() must not run; logs=%v", h.logs)
	}
}
