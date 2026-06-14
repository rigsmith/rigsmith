package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

// TestE2E_CrossOSPortability runs the full pipeline (sync → git → restore)
// translating a session BETWEEN operating systems, both directions. The physical
// files live in mac temp dirs; the "machine" OS/home only drive path translation,
// and slugs flatten away separators, so a Windows target's slug/paths materialise
// fine on a mac test host. It confirms: project slug re-derived for the target OS,
// and path values inside config JSON resolved to the target's native form.
func TestE2E_CrossOSPortability(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") == "" {
		t.Skip("gated: set CLAUDERIG_E2E=1 to run cross-OS portability")
	}
	ctx := t.Context()

	cases := []struct {
		name              string
		srcOS, srcHome    string
		tgtOS, tgtHome    string
		wantSlug          string // flatten of tgtHome/Git/proj
		wantExtraResolved string // additionalDirectories[0] resolved for the target
	}{
		{
			name:  "mac→windows",
			srcOS: pathmap.OSMacOS, srcHome: "/Users/john",
			tgtOS: pathmap.OSWindows, tgtHome: `C:\Users\Jane`,
			wantSlug:          "C--Users-Jane-Git-proj",
			wantExtraResolved: `C:\Users\Jane\Git\extra`,
		},
		{
			name:  "windows→mac",
			srcOS: pathmap.OSWindows, srcHome: `C:\Users\John`,
			tgtOS: pathmap.OSMacOS, tgtHome: "/Users/jane",
			wantSlug:          "-Users-jane-Git-proj",
			wantExtraResolved: "/Users/jane/Git/extra",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srcCwd := joinOS(c.srcOS, c.srcHome, "Git", "proj")
			srcExtra := joinOS(c.srcOS, c.srcHome, "Git", "extra")
			srcSlug := project.Flatten(srcCwd)

			// ---- source machine tree (physically on the mac host) ----
			claudeSrc := t.TempDir()
			writeJSON(t, claudeSrc, "settings.json", map[string]any{
				"effortLevel":           "high",
				"additionalDirectories": []string{srcExtra},
			})
			writeJSON(t, claudeSrc, "projects/"+srcSlug+"/s.jsonl", map[string]any{
				"type": "user", "cwd": srcCwd, "isSidechain": false,
			})

			srcMachine := config.Machine{Name: "src", OS: c.srcOS, Home: c.srcHome}

			// ---- sync → bare → clone ----
			stagingA := t.TempDir()
			if _, err := engine.Sync(engine.Options{
				StagingDir: stagingA, Config: cliOnly(claudeSrc), Machine: srcMachine,
				SourceOverride: map[string]string{"cli": claudeSrc},
			}); err != nil {
				t.Fatal(err)
			}
			bare := filepath.Join(t.TempDir(), "remote.git")
			mustGit(t, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare))
			repoA, err := gitrepo.Init(ctx, stagingA)
			must(t, err)
			must(t, repoA.SetRemote(ctx, "origin", bare))
			if _, err := repoA.Commit(ctx, "sync"); err != nil {
				t.Fatal(err)
			}
			must(t, repoA.Push(ctx, "origin", "main"))

			stagingB := filepath.Join(t.TempDir(), "repo")
			if _, err := gitrepo.Clone(ctx, bare, stagingB); err != nil {
				t.Fatal(err)
			}
			man, err := manifest.Load(stagingB)
			must(t, err)

			// ---- restore for the TARGET os into an override dir ----
			outDir := t.TempDir()
			tgtMachine := config.Machine{Name: "tgt", OS: c.tgtOS, Home: c.tgtHome}
			if _, err := engine.Restore(engine.RestoreOptions{
				StagingDir: stagingB, Config: cliOnly(outDir), Machine: tgtMachine, Manifest: man,
				TargetOverride: map[string]string{"cli": outDir}, OverriddenOnly: true,
			}); err != nil {
				t.Fatal(err)
			}

			// 1. project slug re-derived for the target OS
			if !exists(filepath.Join(outDir, "projects", c.wantSlug, "s.jsonl")) {
				t.Errorf("expected target slug %q under %s", c.wantSlug, outDir)
			}
			if exists(filepath.Join(outDir, "projects", srcSlug)) && srcSlug != c.wantSlug {
				t.Errorf("source slug %q should not exist on target", srcSlug)
			}
			// 2. path value inside settings.json resolved to the target's native form
			s := readJSON(t, filepath.Join(outDir, "settings.json"))
			dirs, _ := s["additionalDirectories"].([]any)
			if len(dirs) != 1 || dirs[0] != c.wantExtraResolved {
				t.Errorf("additionalDirectories = %v, want [%q]", dirs, c.wantExtraResolved)
			}
			t.Logf("%s: slug %s → %s, path → %s", c.name, srcSlug, c.wantSlug, c.wantExtraResolved)
		})
	}
}

func joinOS(os string, parts ...string) string {
	sep := "/"
	if os == pathmap.OSWindows {
		sep = `\`
	}
	return strings.Join(parts, sep)
}

func writeJSON(t *testing.T, root, rel string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(rel, ".jsonl") {
		b = append(b, '\n')
	}
	write(t, root, rel, string(b))
}
