package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/core/pathmap"
)

// TestE2E_DesktopRewriteCompleteOnRealData is the automatable half of Q4: it
// round-trips this machine's REAL Desktop session metadata
// (~/Library/Application Support/Claude/{claude-code-sessions,local-agent-mode-sessions})
// — portablize on this OS, then resolve onto a *different* machine — and asserts
// that NO value still contains the source home. A residual source path means a
// path-bearing field the value-based rewriter missed (e.g. a `//`-prefixed
// permission ruleContent), which would break resume on the target.
//
// It cannot prove the Electron app itself resumes — that's a manual check
// (see docs/CLAUDERIG-DESIGN.md Q4) — but it proves the rewrite is *complete*.
func TestE2E_DesktopRewriteCompleteOnRealData(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") == "" {
		t.Skip("gated: set CLAUDERIG_E2E=1 to check Desktop rewrite completeness on real data")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, "Library", "Application Support", "Claude")
	srcFolders := map[string]string{"HOME": home}
	target := pathmap.NewResolver(pathmap.MapFolders{"HOME": `C:\Users\Jane`}, pathmap.OSWindows, nil)

	checked, residual := 0, 0
	for _, sub := range []string{"claude-code-sessions", "local-agent-mode-sessions"} {
		root := filepath.Join(base, sub)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
			if werr != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			var v any
			if json.Unmarshal(data, &v) != nil {
				return nil
			}
			pv, _ := pathmap.PortablizeJSONValues(v, srcFolders, pathmap.OSMacOS)
			rv, _ := pathmap.ResolveJSONValues(pv, target)
			out, _ := json.Marshal(rv)
			checked++
			if strings.Contains(string(out), home) {
				residual++
				if residual <= 5 {
					t.Logf("residual source path in %s", strings.TrimPrefix(p, base+"/"))
				}
			}
			return nil
		})
	}

	if checked == 0 {
		t.Skip("no Desktop session files on this machine")
	}
	t.Logf("checked %d Desktop session files; %d still contained a source path after rewrite", checked, residual)
	if residual > 0 {
		t.Errorf("rewrite incomplete: %d/%d files retain a source-home path", residual, checked)
	}
}
