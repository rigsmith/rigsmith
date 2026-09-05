//go:build !darwin && !windows

package bridge

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const terminalScriptExt = ".sh"

func terminalScript(dir string, argv []string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	if dir != "" {
		b.WriteString("cd " + shQuote(dir) + " || exit 1\n")
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shQuote(a)
	}
	b.WriteString(strings.Join(quoted, " ") + "\n")
	return b.String()
}

// unixTerminals are tried in order. There is no equivalent of `open -a` here —
// no registered default terminal — so the only honest approach is to look for
// the ones that exist. x-terminal-emulator comes first because on Debian and
// Ubuntu it IS the user's choice, expressed through alternatives.
var unixTerminals = []struct {
	bin  string
	args []string // how that terminal is told to run a command
}{
	{"x-terminal-emulator", []string{"-e"}},
	{"gnome-terminal", []string{"--"}},
	{"konsole", []string{"-e"}},
	{"xfce4-terminal", []string{"-x"}},
	{"kitty", nil},
	{"alacritty", []string{"-e"}},
	{"wezterm", []string{"start", "--"}},
	{"xterm", []string{"-e"}},
}

func launchTerminal(ctx context.Context, script string) error {
	if app := envTerminal(); app != "" {
		return startTerminal(ctx, app, []string{"-e"}, script)
	}
	for _, t := range unixTerminals {
		if _, err := exec.LookPath(t.bin); err != nil {
			continue
		}
		return startTerminal(ctx, t.bin, t.args, script)
	}
	return fmt.Errorf("no terminal emulator found — set CLAUDERIG_TERMINAL, or use Copy command")
}

// startTerminal launches and does NOT wait: a terminal runs for as long as the
// user keeps it open, and waiting would block the window that opened it.
func startTerminal(ctx context.Context, bin string, args []string, script string) error {
	cmd := exec.CommandContext(ctx, bin, append(append([]string{}, args...), script)...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open %s: %w", bin, err)
	}
	go func() { _ = cmd.Wait() }() // reap it rather than leaving a zombie
	return nil
}
