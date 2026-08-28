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

func TestElevationScriptFailsWhenProcessCannotStart(t *testing.T) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", elevationScript)
	cmd.Env = append(os.Environ(),
		"RIGSMITH_INSTALL_EXE="+filepath.Join(t.TempDir(), "missing.exe"),
		"RIGSMITH_INSTALL_CWD="+t.TempDir(),
	)
	if err := cmd.Run(); err == nil {
		t.Fatal("PowerShell launch failure must not be reported as a successful elevation")
	}
}
