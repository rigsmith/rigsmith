package adapter_test

import (
	"reflect"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

// These cases pin the intentionally different capture, retention and merge
// scopes. In particular, moving the rules must not turn every JSONL into a
// chunkable transcript or every memory directory into age-exempt project memory.
func TestFilePoliciesPreserveDistinctScopes(t *testing.T) {
	for _, tc := range []struct {
		root, rel string
		kind      adapter.Kind
		retention adapter.Retention
		transform adapter.Transform
		chunk     bool
		merge     adapter.MergeStrategy
	}{
		{"cli", "settings.json", adapter.Settings, adapter.Keep, adapter.JSON, false, adapter.NewestSnapshot},
		{"cli", "CLAUDE.md", adapter.Other, adapter.Keep, adapter.Copy, false, adapter.NewestSnapshot},
		{"cli", "skills/tool/data.jsonl", adapter.Other, adapter.Keep, adapter.Copy, false, adapter.UnionText},
		{"cli", "projects/p/session.jsonl", adapter.Transcript, adapter.ProjectAge, adapter.ConversationText, true, adapter.UnionText},
		{"cli", "projects/p/session/subagents/agent.jsonl", adapter.Transcript, adapter.ProjectAge, adapter.ConversationText, true, adapter.UnionText},
		{"cli", "projects/p/session.JSONL", adapter.Other, adapter.ProjectAge, adapter.ConversationText, false, adapter.UnionText},
		{"cli", "projects/p/tool-result.log", adapter.Other, adapter.ProjectAge, adapter.ConversationText, false, adapter.NewestSnapshot},
		{"cli", "projects/p/output", adapter.Other, adapter.ProjectAge, adapter.ConversationText, false, adapter.NewestSnapshot},
		{"cli", "projects/p/image.png", adapter.Other, adapter.ProjectAge, adapter.ConversationText, false, adapter.NewestSnapshot},
		{"cli", "projects/p/state.json", adapter.Settings, adapter.ProjectAge, adapter.JSON, false, adapter.NewestSnapshot},
		{"cli", "projects/p/state.JSON", adapter.Other, adapter.ProjectAge, adapter.ConversationText, false, adapter.NewestSnapshot},
		{"cli", "projects/p/memory/MEMORY.md", adapter.Memory, adapter.Keep, adapter.ConversationText, false, adapter.UnionText},
		{"cli", "projects/p/memory/data.jsonl", adapter.Memory, adapter.Keep, adapter.ConversationText, false, adapter.UnionText},
		{"cli", "projects/p/memory/data.json", adapter.Memory, adapter.Keep, adapter.JSON, false, adapter.NewestSnapshot},
		{"cli", "projects/p/session/memory/note.md", adapter.Other, adapter.ProjectAge, adapter.ConversationText, false, adapter.UnionText},
		{"cli", "skills/tool/memory/note.md", adapter.Other, adapter.Keep, adapter.Copy, false, adapter.UnionText},
		{"cli", "projects-old/p/session.jsonl", adapter.Other, adapter.Keep, adapter.Copy, false, adapter.UnionText},
		{"custom", "projects/p/session.jsonl", adapter.Transcript, adapter.ProjectAge, adapter.ConversationText, true, adapter.UnionText},
		{"desktop", "claude-code-sessions/a/o/local_s.json", adapter.DesktopCodeSidecar, adapter.Keep, adapter.JSON, false, adapter.NewestSnapshot},
		{"desktop", "local-agent-mode-sessions/a/o/local_s.json", adapter.DesktopCoworkSidecar, adapter.Keep, adapter.JSON, false, adapter.NewestSnapshot},
		{"desktop@work", "data/claude-code-sessions/a/o/local_s.json", adapter.DesktopCodeSidecar, adapter.Keep, adapter.JSON, false, adapter.NewestSnapshot},
		{"desktop@work", "profile.json", adapter.ProfileMetadata, adapter.Keep, adapter.JSON, false, adapter.NewestSnapshot},
		{"cli", "claude-code-sessions/a/o/local_s.json", adapter.Settings, adapter.Keep, adapter.JSON, false, adapter.NewestSnapshot},
	} {
		t.Run(tc.root+"/"+tc.rel, func(t *testing.T) {
			got := adapter.Classify(tc.root, tc.rel)
			if got.RootID != tc.root || got.Rel != tc.rel || got.Kind != tc.kind || got.Retention != tc.retention || got.Transform != tc.transform || got.ChunkEligible != tc.chunk || got.Merge.Strategy != tc.merge {
				t.Fatalf("policy: %+v; want kind=%v retention=%v transform=%v chunk=%v merge=%v", got, tc.kind, tc.retention, tc.transform, tc.chunk, tc.merge)
			}
		})
	}
}

func TestDesktopKeepKeysStayScopedToConfig(t *testing.T) {
	for _, tc := range []struct {
		root, rel string
		filtered  bool
	}{
		{"desktop", "config.json", true},
		{"desktop@work", "data/config.json", true},
		{"desktop@work", "profile.json", false},
		{"desktop", "claude_desktop_config.json", false},
		{"cli", "config.json", false},
		{"custom", "config.json", false},
		{"desktop", "nested/config.json", false},
		{"desktop@", "data/config.json", false},
	} {
		got := adapter.Classify(tc.root, tc.rel).KeepKeys
		var want []string
		if tc.filtered {
			want = []string{"preferences", "locale", "userThemeMode"}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s/%s: keys=%v, want %v", tc.root, tc.rel, got, want)
		}
	}
}

func TestMergePoliciesPreserveMetadataAndLegacyCaseRules(t *testing.T) {
	for _, tc := range []struct {
		path     string
		strategy adapter.MergeStrategy
		index    bool
		dedup    bool
	}{
		{manifest.FileName, adapter.UnionManifest, false, false},
		{devices.FileName, adapter.UnionDevices, false, false},
		{"cli/" + manifest.FileName, adapter.NewestSnapshot, false, false},
		{"cli/projects/p/s.jsonl", adapter.UnionText, true, true},
		{"cli/skills/tool/data.JSONL", adapter.UnionText, false, true},
		{"cli/projects/p/memory/note.MARKDOWN", adapter.UnionText, false, false},
		{"cli/projects/p/Memory/note.md", adapter.NewestSnapshot, false, false},
		{"cli/projects/p/memory.json", adapter.NewestSnapshot, false, false},
		{"cli/skills/tool/SKILL.md", adapter.NewestSnapshot, false, false},
	} {
		got := adapter.ClassifyMerge(tc.path)
		if got.Strategy != tc.strategy || got.CheckChunkIndex != tc.index || got.DeduplicateRecords != tc.dedup {
			t.Errorf("%s: %+v, want strategy=%v index=%v dedup=%v", tc.path, got, tc.strategy, tc.index, tc.dedup)
		}
	}
}

func TestRetainedSnapshotPaths(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"cli/settings.json", true}, {"cli/settings.local.json", true}, {"cli/CLAUDE.md", true},
		{"cli/skills/example/SKILL.md", true}, {"cli/plugins/data/state.bin", true},
		{"desktop/config.json", true}, {"desktop@generic/data/config.json", true}, {"desktop@generic/profile.json", true},
		{"desktop/local-agent-mode-sessions/org/user/local_session.json", true},
		{"desktop/local-agent-mode-sessions/org/user/local_session/upload.txt", false},
		{"cli/plugins/cache/state.json", false}, {"cli/projects/p/file-history/snapshot", false},
		{"cli/skills/tool/node_modules/config.json", false}, {"cli/projects/p/s.jsonl", false},
		{"cli/projects/p/memory/settings.json", false}, {"cli/projects/p/s.jsonl.chunks/x.part", false},
		{"cli/history.jsonl", false}, {"custom/settings.json", false}, {"desktop@/profile.json", false},
		{"clauderig-storage.json", false}, {".gitattributes", false}, {"cli/skills/example/.gitattributes", false}, {"cli/skills/example/.GITATTRIBUTES", false},
	} {
		if got := adapter.RetainedSnapshot(tc.path); got != tc.want {
			t.Errorf("%s: %v want %v", tc.path, got, tc.want)
		}
	}
}
