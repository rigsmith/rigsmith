package pipeline

import (
	"os"
	"path/filepath"
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

func TestPortableRunnerArgvExecsDirectly(t *testing.T) {
	// argv commands have no shell syntax; the portable runner exec's them like
	// the system runner. (echo is on PATH via the inherited env.)
	lines, code, err := NewPortableRunner(nil)(false, []string{"echo", "hello"}, t.TempDir())
	if err != nil || code != 0 {
		t.Fatalf("argv code=%d err=%v", code, err)
	}
	if strings.Join(lines, "") != "hello" {
		t.Errorf("argv output = %v, want hello", lines)
	}
}

func TestShellModeValidation(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", ShellPortable, true},
		{"portable", ShellPortable, true},
		{"system", ShellSystem, true},
		{"bash", "", false},
	} {
		got, err := ShellMode(tc.in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("ShellMode(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Errorf("ShellMode(%q) should error", tc.in)
		}
	}
}

func TestLoadConfigRejectsUnknownShell(t *testing.T) {
	if _, err := parseConfig(t, `{ "shell": "fish" }`); err == nil || !strings.Contains(err.Error(), "shell") {
		t.Errorf("err = %v, want a shell validation error", err)
	}
	if cfg := mustParseConfig(t, `{ "shell": "system" }`); cfg.Shell != "system" {
		t.Errorf("Shell = %q, want system", cfg.Shell)
	}
}
