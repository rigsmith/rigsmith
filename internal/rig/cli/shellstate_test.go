package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The Windows fallback exists for the normal case there — $SHELL unset. An
// explicitly set but unsupported shell is a fact about the setup, and
// overriding it would make doctor read the wrong startup file and report bogus
// missing/stale blocks.
func TestSetupShellNameKeepsAnExplicitUnsupportedShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Setenv("SHELL", "/usr/bin/nu")
		if got := setupShellName(); got != "" {
			t.Fatalf("setupShellName() = %q, want empty for an unsupported shell", got)
		}
		return
	}
	t.Setenv("SHELL", "nu")
	if got := setupShellName(); got == "powershell" {
		t.Fatal("an explicitly set unsupported shell was replaced with powershell")
	}
	t.Setenv("SHELL", "")
	if got := setupShellName(); got != "powershell" {
		t.Fatalf("setupShellName() = %q, want powershell when $SHELL is unset on Windows", got)
	}
}

// An empty PATH component means the current directory on POSIX. Skipping it
// reports a rig the shell can actually find as absent.
func TestPathCopiesTreatsAnEmptyEntryAsTheCurrentDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an empty PATH entry is not the cwd on Windows")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "rig-test-probe")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// An empty COMPONENT — "/usr/bin:" or ":/usr/bin" — is the POSIX spelling of
	// the current directory. An entirely empty PATH has no components at all.
	t.Setenv("PATH", ":"+t.TempDir())

	got := pathCopies("rig-test-probe")
	if len(got) == 0 {
		t.Fatal("an empty PATH entry was skipped, so the executable in the cwd was not found")
	}
	if !filepath.IsAbs(got[0]) {
		t.Fatalf("pathCopies returned a relative path %q — it cannot be compared with os.Executable", got[0])
	}
}
