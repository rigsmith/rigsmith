package adapter_test

import (
	"reflect"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

func TestRootsPreserveOrderLocationsAndProfileEnablement(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		configured := []config.Root{
			{ID: "custom", Enabled: true, Location: pathmap.Cascade{Portable: "$HOME/custom"}},
			{ID: "desktop", Enabled: enabled, Location: pathmap.Cascade{Portable: "$HOME/desktop"}},
			{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: "$HOME/.claude"}},
		}
		before := append([]config.Root(nil), configured...)
		roots := adapter.Roots(&config.Config{Roots: configured}, []string{"work", "personal"})
		if len(roots) != 5 || !reflect.DeepEqual(configured, before) {
			t.Fatalf("configured roots changed: %+v", roots)
		}
		for i := range configured {
			if !reflect.DeepEqual(roots[i].Root, configured[i]) {
				t.Fatalf("root %d moved or changed: %+v", i, roots[i])
			}
		}
		for i, name := range []string{"work", "personal"} {
			r := roots[3+i]
			if r.ID != "desktop@"+name || r.Kind != adapter.DesktopProfileRoot || r.Enabled != enabled || r.Location.Portable != "$HOME/.clauderig/desktop/"+name {
				t.Fatalf("profile root: %+v", r)
			}
		}
	}
	roots := adapter.Roots(&config.Config{}, []string{"work"})
	if len(roots) != 1 || roots[0].Enabled {
		t.Fatalf("profile enabled without Desktop configuration: %+v", roots)
	}
}

func TestRootAllowlistRetainsExclusions(t *testing.T) {
	for _, tc := range []struct {
		root, rel string
		allowed   bool
	}{
		{"custom", "settings.json", true},
		{"custom", ".credentials.json", false},
		{"cli", "skills/tool/node_modules/pkg/index.js", false},
		{"cli", "projects/p/file-history/history.json", false},
		{"cli", "projects/p/memory/MEMORY.md", true},
		{"desktop", "Cookies", false},
		{"desktop", "local-agent-mode-sessions/a/o/local_s.json", true},
		{"desktop", "local-agent-mode-sessions/a/o/local_s/upload.pdf", false},
		{"desktop@work", "profile.json", true},
		{"desktop@work", "config.json", false},
		{"desktop@work", "data/config.json", true},
		{"desktop@work", "data/Cookies", false},
		{"desktop@work", "data/local-agent-mode-sessions/a/o/local_s/upload.pdf", false},
	} {
		r := adapter.DescribeRoot(config.Root{ID: tc.root})
		if got := r.Allowlist.Match(tc.rel); got != tc.allowed {
			t.Errorf("%s/%s: included=%v, want %v", tc.root, tc.rel, got, tc.allowed)
		}
		if got := r.Classify(tc.rel); !reflect.DeepEqual(got, adapter.Classify(tc.root, tc.rel)) {
			t.Errorf("configured and staged classification disagree: %+v", got)
		}
	}
}
