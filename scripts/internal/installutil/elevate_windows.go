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

const elevationScript = `$ErrorActionPreference = 'Stop'; try { $p = Start-Process -FilePath $env:RIGSMITH_INSTALL_EXE -WorkingDirectory $env:RIGSMITH_INSTALL_CWD -Verb RunAs -Wait -PassThru -ErrorAction Stop; if ($null -eq $p) { exit 1 }; exit $p.ExitCode } catch { Write-Error $_; exit 1 }`

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
	cmd.Env = append(os.Environ(),
		elevatedEnv+"=1",
		"RIGSMITH_INSTALL_EXE="+exe,
		"RIGSMITH_INSTALL_CWD="+cwd,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running elevated installer: %w", err)
	}
	return nil
}
