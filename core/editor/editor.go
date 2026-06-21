// Package editor resolves which editor command to launch for an interactive
// file edit, shared across the rigsmith CLIs (rig, changerig, clauderig).
package editor

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// guiEditors are GUI editors auto-launched when neither $VISUAL nor $EDITOR is
// set, in preference order. Each is launched blocking (--wait) so the edit
// completes before the caller re-reads the file.
var guiEditors = []struct {
	cmd  string
	args []string
}{
	{"code", []string{"--wait"}},          // VS Code
	{"code-insiders", []string{"--wait"}}, // VS Code Insiders
	{"cursor", []string{"--wait"}},        // Cursor
}

// macAppBundles are the macOS .app fallbacks probed when a GUI editor's CLI
// isn't on PATH. They're opened via `open -W -a <app>`, which blocks until the
// app's window for the file is closed.
var macAppBundles = []struct {
	appName    string
	bundlePath string
}{
	{"Visual Studio Code", "/Applications/Visual Studio Code.app"},
	{"Cursor", "/Applications/Cursor.app"},
}

// Argv returns the command (program + args + path) to open path for an
// interactive edit, reading $VISUAL/$EDITOR from the environment and probing the
// real PATH and filesystem. See resolveArgv for precedence. Always returns a
// runnable argv — it never errors, falling back to a per-OS terminal default.
func Argv(path string) []string {
	return resolveArgv(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS, exec.LookPath, bundleExists, path)
}

// resolveArgv decides how to open path. Precedence: $VISUAL, then $EDITOR
// (splitting on spaces honors forms like "code --wait"); else the first detected
// GUI editor — by PATH command, then macOS .app bundle — launched blocking; else
// a per-OS terminal default (notepad on Windows, vi elsewhere). Always returns a
// runnable argv. Pure given lookPath and bundleExists.
func resolveArgv(visual, editorEnv, goos string, lookPath func(string) (string, error), bundleExists func(string) bool, path string) []string {
	if ed := firstNonEmpty(visual, editorEnv); ed != "" {
		return append(strings.Fields(ed), path)
	}
	for _, e := range guiEditors {
		if _, err := lookPath(e.cmd); err == nil {
			return append(append([]string{e.cmd}, e.args...), path)
		}
	}
	if goos == "darwin" {
		for _, b := range macAppBundles {
			if bundleExists(b.bundlePath) {
				return []string{"open", "-W", "-a", b.appName, path}
			}
		}
	}
	if goos == "windows" {
		return []string{"notepad", path}
	}
	return []string{"vi", path}
}

// firstNonEmpty returns the first argument that isn't blank (after trimming),
// else "".
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if s := strings.TrimSpace(x); s != "" {
			return s
		}
	}
	return ""
}

// bundleExists reports whether p is an existing directory (a macOS .app bundle).
func bundleExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
