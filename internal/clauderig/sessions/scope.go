package sessions

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
)

// StaleAfter is how long another machine may go without syncing before a
// listing says so out loud. A day is where "I looked everywhere" stops being
// true for a laptop that has been shut since yesterday.
const StaleAfter = 24 * time.Hour

// maxAgeDays is the largest "<n>d" an age can express: a time.Duration is int64
// nanoseconds, which runs out at ~292 years.
const maxAgeDays = int64(math.MaxInt64 / (24 * int64(time.Hour)))

// Scope is everything that narrows a listing and everything that bounds what it
// could possibly have seen: the since/until/cwd/account filters, plus the synced
// device registry behind the coverage footer.
//
// The coverage half exists because absence is the answer people act on — "that
// chat is gone" — and it is only sound when the store is complete. A machine
// that has not synced since Tuesday makes every Wednesday session invisible
// here, and nothing in the results says so on its own.
type Scope struct {
	CaseSensitive bool
	// Since/Until bound a session's recency time (its last transcript record;
	// see [Row.When]) — the same instant a result line dates.
	Since time.Time
	Until time.Time
	// Cwd is a case-insensitive substring of the session's project directory.
	Cwd string
	// Account is a resolved accountUuid; only sessions the ledger attributes to
	// it survive. Attribution is recorded at sync time and cannot be
	// reconstructed afterwards, so sessions synced before it existed have none
	// and are reported as such rather than quietly assumed to match.
	Account string
	// Devices is the synced registry; empty when there is no staging repo (or
	// when the live-only scope took it out), which correctly prints no footer.
	Devices []devices.Device
	// Ledger is the permanent session index unioned across devices, keyed by
	// session id. Empty when there is no staging repo or under live-only.
	Ledger map[string]ledger.Entry
	// Me is this machine's name.
	Me string
	// LiveInScope reports that this machine's live roots were read. It gates
	// whether THIS device is exempt from the staleness warning: normally its
	// ~/.claude is scanned directly, so its sync age hides nothing — but with
	// the live roots out of scope, everything it has not yet synced is just as
	// invisible as another machine's.
	LiveInScope bool
	// DevicesUnavailable reports that the registry could not be read, as opposed
	// to naming no other machines. The two look identical in the output
	// otherwise, and they mean opposite things: one is "nothing to say", the
	// other is "coverage could not be established".
	DevicesUnavailable bool
	Now                time.Time
}

// Filtering reports whether any narrowing filter was set.
func (sc Scope) Filtering() bool {
	return !sc.Since.IsZero() || !sc.Until.IsZero() || sc.Cwd != "" || sc.Account != ""
}

// Dropped says WHY [Scope.Keep] rejected a session, when the reason is that the
// session lacks the information the filter needs rather than that it failed the
// filter. Those two look identical in a shrunken result set and mean opposite
// things: "no such session" versus "cannot tell".
type Dropped int

const (
	DroppedByFilter     Dropped = iota // genuinely outside the filter
	DroppedUndated                     // no date to place in a time window
	DroppedUnattributed                // no recorded account to match against
)

// Keep decides whether one session survives the filters. A session that lacks
// what a filter needs is dropped rather than waved through: no usable date
// cannot honestly be placed in a time window, no resolved cwd cannot be matched
// against a directory filter, and an attribution the ledger never recorded
// cannot be matched against an account.
//
// The second return distinguishes only the two worth reporting — undated and
// unattributed — because those are permanent properties of the session rather
// than a verdict on it, and a caller that stayed silent about them would shrink
// the result set for a reason the user cannot see. A missing cwd is reported as
// an ordinary filter miss: unlike the other two, it is almost always a
// transcript this run simply could not read a path from, not a standing fact
// about the session.
//
// It takes plain values rather than a row so the search command, whose own
// result type carries hit-tracking this package has no business knowing about,
// can apply exactly the same rules.
func (sc Scope) Keep(when time.Time, cwd, account string) (ok bool, why Dropped) {
	if !sc.Since.IsZero() || !sc.Until.IsZero() {
		if when.IsZero() {
			return false, DroppedUndated
		}
		if !sc.Since.IsZero() && when.Before(sc.Since) {
			return false, DroppedByFilter
		}
		if !sc.Until.IsZero() && when.After(sc.Until) {
			return false, DroppedByFilter
		}
	}
	if sc.Cwd != "" {
		if cwd == "" {
			return false, DroppedByFilter
		}
		if !strings.Contains(strings.ToLower(cwd), sc.Cwd) {
			return false, DroppedByFilter
		}
	}
	if sc.Account != "" {
		if account == "" {
			return false, DroppedUnattributed
		}
		if !strings.EqualFold(account, sc.Account) {
			return false, DroppedByFilter
		}
	}
	return true, DroppedByFilter
}

// ActiveFilters names the narrowing filters actually set, so an "everything was
// excluded" hint can point at the one that did it rather than a fixed list.
func (sc Scope) ActiveFilters() []string {
	var f []string
	if !sc.Since.IsZero() {
		f = append(f, "--since")
	}
	if !sc.Until.IsZero() {
		f = append(f, "--until")
	}
	if sc.Cwd != "" {
		f = append(f, "--cwd")
	}
	if sc.Account != "" {
		f = append(f, "--account")
	}
	if len(f) == 0 {
		return []string{"the filters"}
	}
	return f
}

// StaleDevices lists the OTHER machines whose last sync is old enough that this
// listing cannot claim to cover them, most stale last.
func (sc Scope) StaleDevices() []devices.Device {
	var out []devices.Device
	for _, d := range sc.Devices {
		if d.Name == sc.Me && sc.LiveInScope {
			continue // its live tree was read directly
		}
		if sc.Now.Sub(d.LastSync) > StaleAfter {
			out = append(out, d)
		}
	}
	return out
}

// ParseWhen reads a since/until value in any of three shapes: a calendar day
// (2026-08-17), a full RFC3339 timestamp, or an age counted back from now
// (7d, 36h, 90m). Days are read in UTC so the value agrees with the date printed
// on each result, which is also UTC.
//
// endOfDay extends a day-only value to that day's last instant, so
// `--since 2026-08-17 --until 2026-08-17` means the whole of the 17th rather
// than its first millisecond.
func ParseWhen(s string, now time.Time, endOfDay bool) (time.Time, error) {
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
	if d, err := ParseAge(s); err == nil {
		return now.Add(-d).UTC(), nil
	}
	return time.Time{}, fmt.Errorf(
		"could not read %q as a time — use a day (2026-08-17), a timestamp (2026-08-17T14:00:00Z), or an age (7d, 36h, 90m)", s)
}

// ParseAge reads "7d"/"36h"/"90m" as a duration. Go's own ParseDuration has no
// day unit, and a day is the natural unit for "when did I have that chat".
func ParseAge(s string) (time.Duration, error) {
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

// LoadLedger reads the permanent session index, best-effort for the same reason
// as [LoadDevices]: it can only ever add answers, never justify withholding them.
func LoadLedger() map[string]ledger.Entry {
	staging, err := config.StagingDir()
	if err != nil {
		return nil
	}
	return ledger.LoadAll(staging)
}

// LoadDevices reads the synced device registry, best-effort: a listing is a
// report, and a missing or unreadable registry means only that there is nothing
// to say about other machines — never that the listing should fail.
func LoadDevices() (list []devices.Device, ok bool) {
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
