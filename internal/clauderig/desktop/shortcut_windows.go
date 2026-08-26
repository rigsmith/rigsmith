//go:build windows

package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A Windows shortcut is a real .lnk, created through WScript.Shell — the COM
// object every scripting host has used for this since the nineties, and the
// only way to write the binary shell-link format without either a Go
// implementation of it or a cgo dependency on IShellLink. The package already
// shells to PowerShell for its process scans, so this adds a mechanism, not a
// dependency.
//
// Values reach the script through the ENVIRONMENT rather than being pasted into
// its text. A label and a path can contain quotes, backticks and $ — every one
// of which means something to PowerShell's parser — and building the script by
// concatenation is how that turns into a syntax error at best.

const lnkExt = ".lnk"

func destLabel(d Dest) string {
	if d == DestApps {
		return "the Start Menu"
	}
	return "the Desktop"
}

// shortcutDir asks Windows where the folder is instead of composing it from
// %USERPROFILE%. On most machines with OneDrive the Desktop is redirected into
// the OneDrive tree, and a shortcut written to the literal %USERPROFILE%\Desktop
// then lands in a folder the user never sees.
func shortcutDir(d Dest) (string, error) {
	folder := "DesktopDirectory"
	if d == DestApps {
		folder = "Programs" // …\AppData\Roaming\Microsoft\Windows\Start Menu\Programs
	}
	const script = `$ErrorActionPreference='Stop'
$p = [Environment]::GetFolderPath($env:CLAUDERIG_SC_FOLDER)
if (-not $p) { throw "Windows did not report a path for this folder" }
Write-Output $p`
	out, err := runPowerShell(script, map[string]string{"CLAUDERIG_SC_FOLDER": folder})
	if err != nil {
		return "", fmt.Errorf("locate the %s folder: %w", d.Where(), err)
	}
	path := strings.TrimSpace(out)
	if path == "" {
		return "", fmt.Errorf("Windows did not report a path for %s", d.Where())
	}
	return path, nil
}

func installShortcutIn(dir string, spec ShortcutSpec) (Shortcut, error) {
	spec, err := spec.normalized()
	if err != nil {
		return Shortcut{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Shortcut{}, err
	}
	final := filepath.Join(dir, spec.Label+lnkExt)

	switch _, err := os.Lstat(final); {
	case err == nil:
		// Ownership is decided by the same listing that `rm` deletes from, so
		// "may I replace this" and "is this mine" can never disagree.
		ours, lerr := listShortcutsIn(dir)
		if lerr != nil {
			return Shortcut{}, fmt.Errorf("could not tell whether %s is one of ours: %w", final, lerr)
		}
		if !containsPath(ours, final) && !spec.Force {
			return Shortcut{}, fmt.Errorf("%w: %s", ErrShortcutExists, final)
		}
	case !errors.Is(err, os.ErrNotExist):
		return Shortcut{}, err
	}

	// CreateShortcut+Save overwrites in place, so there is no build-then-swap
	// here as there is on macOS: a .lnk is one small file the shell writes
	// whole, not a directory tree that can be left half-built.
	const script = `$ErrorActionPreference='Stop'
$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:CLAUDERIG_SC_PATH)
$s.TargetPath = $env:CLAUDERIG_SC_EXE
$s.Arguments = $env:CLAUDERIG_SC_ARGS
$s.Description = $env:CLAUDERIG_SC_DESC
$s.WorkingDirectory = $env:CLAUDERIG_SC_CWD
if ($env:CLAUDERIG_SC_ICON) { $s.IconLocation = $env:CLAUDERIG_SC_ICON }
$s.WindowStyle = 7
$s.Save()`
	env := map[string]string{
		"CLAUDERIG_SC_PATH": final,
		"CLAUDERIG_SC_EXE":  spec.Exe,
		// The profile name is validated to letters, digits, dot, dash and
		// underscore, so it needs no quoting to survive argv — quoted anyway,
		// because the rule that keeps it safe lives in another file.
		"CLAUDERIG_SC_ARGS": `desktop open "` + spec.Profile + `"`,
		"CLAUDERIG_SC_DESC": shortcutDescription(spec.Profile),
		// Not the exe's own directory: a shortcut's working directory is held
		// open by the shell, and there is no reason to pin clauderig's install
		// folder. `desktop open` is given an explicit profile name, so nothing
		// it does depends on where it runs.
		"CLAUDERIG_SC_CWD":  os.Getenv("USERPROFILE"),
		"CLAUDERIG_SC_ICON": claudeIconLocation(),
	}
	if _, err := runPowerShell(script, env); err != nil {
		return Shortcut{}, fmt.Errorf("create %s: %w", final, err)
	}
	// COM reports success through an exception it may not have thrown; confirm
	// the file is actually there before telling the user it is.
	if _, err := os.Stat(final); err != nil {
		return Shortcut{}, fmt.Errorf("create %s: the shortcut was not written: %w", final, err)
	}
	return Shortcut{Profile: spec.Profile, Label: spec.Label, Path: final, Dest: spec.Dest}, nil
}

// claudeIconLocation points the shortcut at the icon inside claude.exe. Returns
// "" when Claude Desktop cannot be found, which leaves the shortcut showing
// clauderig's own icon — worse-looking, never a failure.
func claudeIconLocation() string {
	exe, ok := windowsApp{}.Installed()
	if !ok {
		return ""
	}
	return exe + ",0"
}

// lnkRow is the shape asked of PowerShell when reading a folder of shortcuts.
type lnkRow struct {
	Path        string `json:"Path"`
	Description string `json:"Description"`
}

func listShortcutsIn(dir string) ([]Shortcut, error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	// Not recursive: clauderig writes into the folder itself, and a Start Menu
	// tree can be large. A shortcut the user filed into a subfolder is theirs
	// to manage, and is left alone rather than deleted from under them.
	const script = `$ErrorActionPreference='Stop'
$sh = New-Object -ComObject WScript.Shell
$rows = @()
foreach ($f in Get-ChildItem -LiteralPath $env:CLAUDERIG_SC_DIR -Filter *.lnk -File -ErrorAction SilentlyContinue) {
	try { $lnk = $sh.CreateShortcut($f.FullName) } catch { continue }
	$rows += [pscustomobject]@{ Path = $f.FullName; Description = $lnk.Description }
}
ConvertTo-Json -Compress -InputObject @($rows)`
	out, err := runPowerShell(script, map[string]string{"CLAUDERIG_SC_DIR": dir})
	if err != nil {
		return nil, fmt.Errorf("read the shortcuts in %s: %w", dir, err)
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var rows []lnkRow
	if uerr := json.Unmarshal([]byte(trimmed), &rows); uerr != nil {
		return nil, fmt.Errorf("parse the shortcut list in %s: %w", dir, uerr)
	}
	var shortcuts []Shortcut
	for _, r := range rows {
		profile, ok := profileFromTag(r.Description)
		if !ok {
			continue // somebody else's shortcut
		}
		shortcuts = append(shortcuts, Shortcut{
			Profile: profile,
			Label:   strings.TrimSuffix(filepath.Base(r.Path), lnkExt),
			Path:    r.Path,
		})
	}
	return shortcuts, nil
}

func removeShortcutAt(path string) error { return os.Remove(path) }

// containsPath reports whether a listing already holds this file.
//
// Compared by FILE NAME, not by full path, and the difference is load-bearing.
// The listing came from the very directory the file is being written into, so a
// name identifies it there uniquely — while the two paths can be spelled
// differently and still be the same file: PowerShell reports the canonical long
// form (C:\Users\runneradmin\…) where the path we composed may carry an 8.3
// short component (C:\Users\RUNNER~1\…). Comparing the full strings made
// re-running the command over its OWN shortcut refuse it as somebody else's,
// which is the one thing that has to keep working: it is how a shortcut is
// repaired after clauderig moves.
func containsPath(list []Shortcut, path string) bool {
	name := filepath.Base(path)
	for _, s := range list {
		if strings.EqualFold(filepath.Base(s.Path), name) {
			return true
		}
	}
	return false
}

// runPowerShell runs a script with values supplied through the environment, and
// folds stderr into the error — a PowerShell failure prints there and exits
// zero often enough that a silent empty stdout would otherwise be read as an
// empty result.
func runPowerShell(script string, env map[string]string) (string, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		if err == nil {
			return stdout.String(), errors.New(msg)
		}
		return stdout.String(), fmt.Errorf("%w: %s", err, msg)
	}
	return stdout.String(), err
}
