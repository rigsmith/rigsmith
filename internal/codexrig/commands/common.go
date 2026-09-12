package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// interactive reports whether we're attached to a terminal (so it's safe to
// prompt). Hooks run non-interactively and must not. A variable so a test can
// pin the answer: a prompt that a test reaches on a developer's terminal blocks
// on stdin instead of asserting anything.
var interactive = Interactive

// Interactive reports whether both stdin and stdout are real terminals — the
// shared gate for any prompt and for landing bare `codexrig` on the dashboard.
func Interactive() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) && isatty.IsTerminal(os.Stdin.Fd())
}

// planned prints a scaffolded command's intended behaviour plus a clear
// not-yet-implemented marker, so the skeleton is runnable and self-documenting
// while the real logic lands incrementally.
func planned(w io.Writer, title string, lines ...string) {
	fmt.Fprintln(w, HeaderStyle.Render(title))
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
	fmt.Fprintf(w, "\n  %s\n", DimStyle.Render("(not yet implemented)"))
}

// errCheckFailed is returned by a check command that found a problem, so the
// process exits non-zero without printing a second error line on top of the
// report it already rendered.
var errCheckFailed = &silentError{}

type silentError struct{}

func (e *silentError) Error() string { return "" }
