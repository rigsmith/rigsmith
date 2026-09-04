//go:build windows

package installutil

import (
	"errors"
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

// The launcher script, run without RunAs so no consent prompt appears:
// the child's exit code comes back as the launcher's, and a host that
// cannot start is a failure, not a silent success.
func TestElevationScriptReportsTheChildsOutcome(t *testing.T) {
	run := func(host string, encoded string) error {
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", elevationScriptFor(host, "Open"))
		cmd.Env = append(os.Environ(), "RIGSMITH_INSTALL_CMD="+encoded)
		return cmd.Run()
	}
	if err := run("powershell.exe", encodeCommand("exit 0")); err != nil {
		t.Fatalf("a child that succeeded was reported as a failure: %v", err)
	}
	var exit *exec.ExitError
	if err := run("powershell.exe", encodeCommand("exit 3")); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("child exit code not handed back: %v", err)
	}
	if err := run(filepath.Join(t.TempDir(), "no-such-host.exe"), encodeCommand("exit 0")); err == nil {
		t.Fatal("a host that cannot start must not be reported as a successful elevation")
	}
}
