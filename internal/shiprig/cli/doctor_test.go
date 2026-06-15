package cli

import (
	"context"
	"testing"

	"github.com/rigsmith/rigsmith/core/doctor"
)

func TestPublishResults_MapsEcosystems(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // nothing installed ⇒ deterministic warns

	byName := map[string]doctor.Result{}
	for _, r := range publishResults([]string{"dotnet", "node", "cargo", "go", "regex"}) {
		byName[r.Name] = r
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
