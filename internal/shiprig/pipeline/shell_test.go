package pipeline

import (
	"os"
	"strings"
	"testing"
)

// isDir reports whether path is a directory — a local test helper now that the
// cross-platform file ops live in core/shellrun.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
