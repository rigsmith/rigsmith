package bridge

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// terminalScriptExt is the extension Terminal.app executes on open.
const terminalScriptExt = ".command"

// terminalScript builds a POSIX script. The shell is left running in the
// directory when the command exits rather than exec'd away, so a session that
// ends leaves you somewhere useful instead of closing the window.
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

// launchTerminal opens the script in the user's terminal. CLAUDERIG_TERMINAL
// names an alternative (iTerm, Ghostty, WezTerm); Terminal is the default
// because it is the one macOS always has.
func launchTerminal(ctx context.Context, script string) error {
	app := envTerminal()
	if app == "" {
		app = "Terminal"
	}
	if out, err := exec.CommandContext(ctx, "open", "-a", app, script).CombinedOutput(); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("could not open %s: %s", app, msg)
		}
		return err
	}
	return nil
}
