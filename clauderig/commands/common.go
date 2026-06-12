package commands

import (
	"fmt"
	"io"
	"runtime"

	"github.com/rigsmith/core/pathmap"
)

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

// currentOSToken maps Go's runtime.GOOS to the pathmap OS token used by the
// cascade/resolver.
func currentOSToken() string {
	switch runtime.GOOS {
	case "windows":
		return pathmap.OSWindows
	case "darwin":
		return pathmap.OSMacOS
	default:
		return pathmap.OSLinux
	}
}
