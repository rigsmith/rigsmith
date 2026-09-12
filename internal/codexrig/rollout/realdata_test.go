package rollout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gated real-data check. Synthetic fixtures pin the semantics; this pins
// them against what Codex actually writes, which is the only thing that can
// catch a format change. Opt-in because it reads the developer's own sessions
// and reports nothing about their contents.
//
//	CODEXRIG_REAL_DATA=1 go test ./internal/codexrig/rollout/
func TestRealRolloutsAreAllReadable(t *testing.T) {
	if os.Getenv("CODEXRIG_REAL_DATA") == "" {
		t.Skip("set CODEXRIG_REAL_DATA=1 to read this machine's own Codex sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	root := filepath.Join(home, ".codex", "sessions")
	var files []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Skip("no rollouts on this machine")
	}

	var noMeta, noActivity, noTitle, badRel []string
	for _, f := range files {
		rel, _ := filepath.Rel(filepath.Dir(root), f)
		rel = filepath.ToSlash(rel)
		if !IsRolloutRel(rel) {
			badRel = append(badRel, rel)
		}
		m, ok, _ := ReadMeta(f)
		if !ok || m.SessionID == "" {
			noMeta = append(noMeta, filepath.Base(f))
			continue
		}
		// The filename and the header must name the same session; every index
		// keys on one and displays the other.
		if id := IDFromRolloutRel(rel); id != "" && id != CanonicalID(m.SessionID) {
			t.Errorf("%s: filename says %s, header says %s", filepath.Base(f), id, m.SessionID)
		}
		if a, ok := LastActivity(f); !ok || a.At.IsZero() {
			noActivity = append(noActivity, filepath.Base(f))
		}
		if FirstPrompt(f) == "" {
			noTitle = append(noTitle, filepath.Base(f))
		}
	}
	t.Logf("read %d rollouts", len(files))
	for what, list := range map[string][]string{
		"unrecognised path": badRel,
		"no header":         noMeta,
		"no activity":       noActivity,
		"no title":          noTitle,
	} {
		if len(list) > 0 {
			t.Errorf("%d rollouts with %s (first few: %v)", len(list), what, list[:min(3, len(list))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
