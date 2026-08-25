package commands

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
)

// staleAfter is how long another machine may go without syncing before search
// says so out loud. A day is where "I searched everywhere" stops being true for
// a laptop that has been shut since yesterday.
const staleAfter = 24 * time.Hour

// maxAgeDays is the largest "<n>d" an age can express: a time.Duration is int64
// nanoseconds, which runs out at ~292 years.
const maxAgeDays = int64(math.MaxInt64 / (24 * int64(time.Hour)))

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
	// account is a resolved accountUuid from --account; only sessions the ledger
	// attributes to it survive. Attribution is recorded at sync time and cannot
	// be reconstructed afterwards, so sessions synced before it existed have
	// none and are reported as such rather than quietly assumed to match.
	account string
	// devices is the synced registry; empty when there is no staging repo (or
	// when --live took it out of scope), which correctly prints no footer.
	devices []devices.Device
	// ledger is the permanent session index unioned across devices, keyed by
	// session id. Empty when there is no staging repo or under --live.
	ledger map[string]ledger.Entry
	// me is this machine's name.
	me string
	// liveInScope reports that this machine's live roots were searched. It gates
	// whether THIS device is exempt from the staleness warning: normally its
	// ~/.claude is scanned directly, so its sync age hides nothing — but under
	// --repo the live roots are not searched at all, and everything it has not
	// yet synced is just as invisible as another machine's.
	liveInScope bool
	// devicesUnavailable reports that the registry could not be read, as opposed
	// to naming no other machines. The two look identical in the output otherwise,
	// and they mean opposite things: one is "nothing to say", the other is
	// "coverage could not be established".
	devicesUnavailable bool
	now                time.Time
}

// filtering reports whether any narrowing flag was set.
func (sc sessionScope) filtering() bool {
	return !sc.since.IsZero() || !sc.until.IsZero() || sc.cwd != "" || sc.account != ""
}

// dropped says WHY keep() rejected a session, when the reason is that the
// session lacks the information the filter needs rather than that it failed the
// filter. Those two look identical in a shrunken result set and mean opposite
// things: "no such session" versus "cannot tell".
type dropped int

const (
	droppedByFilter     dropped = iota // genuinely outside the filter
	droppedUndated                     // no date to place in a time window
	droppedUnattributed                // no recorded account to match --account
)

// keep decides whether one session survives the filters. A session that lacks
// what a filter needs is dropped rather than waved through: no usable date
// cannot honestly be placed in a time window, no resolved cwd cannot be matched
// against --cwd, and an attribution the ledger never recorded cannot be matched
// against --account.
//
// The second return distinguishes only the two that are worth reporting —
// undated and unattributed — because those are permanent properties of the
// session rather than a verdict on it, and a caller that stayed silent about
// them would shrink the result set for a reason the user cannot see. A missing
// cwd is reported as an ordinary filter miss: unlike the other two, it is
// almost always a transcript this run simply could not read a path from, not a
// standing fact about the session.
func (sc sessionScope) keep(r *sessResult) (ok bool, why dropped) {
	if !sc.since.IsZero() || !sc.until.IsZero() {
		if r.when.IsZero() {
			return false, droppedUndated
		}
		if !sc.since.IsZero() && r.when.Before(sc.since) {
			return false, droppedByFilter
		}
		if !sc.until.IsZero() && r.when.After(sc.until) {
			return false, droppedByFilter
		}
	}
	if sc.cwd != "" {
		if r.cwd == "" {
			return false, droppedByFilter
		}
		if !strings.Contains(strings.ToLower(r.cwd), sc.cwd) {
			return false, droppedByFilter
		}
	}
	if sc.account != "" {
		if r.led.Account == "" {
			return false, droppedUnattributed
		}
		if !strings.EqualFold(r.led.Account, sc.account) {
			return false, droppedByFilter
		}
	}
	return true, droppedByFilter
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
		n, err := strconv.ParseInt(strings.TrimSuffix(s, "d"), 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("bad day count %q", s)
		}
		// A Duration is int64 nanoseconds, so a large day count silently wraps
		// NEGATIVE — and a negative age becomes a FUTURE cutoff that hides every
		// result while looking like a perfectly valid flag. Reject it instead.
		if n > maxAgeDays {
			return 0, fmt.Errorf("%q is too far back — the most this can express is %dd", s, maxAgeDays)
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
		if d.Name == sc.me && sc.liveInScope {
			continue // its live tree was searched directly
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
	if sc.devicesUnavailable {
		fmt.Fprintf(out, "%s\n", WarnStyle.Render(
			"device coverage unavailable — the synced device registry could not be read"))
		return
	}
	others := 0
	for _, d := range sc.devices {
		if d.Name != sc.me {
			others++
		}
	}
	// With the live roots out of scope (--repo), this machine's own sync age
	// bounds the search too, so a one-device registry still has something to say.
	if others == 0 && sc.liveInScope {
		return
	}
	if len(sc.devices) == 0 {
		return
	}

	parts := make([]string, 0, len(sc.devices))
	for _, d := range sc.devices {
		label := d.Name + " " + humanizeSinceAt(d.LastSync, sc.now)
		if d.Name == sc.me {
			label += " (this)"
			if sc.liveInScope {
				label += ", searched live"
			}
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

// loadLedger reads the permanent session index, best-effort for the same reason
// as loadDevices: it can only ever add answers, never justify withholding them.
func loadLedger() map[string]ledger.Entry {
	staging, err := config.StagingDir()
	if err != nil {
		return nil
	}
	return ledger.LoadAll(staging)
}

// loadDevices reads the synced device registry, best-effort: search is a report,
// and a missing or unreadable registry means only that there is nothing to say
// about other machines — never that the search should fail.
func loadDevices() (list []devices.Device, ok bool) {
	staging, err := config.StagingDir()
	if err != nil {
		return nil, false
	}
	reg, err := devices.Load(staging)
	if err != nil {
		return nil, false
	}
	return reg.List(), true
}

// activeFilters names the narrowing flags actually set, so a "everything was
// excluded" hint points at the flag that did it rather than a fixed list.
func (sc sessionScope) activeFilters() []string {
	var f []string
	if !sc.since.IsZero() {
		f = append(f, "--since")
	}
	if !sc.until.IsZero() {
		f = append(f, "--until")
	}
	if sc.cwd != "" {
		f = append(f, "--cwd")
	}
	if sc.account != "" {
		f = append(f, "--account")
	}
	if len(f) == 0 {
		return []string{"the filters"}
	}
	return f
}
