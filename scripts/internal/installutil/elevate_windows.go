//go:build windows

package installutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// elevationScript is what the launcher's own PowerShell runs: start an elevated
// PowerShell that executes the encoded command in RIGSMITH_INSTALL_CMD, wait
// for it, and hand its exit code back. The encoded command is the one thing
// crossing the elevation boundary, and it carries everything the installer
// needs — see elevatedCommand.
var elevationScript = elevationScriptFor("powershell.exe", "RunAs")

// elevationScriptFor renders elevationScript for a given host and
// Start-Process verb, so a test can run the real script without the consent
// prompt RunAs raises — and against a host that does not exist, to see a
// launch failure reported as one.
func elevationScriptFor(host, verb string) string {
	return "$ErrorActionPreference = 'Stop'; try { $p = Start-Process -FilePath " + psQuote(host) +
		" -ArgumentList @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-EncodedCommand', $env:RIGSMITH_INSTALL_CMD)" +
		" -Verb " + verb + " -Wait -PassThru -ErrorAction Stop; if ($null -eq $p) { exit 1 }; exit $p.ExitCode } catch { Write-Error $_; exit 1 }"
}

func shouldElevate(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, windows.ERROR_ELEVATION_REQUIRED)
}

func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating installer executable: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("locating installer working directory: %w", err)
	}

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", elevationScript)
	cmd.Env = append(os.Environ(), "RIGSMITH_INSTALL_CMD="+encodeCommand(elevatedCommand(exe, cwd, os.Environ())))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running elevated installer: %w", err)
	}
	return nil
}
