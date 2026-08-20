package commands

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
)

// staleAfter is how long another machine may go without syncing before search
// says so out loud. A day is where "I searched everywhere" stops being true for
// a laptop that has been shut since yesterday.
const staleAfter = 24 * time.Hour

// sessionScope is everything that narrows a search and everything that bounds
// what it could possibly have seen: the --since/--until/--cwd filters, plus the
// synced device registry behind the coverage footer.
//
// The footer exists because absence of a hit is the answer people act on —
// "that chat is gone" — and it is only sound when the store is complete. A
// machine that has not synced since Tuesday makes every Wednesday session
// invisible here, and nothing in the results says so.
type sessionScope struct {
	caseSensitive bool
	// since/until bound a session's recency time (the sidecar's lastActivity,
	// else the transcript mtime) — the same instant the result line dates.
	since time.Time
	until time.Time
	// cwd is a case-insensitive substring of the session's project directory.
	cwd string
	// devices is the synced registry; empty when there is no staging repo (or
	// when --live took it out of scope), which correctly prints no footer.
	devices []devices.Device
	// me is this machine's name — excluded from staleness warnings, because its
	// live ~/.claude is scanned directly and its sync age hides nothing.
	me  string
	now time.Time
}

// filtering reports whether any narrowing flag was set.
func (sc sessionScope) filtering() bool {
	return !sc.since.IsZero() || !sc.until.IsZero() || sc.cwd != ""
}

// keep decides whether one session survives the filters. A session with no
// usable date cannot honestly be placed inside a time window, and one with no
// resolved cwd cannot be matched against --cwd, so each is dropped rather than
// waved through — the second return says which, so the caller can account for
// them instead of silently shrinking the result set.
func (sc sessionScope) keep(r *sessResult) (ok bool, undated bool) {
	if !sc.since.IsZero() || !sc.until.IsZero() {
		if r.when.IsZero() {
			return false, true
		}
		if !sc.since.IsZero() && r.when.Before(sc.since) {
			return false, false
		}
		if !sc.until.IsZero() && r.when.After(sc.until) {
			return false, false
		}
	}
	if sc.cwd != "" {
		if r.cwd == "" {
			return false, false
		}
		if !strings.Contains(strings.ToLower(r.cwd), sc.cwd) {
			return false, false
		}
	}
	return true, false
}

// parseWhen reads a --since/--until value in any of three shapes: a calendar day
// (2026-08-17), a full RFC3339 timestamp, or an age counted back from now
// (7d, 36h, 90m). Days are read in UTC so the flag agrees with the date printed
// on each result, which is also UTC.
//
// endOfDay extends a day-only value to that day's last instant, so
// `--since 2026-08-17 --until 2026-08-17` means the whole of the 17th rather
// than its first millisecond.
func parseWhen(s string, now time.Time, endOfDay bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		t = t.UTC()
		if endOfDay {
			t = t.Add(24*time.Hour - time.Nanosecond)
		}
		return t, nil
	}
	if d, err := parseAge(s); err == nil {
		return now.Add(-d).UTC(), nil
	}
	return time.Time{}, fmt.Errorf(
		"could not read %q as a time — use a day (2026-08-17), a timestamp (2026-08-17T14:00:00Z), or an age (7d, 36h, 90m)", s)
}

// parseAge reads "7d"/"36h"/"90m" as a duration. Go's own ParseDuration has no
// day unit, and a day is the natural unit for "when did I have that chat".
func parseAge(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("bad day count %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("bad age %q", s)
	}
	return d, nil
}

// staleDevices lists the OTHER machines whose last sync is old enough that this
// search cannot claim to cover them, most stale last.
func (sc sessionScope) staleDevices() []devices.Device {
	var out []devices.Device
	for _, d := range sc.devices {
		if d.Name == sc.me {
			continue
		}
		if sc.now.Sub(d.LastSync) > staleAfter {
			out = append(out, d)
		}
	}
	return out
}

// renderCoverage prints the device roster and warns about every machine whose
// sessions this search could not see. It prints nothing when this machine is the
// only one on the registry — there is then no elsewhere for a chat to be.
func renderCoverage(out io.Writer, sc sessionScope) {
	others := 0
	for _, d := range sc.devices {
		if d.Name != sc.me {
			others++
		}
	}
	if others == 0 {
		return
	}

	parts := make([]string, 0, len(sc.devices))
	for _, d := range sc.devices {
		label := d.Name + " " + humanizeSinceAt(d.LastSync, sc.now)
		if d.Name == sc.me {
			label += " (this)"
		}
		parts = append(parts, label)
	}
	fmt.Fprintf(out, "%s\n", DimStyle.Render("devices  "+strings.Join(parts, " · ")))

	for _, d := range sc.staleDevices() {
		fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf(
			"%s has not synced since %s — anything it recorded after that is not searchable here",
			d.Name, d.LastSync.UTC().Format("2006-01-02 15:04 UTC"))))
		fmt.Fprintf(out, "%s\n", DimStyle.Render("  run `clauderig sync` there, then `clauderig pull` here"))
	}
}

// loadDevices reads the synced device registry, best-effort: search is a report,
// and a missing or unreadable registry means only that there is nothing to say
// about other machines — never that the search should fail.
func loadDevices() []devices.Device {
	staging, err := config.StagingDir()
	if err != nil {
		return nil
	}
	reg, err := devices.Load(staging)
	if err != nil {
		return nil
	}
	return reg.List()
}
