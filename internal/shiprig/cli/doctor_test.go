package cli

import (
	"context"
	"testing"

	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

func findResult(rs []doctor.Result, name string) (doctor.Result, bool) {
	for _, r := range rs {
		if r.Name == name {
			return r, true
		}
	}
	return doctor.Result{}, false
}

// TestPackageResults: the summary is always present; the scope warning fires for
// unplanned (would-publish, no bump) packages only when discovery is unscoped.
func TestPackageResults(t *testing.T) {
	rps := []commands.ReleasePkg{
		{Name: "core", Next: "1.1.0", Bump: "minor"}, // releasing
		{Name: "demo", Private: true},                // private
		{Name: "old", Ignored: true},                 // ignored
		{Name: "fixture-a"},                          // unplanned (drift)
		{Name: "fixture-b"},                          // unplanned (drift)
	}
	noop := func(context.Context) error { return nil }

	// Unscoped (no paths): warning present and fixable.
	rs := packageResults(rps, false, noop)
	if summary, ok := findResult(rs, "release plan"); !ok || summary.Status != doctor.Info {
		t.Fatalf("summary = %+v, want an Info row", summary)
	}
	warn, ok := findResult(rs, "publish scope")
	if !ok || warn.Status != doctor.Warn || warn.Fix == nil {
		t.Fatalf("scope warning = %+v, want a fixable Warn", warn)
	}

	// Scoped (paths set): the user curated discovery, so no drift warning.
	rs = packageResults(rps, true, noop)
	if _, ok := findResult(rs, "publish scope"); ok {
		t.Error("scope warning should be suppressed when paths is configured")
	}
	if _, ok := findResult(rs, "release plan"); !ok {
		t.Error("summary should still be present when paths is configured")
	}

	// Non-interactive (no fix): the warning is still reported, but report-only —
	// no Fix, so `doctor --fix` can't launch the picker and hang.
	rs = packageResults(rps, false, nil)
	warn, ok = findResult(rs, "publish scope")
	if !ok || warn.Status != doctor.Warn {
		t.Fatalf("scope warning should still report without a fix: %+v", warn)
	}
	if warn.Fix != nil || warn.FixLabel != "" {
		t.Error("warning must be report-only when no interactive fix is provided")
	}
}

func TestPublishResults_MapsEcosystems(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // nothing installed ⇒ deterministic warns

	byName := map[string]doctor.Result{}
	for _, r := range publishResults([]string{"dotnet", "node", "cargo", "go", "regex", "tauri", "electron"}) {
		byName[r.Name] = r
	}

	// Desktop ecosystems release as forge artifacts — info rows, never a tool warn.
	for _, id := range []string{"tauri", "electron"} {
		if r, ok := byName[id]; !ok || r.Status != doctor.Info {
			t.Errorf("%s: got %+v, want an Info row", id, r)
		}
	}

	// dotnet/npm/cargo are publish tools; missing ⇒ warn.
	for _, bin := range []string{"dotnet", "npm", "cargo"} {
		if r, ok := byName[bin]; !ok || r.Status != doctor.Warn {
			t.Errorf("%s: got %+v, want a Warn", bin, r)
		}
	}
	// Go publishes via git tags ⇒ an info row, never a tool check.
	if r, ok := byName["go"]; !ok || r.Status != doctor.Info {
		t.Errorf("go: got %+v, want Info", r)
	}
	// An ecosystem with no publish mapping (regex) contributes nothing.
	if _, ok := byName["regex"]; ok {
		t.Error("regex should not produce a publish-tool row")
	}
}

func TestCheckGh_NotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if r := checkGh(context.Background()); r.Status != doctor.Warn {
		t.Fatalf("gh missing: got %+v, want Warn", r)
	}
}
