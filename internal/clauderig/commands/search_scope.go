package commands

import (
	"fmt"
	"io"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
)

// renderCoverage prints the device roster and warns about every machine whose
// sessions this search could not see. It prints nothing when this machine is the
// only one on the registry — there is then no elsewhere for a chat to be.
//
// The filters and the roster themselves live in [sessions.Scope]; what stays
// here is only how to say it out loud, which is the CLI's business alone.
func renderCoverage(out io.Writer, sc sessions.Scope) {
	if sc.DevicesUnavailable {
		fmt.Fprintf(out, "%s\n", WarnStyle.Render(
			"device coverage unavailable — the synced device registry could not be read"))
		return
	}
	others := 0
	for _, d := range sc.Devices {
		if d.Name != sc.Me {
			others++
		}
	}
	// With the live roots out of scope (--repo), this machine's own sync age
	// bounds the search too, so a one-device registry still has something to say.
	if others == 0 && sc.LiveInScope {
		return
	}
	if len(sc.Devices) == 0 {
		return
	}

	parts := make([]string, 0, len(sc.Devices))
	for _, d := range sc.Devices {
		label := d.Name + " " + humanizeSinceAt(d.LastSync, sc.Now)
		if d.Name == sc.Me {
			label += " (this)"
			if sc.LiveInScope {
				label += ", searched live"
			}
		}
		parts = append(parts, label)
	}
	fmt.Fprintf(out, "%s\n", DimStyle.Render("devices  "+strings.Join(parts, " · ")))

	for _, d := range sc.StaleDevices() {
		fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf(
			"%s has not synced since %s — anything it recorded after that is not searchable here",
			d.Name, d.LastSync.UTC().Format("2006-01-02 15:04 UTC"))))
		fmt.Fprintf(out, "%s\n", DimStyle.Render("  run `clauderig sync` there, then `clauderig pull` here"))
	}
}
