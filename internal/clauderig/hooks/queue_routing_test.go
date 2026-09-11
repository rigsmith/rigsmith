package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckSyncRouting(t *testing.T) {
	for _, tc := range []string{"standard", "missing", "stale", "duplicate", "scoped", "matcher-type", "command-type", "disabled", "disable-string", "disable-null", "disable-number", "explicit-enabled", "unrelated"} {
		t.Run(tc, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if _, err := Install(path, SyncPlans()); err != nil {
				t.Fatal(err)
			}
			s, err := load(path)
			if err != nil {
				t.Fatal(err)
			}
			h := hooksMap(s)
			groups := h["Stop"].([]any)
			g := groups[0].(map[string]any)
			command := g["hooks"].([]any)[0].(map[string]any)
			switch tc {
			case "missing":
				delete(h, "Stop")
			case "stale":
				command["command"] = "clauderig sync"
			case "duplicate":
				h["Stop"] = append(groups, newGroup(SyncPlans()[1]))
			case "scoped":
				g["matcher"] = "specific"
			case "matcher-type":
				g["matcher"] = true
			case "command-type":
				command["type"] = "prompt"
			case "disabled":
				s["disableAllHooks"] = true
			case "disable-string":
				s["disableAllHooks"] = "false"
			case "disable-null":
				s["disableAllHooks"] = nil
			case "disable-number":
				s["disableAllHooks"] = 0
			case "explicit-enabled":
				s["disableAllHooks"] = false
			case "unrelated":
				h["Stop"] = append(groups, newGroup(Plan{Command: "other-tool"}))
			}
			if err := save(path, s); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			err = CheckSyncRouting(path)
			wantOK := tc == "standard" || tc == "unrelated" || tc == "explicit-enabled"
			if (err == nil) != wantOK {
				t.Fatal(tc, err)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("checker edited settings")
			}
		})
	}
}
