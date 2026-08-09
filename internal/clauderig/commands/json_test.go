package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
)

// The contract of every --json flag: stdout is a JSON document and nothing
// else. A styled banner ahead of it breaks every consumer that pipes this, and
// that is exactly what shipped in the first cut of `search --json`.
func TestStatusJSONIsParseable(t *testing.T) {
	info := status.Info{
		Machine:    config.Machine{Name: "Air13", OS: "macos"},
		Remote:     "https://example.invalid/claude.git",
		HasStaging: true,
		LastSync:   "abc1234 2 minutes ago — clauderig sync: Air13",
		Divergence: gitrepo.Divergence{Ref: "origin/main", Tracked: true, Behind: 65},
		Roots:      []status.RootInfo{{ID: "cli", Files: 1418, Present: true}},
		Hooks:      []string{"SessionStart", "Stop"},
		Devices:    []devices.Device{{Name: "Air13", OS: "macos", LastSync: time.Now()}},
	}

	var buf bytes.Buffer
	if err := emitStatusJSON(&buf, info, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, buf.String())
	}

	// The health verdict is the point of the document — it's what a caller
	// would otherwise have to re-derive from ahead/behind by hand.
	if doc["level"] != "amber" || doc["reason"] != "behind" {
		t.Errorf("level/reason = %v/%v, want amber/behind", doc["level"], doc["reason"])
	}
	// The gathered struct is embedded verbatim, so its fields sit at the top level.
	for _, key := range []string{"machine", "remote", "hasStaging", "lastSync", "dirty", "divergence", "roots", "hooks", "devices", "summary"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("missing %q:\n%s", key, buf.String())
		}
	}
	div, _ := doc["divergence"].(map[string]any)
	if div["behind"] != float64(65) || div["ref"] != "origin/main" {
		t.Errorf("divergence not tagged through: %v", div)
	}
	// Nothing recorded yet → the key is absent rather than null-ish noise.
	if _, ok := doc["lastRun"]; ok {
		t.Errorf("lastRun should be omitted when nothing is journalled: %s", buf.String())
	}
}

// The last journalled run rides along, so a script can tell "healthy" from
// "healthy but the last sync refused to push a credential".
func TestStatusJSONCarriesLastRun(t *testing.T) {
	staging := t.TempDir()
	if err := journal.Append(staging, journal.Record{
		Machine: "Air13", Op: journal.OpSync, Outcome: journal.OutcomeRefused,
		Leaks: []journal.Leak{{Path: "env.KEY", Kind: "anthropic-key"}},
	}); err != nil {
		t.Fatal(err)
	}

	info := status.Info{HasStaging: true, Divergence: gitrepo.Divergence{Ref: "origin/main", Tracked: true}}
	var buf bytes.Buffer
	if err := emitStatusJSON(&buf, info, staging); err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Level   string          `json:"level"`
		Reason  string          `json:"reason"`
		LastRun *journal.Record `json:"lastRun"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.LastRun == nil || doc.LastRun.Outcome != journal.OutcomeRefused {
		t.Fatalf("lastRun not carried: %s", buf.String())
	}
	// A tripwire refusal leaves the repo looking perfectly level, so the level
	// has to come from the journal or it reads green.
	if doc.Level != "red" || doc.Reason != "last-run-refused" {
		t.Errorf("level/reason = %q/%q, want red/last-run-refused", doc.Level, doc.Reason)
	}
}

func TestSearchJSONShape(t *testing.T) {
	me := config.Machine{Name: "Air13", OS: "macos", Home: "/Users/jane"}
	results := []*sessResult{
		{
			id: "aaaaaaaa-1111-2222-3333-444444444444", matches: 12, titleMatch: true,
			meta:    session.Meta{Title: "Wiring the tray icon", Cwd: "/Users/jane/Git/demo", Model: "claude-opus-5"},
			hasMeta: true,
			when:    time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC),
			// cliLive false → not resumable, and no command offered.
		},
	}

	var buf bytes.Buffer
	if err := emitSearchJSON(&buf, me, "tray icon", results, 1109, 3); err != nil {
		t.Fatal(err)
	}
	var doc SearchJSON
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}

	if doc.Query != "tray icon" || doc.Scanned != 1109 || doc.Skipped != 3 {
		t.Errorf("counts lost: %+v", doc)
	}
	if len(doc.Sessions) != 1 {
		t.Fatalf("got %d sessions", len(doc.Sessions))
	}
	h := doc.Sessions[0]
	if h.Matches != 12 || !h.TitleMatch {
		t.Errorf("hit fields lost: %+v", h)
	}
	// A session absent from the live CLI root can't be resumed, so no command
	// is offered — one that would fail is worse than none.
	if h.Resumable || h.Resume != "" {
		t.Errorf("offered a resume command for a non-live session: %+v", h)
	}
	if !strings.Contains(buf.String(), `"scanned"`) {
		t.Error("scanned count should be present even with hits")
	}
}
