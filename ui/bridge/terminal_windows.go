package bridge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// terminalScriptExt is the extension cmd.exe runs as a batch file.
const terminalScriptExt = ".cmd"

// terminalScript builds a batch file. `cd /d` because plain cd will not cross
// drives, which is exactly the case a project on D: hits.
func terminalScript(dir string, argv []string) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	if dir != "" {
		b.WriteString("cd /d " + cmdQuote(dir) + " || exit /b 1\r\n")
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = cmdQuote(a)
	}
	b.WriteString(strings.Join(quoted, " ") + "\r\n")
	return b.String()
}

// cmdQuote wraps a value for cmd.exe. Double quotes, an internal quote doubled,
// and `%` escaped — a batch file expands %VAR% and a path containing a percent
// sign would otherwise silently lose part of itself.
func cmdQuote(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, `"`, `""`)
	return `"` + s + `"`
}

// launchTerminal opens the script in a terminal window.
//
// Windows Terminal first when it is installed: it is what a current Windows
// machine has and what the Claude Code docs assume, and unlike a bare cmd
// window it survives the session ending. Falls back to cmd.exe, which every
// machine has.
func launchTerminal(ctx context.Context, script string) error {
	if app := envTerminal(); app != "" {
		return runTerminal(ctx, app, script)
	}
	if _, err := exec.LookPath("wt.exe"); err == nil {
		return runTerminal(ctx, "wt.exe", script)
	}
	// `start` is a cmd builtin, so it has to be run through cmd itself. The
	// empty "" is the window title: without it start treats a quoted script
	// path as the title and opens nothing.
	if out, err := exec.CommandContext(ctx, "cmd.exe", "/c", "start", "", script).CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

func runTerminal(ctx context.Context, app, script string) error {
	if out, err := exec.CommandContext(ctx, app, script).CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("could not open %s: %s", app, msg)
		}
		return err
	}
	return nil
}
