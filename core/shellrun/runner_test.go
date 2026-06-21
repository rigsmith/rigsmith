package shellrun

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runPortable is a small helper: run a shell string through the portable runner.
func runPortable(t *testing.T, env []string, script, dir string) ([]string, int, error) {
	t.Helper()
	return NewPortableRunner(env)(true, []string{script}, dir)
}

func TestPortableShellPosixSyntaxAndExitCodes(t *testing.T) {
	// && sequencing — pure interpreter, no OS shell, no external binaries.
	lines, code, err := runPortable(t, nil, "echo hi && echo bye", t.TempDir())
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if strings.Join(lines, ",") != "hi,bye" {
		t.Errorf("lines = %v, want [hi bye]", lines)
	}

	// $((...)) arithmetic and variables — these would fail in cmd.exe, proving
	// the in-process POSIX shell is doing the work.
	if lines, _, _ := runPortable(t, nil, "x=5; echo $((x * 2))", t.TempDir()); strings.Join(lines, "") != "10" {
		t.Errorf("arithmetic = %v, want 10", lines)
	}

	// || recovery resets the exit code.
	if _, code, _ := runPortable(t, nil, "false || echo ok", t.TempDir()); code != 0 {
		t.Errorf("|| recovery code = %d, want 0", code)
	}

	// A non-zero exit is reported, not turned into a runner error.
	if _, code, err := runPortable(t, nil, "exit 3", t.TempDir()); err != nil || code != 3 {
		t.Errorf("exit 3 → code=%d err=%v, want code 3", code, err)
	}
}

func TestPortableShellControlFlowAndRedirection(t *testing.T) {
	// A for-loop with arithmetic — pure interpreter, identical on every OS.
	if lines, _, _ := runPortable(t, nil, "total=0; for x in 1 2 3; do total=$((total+x)); done; echo $total", t.TempDir()); strings.Join(lines, "") != "6" {
		t.Errorf("for-loop sum = %v, want 6", lines)
	}

	// An if/else taking the else branch.
	if lines, _, _ := runPortable(t, nil, `if [ 1 -gt 2 ]; then echo a; else echo b; fi`, t.TempDir()); strings.Join(lines, "") != "b" {
		t.Errorf("if/else = %v, want b", lines)
	}

	// Redirection writes a real file (a shell feature handled in-process); read
	// it back with Go so the test needs no external `cat`.
	dir := t.TempDir()
	if _, code, err := runPortable(t, nil, "echo hello > out.txt", dir); err != nil || code != 0 {
		t.Fatalf("redirection code=%d err=%v", code, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); strings.TrimSpace(string(b)) != "hello" {
		t.Errorf("redirected file = %q, want hello", b)
	}
}

func TestPortableShellUsesEnvAndDir(t *testing.T) {
	// Env is honoured ($FOO expands via the interpreter).
	if lines, _, _ := runPortable(t, []string{"FOO=bar"}, "echo $FOO", t.TempDir()); strings.Join(lines, "") != "bar" {
		t.Errorf("env expansion = %v, want bar", lines)
	}

	// Globbing runs relative to dir — write a marker file and let `echo *` expand.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if lines, _, _ := runPortable(t, nil, "echo *", dir); strings.Join(lines, "") != "marker" {
		t.Errorf("glob in dir = %v, want [marker]", lines)
	}
}

func TestPortableShellParseErrorSurfaces(t *testing.T) {
	_, code, err := runPortable(t, nil, `echo "unterminated`, t.TempDir())
	if err == nil {
		t.Error("an unparseable script should return an error")
	}
	if code != -1 {
		t.Errorf("parse error code = %d, want -1", code)
	}
}

// NewExecRunner runs each command with the provided environment, so a value
// declared only in the layered env reaches the spawned command.
func TestNewExecRunnerPassesEnvToCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	runner := NewExecRunner([]string{"FROM_DOTENV=yes"})

	out, code, err := runner(true, []string{`printf %s "$FROM_DOTENV"`}, "")
	if err != nil || code != 0 {
		t.Fatalf("run failed: code=%d err=%v", code, err)
	}
	if len(out) != 1 || out[0] != "yes" {
		t.Errorf("command output = %v, want the env value [yes]", out)
	}
}

// An empty (non-nil) env must inherit the process environment, not clear it —
// matching NewPortableRunner. envstack.Environ of an empty map yields exactly
// this, so a cleared PATH here would be a nasty surprise.
func TestNewExecRunnerEmptyEnvInherits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	// `echo` resolves only if PATH was inherited (it's a /bin/sh builtin here,
	// but the shell itself still needs to be found via the inherited environment).
	out, code, err := NewExecRunner([]string{})(true, []string{"echo inherited"}, t.TempDir())
	if err != nil || code != 0 {
		t.Fatalf("empty env should inherit and run: code=%d err=%v", code, err)
	}
	if len(out) != 1 || out[0] != "inherited" {
		t.Errorf("output = %v, want [inherited]", out)
	}
}

func TestPortableRunnerArgvExecsDirectly(t *testing.T) {
	// argv commands have no shell syntax; the portable runner exec's them like
	// the system runner. Use the test binary itself as the target so this works
	// on every OS (rather than relying on `echo` being a standalone executable,
	// which it is not on Windows).
	_, code, err := NewPortableRunner(nil)(false, []string{os.Args[0], "-test.run=^NoSuchTest$"}, t.TempDir())
	if err != nil || code != 0 {
		t.Fatalf("argv exec of the test binary failed: code=%d err=%v", code, err)
	}
}
