package health

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
)

// synced is a snapshot of a healthy machine; each test perturbs one thing.
func synced() status.Info {
	return status.Info{
		HasStaging: true,
		Divergence: gitrepo.Divergence{Ref: "origin/main", Tracked: true},
	}
}

func TestOfLevels(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*status.Info)
		wantLevel  Level
		wantReason Reason
		wantAction string
	}{
		{
			name:       "level withremote is green",
			mutate:     func(*status.Info) {},
			wantLevel:  Green,
			wantReason: ReasonSynced,
		},
		{
			// The incident state. Behind-only is recoverable by a plain
			// ff pull, so it is amber, not red.
			name:       "behind only is amber and offers pull",
			mutate:     func(i *status.Info) { i.Divergence.Behind = 65 },
			wantLevel:  Amber,
			wantReason: ReasonBehind,
			wantAction: "clauderig pull",
		},
		{
			name:       "ahead only is amber and offers sync",
			mutate:     func(i *status.Info) { i.Divergence.Ahead = 4 },
			wantLevel:  Amber,
			wantReason: ReasonAhead,
			wantAction: "clauderig sync",
		},
		{
			// Both sides moved: ff-only pull fails, so nothing self-heals.
			name: "diverged is red even when it would merge cleanly",
			mutate: func(i *status.Info) {
				i.Divergence.Ahead, i.Divergence.Behind = 15, 65
			},
			wantLevel:  Red,
			wantReason: ReasonDiverged,
		},
		{
			name: "diverged with conflict is red",
			mutate: func(i *status.Info) {
				i.Divergence.Ahead, i.Divergence.Behind = 15, 65
				i.Divergence.Conflict = true
			},
			wantLevel:  Red,
			wantReason: ReasonConflict,
		},
		{
			name:       "mid-merge is red",
			mutate:     func(i *status.Info) { i.Divergence.Merging = true },
			wantLevel:  Red,
			wantReason: ReasonMerging,
		},
		{
			name:       "never fetched is amber",
			mutate:     func(i *status.Info) { i.Divergence.Tracked = false },
			wantLevel:  Amber,
			wantReason: ReasonNeverFetched,
			wantAction: "clauderig pull",
		},
		{
			name:       "no staging repo is amber and points at init",
			mutate:     func(i *status.Info) { i.HasStaging = false },
			wantLevel:  Amber,
			wantReason: ReasonUnconfigured,
			wantAction: "clauderig init",
		},
		{
			name:       "dirty staging while level is amber",
			mutate:     func(i *status.Info) { i.Dirty = true },
			wantLevel:  Amber,
			wantReason: ReasonUncommitted,
			wantAction: "clauderig sync",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := synced()
			tc.mutate(&info)
			got := Of(info, journal.Record{})
			if got.Level != tc.wantLevel {
				t.Errorf("level = %v, want %v (%q)", got.Level, tc.wantLevel, got.Summary)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("reason = %v, want %v (%q)", got.Reason, tc.wantReason, got.Summary)
			}
			if got.Action != tc.wantAction {
				t.Errorf("action = %q, want %q", got.Action, tc.wantAction)
			}
			if got.Summary == "" {
				t.Error("summary must never be empty — it is the tooltip")
			}
		})
	}
}

// A half-finished merge has to win over the ahead/behind counts, or the UI would
// offer a pull that cannot possibly succeed.
func TestMergingOutranksDivergence(t *testing.T) {
	info := synced()
	info.Divergence.Ahead, info.Divergence.Behind = 15, 65
	info.Divergence.Conflict = true
	info.Divergence.Merging = true

	got := Of(info, journal.Record{})
	if got.Reason != ReasonMerging {
		t.Fatalf("reason = %v, want ReasonMerging (%q)", got.Reason, got.Summary)
	}
	if got.Action != "" {
		t.Errorf("a mid-merge needs a human, not a command; got action %q", got.Action)
	}
}

// Counts ride along on the report so the window can render them without
// reaching back into status.Info.
func TestReportCarriesCounts(t *testing.T) {
	info := synced()
	info.Divergence.Ahead, info.Divergence.Behind = 15, 65
	got := Of(info, journal.Record{})
	if got.Ahead != 15 || got.Behind != 65 {
		t.Fatalf("ahead/behind = %d/%d, want 15/65", got.Ahead, got.Behind)
	}
	if !strings.Contains(got.Summary, "15 commits ahead") ||
		!strings.Contains(got.Summary, "65 commits behind") {
		t.Fatalf("summary should name both counts, got %q", got.Summary)
	}
}

func TestSingularCommit(t *testing.T) {
	info := synced()
	info.Divergence.Behind = 1
	if got := Of(info, journal.Record{}); !strings.Contains(got.Summary, "1 commit behind") {
		t.Fatalf("want singular phrasing, got %q", got.Summary)
	}
}

func TestTooltip(t *testing.T) {
	r := Of(synced(), journal.Record{})
	if got := r.Tooltip("claudeRig UI"); got != "claudeRig UI — Up to date" {
		t.Fatalf("tooltip = %q", got)
	}
	if got := r.Tooltip(""); got != "Up to date" {
		t.Fatalf("label-less tooltip = %q", got)
	}
}

// Windows silently drops a tooltip over 127 UTF-16 units, so the clamp counts
// surrogate pairs as two rather than trusting rune length.
func TestTooltipClampedForWindows(t *testing.T) {
	r := Report{Summary: strings.Repeat("a", 400)}
	if got := len([]rune(r.Tooltip(""))); got != tooltipMax {
		t.Fatalf("ascii tooltip len = %d, want %d", got, tooltipMax)
	}

	// Every rune is a surrogate pair, so only half as many fit.
	r = Report{Summary: strings.Repeat("😀", 400)}
	got := []rune(r.Tooltip(""))
	if len(got) != tooltipMax/2 {
		t.Fatalf("astral tooltip len = %d runes, want %d", len(got), tooltipMax/2)
	}
	for _, c := range got {
		if c != '😀' {
			t.Fatal("clamp split a surrogate pair")
		}
	}
}

func TestLevelString(t *testing.T) {
	for lvl, want := range map[Level]string{Green: "green", Amber: "amber", Red: "red"} {
		if got := lvl.String(); got != want {
			t.Errorf("Level(%d).String() = %q, want %q", int(lvl), got, want)
		}
	}
}

// The state that had no indicator at all before the journal existed: a machine
// level with its remote whose last run failed. Every divergence check reads
// clean here, so without the journal this is indistinguishable from healthy —
// which is precisely how a Stop-hook sync refused for days in July 2026 while
// every surface said "synced".
func TestLastRunFailedIsRed(t *testing.T) {
	got := Of(synced(), journal.Record{
		Op: journal.OpPull, Outcome: journal.OutcomeFailed,
		Error: "fatal: Not possible to fast-forward, aborting.",
	})
	if got.Level != Red || got.Reason != ReasonLastRunFailed {
		t.Fatalf("level/reason = %v/%v, want Red/ReasonLastRunFailed (%q)", got.Level, got.Reason, got.Summary)
	}
	if !strings.Contains(got.Summary, "pull") || !strings.Contains(got.Summary, "fast-forward") {
		t.Errorf("summary should name the op and the reason, got %q", got.Summary)
	}
}

// A tripwire refusal pushes nothing, so the repo looks perfectly level. It has
// to surface anyway, and it has to say what it caught.
func TestLastRunRefusedIsRed(t *testing.T) {
	got := Of(synced(), journal.Record{
		Op: journal.OpSync, Outcome: journal.OutcomeRefused,
		Leaks: []journal.Leak{{Path: "env.KEY", Kind: "anthropic-key"}, {Path: "mcp.token", Kind: "jwt"}},
	})
	if got.Level != Red || got.Reason != ReasonLastRunRefused {
		t.Fatalf("level/reason = %v/%v, want Red/ReasonLastRunRefused (%q)", got.Level, got.Reason, got.Summary)
	}
	if !strings.Contains(got.Summary, "2 values") {
		t.Errorf("summary should count the findings, got %q", got.Summary)
	}
	if strings.Contains(strings.ToLower(got.Summary), "failed") {
		t.Errorf("a refusal must not read as a failure: %q", got.Summary)
	}
}

// A successful run clears the state — only the newest record counts, so a
// failure that has since been followed by a clean sync is history.
func TestSuccessfulLastRunStaysGreen(t *testing.T) {
	got := Of(synced(), journal.Record{Op: journal.OpSync, Outcome: journal.OutcomeOK, Files: 12})
	if got.Level != Green || got.Reason != ReasonSynced {
		t.Fatalf("level/reason = %v/%v, want Green/ReasonSynced (%q)", got.Level, got.Reason, got.Summary)
	}
}

// Divergence is the more specific, more actionable diagnosis — it usually
// explains the failure — so it outranks a generic "last run failed".
func TestDivergenceOutranksLastRunFailure(t *testing.T) {
	info := synced()
	info.Divergence.Ahead, info.Divergence.Behind = 15, 65
	got := Of(info, journal.Record{Op: journal.OpPull, Outcome: journal.OutcomeFailed, Error: "boom"})
	if got.Reason != ReasonDiverged {
		t.Fatalf("reason = %v, want ReasonDiverged (%q)", got.Reason, got.Summary)
	}
}

// Every Reason needs a distinct token: the window's CSS and `status --json`
// both key off these strings, so a missing one ships "unknown" to both.
func TestReasonTokensAreExhaustiveAndDistinct(t *testing.T) {
	all := []Reason{
		ReasonSynced, ReasonUnconfigured, ReasonNeverFetched, ReasonUncommitted,
		ReasonAhead, ReasonBehind, ReasonDiverged, ReasonConflict, ReasonMerging,
		ReasonLastRunFailed, ReasonLastRunRefused,
	}
	seen := map[string]bool{}
	for _, r := range all {
		tok := r.String()
		if tok == "unknown" {
			t.Errorf("Reason(%d) has no token", int(r))
		}
		if seen[tok] {
			t.Errorf("token %q used twice", tok)
		}
		seen[tok] = true
	}
	// A Reason nobody has named yet must be obviously unnamed, not silently
	// aliased onto a real state.
	if got := Reason(len(all) + 99).String(); got != "unknown" {
		t.Errorf("unnamed Reason = %q, want %q", got, "unknown")
	}
}
