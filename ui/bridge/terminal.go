package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// runInTerminal opens a terminal window running argv, optionally in dir.
//
// argv rather than a command string, and dir rather than a `cd` baked into it,
// because quoting is per-platform: a POSIX shell wants single quotes and a
// cmd.exe script wants double ones with its own escape for `%`. Handing each
// platform the pieces lets it build something its own shell will actually
// parse, instead of one string that happens to be right on the machine it was
// written on.
//
// tag names the temp script so a stale one is recognisable; it is not trusted
// as a path, only sanitised into one.
func runInTerminal(ctx context.Context, tag, dir string, argv []string) error {
	script := filepath.Join(os.TempDir(), "clauderig-"+safeTag(tag)+terminalScriptExt)
	if err := os.WriteFile(script, []byte(terminalScript(dir, argv)), 0o700); err != nil {
		return err
	}
	return launchTerminal(ctx, script)
}

// safeTag reduces a tag to characters that are a filename on every platform.
func safeTag(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

// envTerminal is the user's chosen terminal, if they named one. Honoured on
// every platform: someone who runs Ghostty on a Mac and Windows Terminal on a
// PC should be able to say so once, in one variable.
func envTerminal() string { return strings.TrimSpace(os.Getenv("CLAUDERIG_TERMINAL")) }

// resumeArgv is the command that resumes a session, as pieces for the platform
// to quote.
func resumeArgv(id string) []string { return []string{"claude", "--resume", id} }
