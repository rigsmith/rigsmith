package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// interactive reports whether we're attached to a terminal (so it's safe to
// launch git mergetool / prompt). Hooks run non-interactively and must not.
func interactive() bool { return Interactive() }

// Interactive reports whether both stdin and stdout are real terminals — the
// shared gate for any prompt and for landing bare `clauderig` on the dashboard.
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
