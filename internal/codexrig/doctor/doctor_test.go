package doctor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/codexrig/hooks"
)

// Status lists every event carrying a codexrig command, not only the sync
// events. Counting them let a stale codexrig event stand in for a missing sync
// event, and "sync hooks: OK" was reported with one absent.
func TestHookChecksNameTheMissingEventRatherThanCountingHeads(t *testing.T) {
	path := filepath.Join(t.TempDir(), hooks.FileName)
	if _, _, err := hooks.Install(path, hooks.SyncPlans()); err != nil {
		t.Fatal(err)
	}
	// Rename one sync event to something stale: same command, wrong key.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	h := doc["hooks"].(map[string]any)
	h["StaleThing"] = h["Stop"]
	delete(h, "Stop")
	out, _ := json.Marshal(doc)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}

	var hookRes *Result
	for _, r := range hookChecks(context.Background(), Env{HooksPath: path}) {
		if r.ID == "user-hooks" {
			r := r
			hookRes = &r
		}
	}
	if hookRes == nil {
		t.Fatal("no sync hooks result")
	}
	if hookRes.Status == OK {
		t.Fatalf("reported OK with the Stop hook missing: %+v", *hookRes)
	}
	if !strings.Contains(hookRes.Detail, "Stop") {
		t.Errorf("the missing event is not named: %q", hookRes.Detail)
	}
}
