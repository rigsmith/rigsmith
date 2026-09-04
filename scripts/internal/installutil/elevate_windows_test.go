//go:build windows

package installutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestAccessDeniedRequestsElevation(t *testing.T) {
	if !shouldElevate(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("Windows access denied must request UAC elevation")
	}
}

// The encoded command has to survive the trip through Start-Process and run
// as a script: a missing installer inside it is an error the launcher sees,
// not a silent success. Without -Verb RunAs, so no consent prompt appears.
func TestEncodedCommandRunsAndReportsFailure(t *testing.T) {
	script := elevatedCommand(filepath.Join(t.TempDir(), "missing.exe"), t.TempDir(), nil)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeCommand(script))
	if err := cmd.Run(); err == nil {
		t.Fatal("a missing installer must not run as a successful elevation")
	}
	// And a command that succeeds reports the marker the child set.
	probe := "$ErrorActionPreference = 'Stop'\n$env:" + elevatedEnv + " = '1'\nif ($env:" + elevatedEnv + " -ne '1') { exit 2 }\nexit 0\n"
	if err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodeCommand(probe)).Run(); err != nil {
		t.Fatalf("encoded command did not run: %v", err)
	}
}

func TestElevationScriptFailsWhenProcessCannotStart(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", elevationScript)
	cmd.Env = append(os.Environ(), "RIGSMITH_INSTALL_CMD=")
	if err := cmd.Run(); err == nil {
		t.Fatal("PowerShell launch failure must not be reported as a successful elevation")
	}
}
