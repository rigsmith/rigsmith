package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestHookChecksProjectTrust(t *testing.T) {
	for _, mode := range []string{"untrusted", "trusted", "unavailable", "error", "missing"} {
		t.Run(mode, func(t *testing.T) {
			home, repo := t.TempDir(), t.TempDir()
			path := filepath.Join(home, hooks.FileName)
			if _, _, err := hooks.Install(path, hooks.SyncPlans()); err != nil {
				t.Fatal(err)
			}
			if _, _, err := hooks.Install(filepath.Join(repo, ".codex", hooks.FileName), hooks.SyncPlans()); err != nil {
				t.Fatal(err)
			}
			calls := 0
			results := hookChecksWithProbe(context.Background(), Env{HooksPath: path, CodexHome: home, RepoRoot: repo}, mode != "unavailable", func(ctx context.Context, h, cwd string) ([]appserver.HookInfo, error) {
				calls++
				if cwd != repo || h != "" {
					t.Fatalf("probe home=%q cwd=%q", h, cwd)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 25*time.Second {
					t.Fatal("probe missing bounded deadline")
				}
				if mode == "error" {
					return nil, errors.New("probe failed")
				}
				list := []appserver.HookInfo{{Source: "unrecognized", SourcePath: path, EventName: "Stop", TrustStatus: "trusted"}}
				if mode != "missing" {
					list = append(list, appserver.HookInfo{Source: "unrecognized", SourcePath: filepath.Join(repo, ".codex", hooks.FileName), EventName: "PreToolUse", TrustStatus: mode})
				}
				return list, nil
			})
			wantCalls := 1
			if mode == "unavailable" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("probe calls=%d", calls)
			}
			found := false
			for _, r := range results {
				if mode == "unavailable" && (r.ID == "hook-trust" || r.ID == "project-hook-trust") && r.Status != Info {
					t.Fatalf("unavailable probe nags: %+v", r)
				}
				if r.ID == "hook-trust" && mode != "error" && mode != "unavailable" && r.Status != OK {
					t.Fatalf("project trust leaked into global result: %+v", r)
				}
				if r.ID != "project-hook-trust" {
					continue
				}
				found = true
				if mode == "trusted" {
					if r.Status != OK {
						t.Fatalf("%+v", r)
					}
				} else if mode == "unavailable" {
					if r.Status != Info {
						t.Fatalf("%+v", r)
					}
				} else if r.Status != Warn {
					t.Fatalf("%+v", r)
				}
				if mode == "untrusted" && (r.Fix == nil || !strings.Contains(r.Hint, "codexrig project trust")) {
					t.Fatalf("missing remedy: %+v", r)
				}
				if mode == "unavailable" && !strings.Contains(r.Detail, "not checked") {
					t.Fatalf("%+v", r)
				}
			}
			if !found {
				t.Fatal("project trust check absent")
			}
		})
	}
}

func TestHookChecksPathClassification(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, ".codex")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, hooks.FileName)
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{path, filepath.Join(alias, ".codex", hooks.FileName), strings.ReplaceAll(path, "/", `\`), root + "/../.codex/" + hooks.FileName} {
		if !projectHookPath(repo, source) {
			t.Errorf("project path missed: %s", source)
		}
	}
	for _, source := range []string{"", filepath.Join(repo, ".codex-other", hooks.FileName), filepath.Join(repo, hooks.FileName)} {
		if projectHookPath(repo, source) {
			t.Errorf("non-project path matched: %s", source)
		}
	}
}

func TestHookChecksTrustResultStatuses(t *testing.T) {
	env := Env{CodexHome: t.TempDir(), RepoRoot: t.TempDir()}
	for _, project := range []bool{false, true} {
		unavailable := hookTrustResult(env, project, false, nil, nil)
		missing := hookTrustResult(env, project, true, nil, nil)
		if unavailable.Status != Info || missing.Status != Warn {
			t.Fatalf("unavailable=%+v missing=%+v", unavailable, missing)
		}
		fails, _, _ := Counts([]Section{{Results: []Result{unavailable, missing}}})
		if fails != 0 {
			t.Fatal("informational and missing trust checks change exit status")
		}
	}
	global := hookTrustResult(env, false, true, []appserver.HookInfo{{SourcePath: filepath.Join(env.CodexHome, hooks.FileName), TrustStatus: "untrusted"}}, nil)
	if global.Status != Fail || !strings.Contains(global.Hint, "codexrig global trust") {
		t.Fatalf("%+v", global)
	}
}

// Read-only hooks/list probe; all Codex state and hook files are disposable.
func TestHookChecksLiveSources(t *testing.T) {
	if os.Getenv("CODEXRIG_LIVE_CODEX") == "" || !appserver.Available() {
		t.Skip("needs codex and CODEXRIG_LIVE_CODEX=1")
	}
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", home)
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[projects."+strconv.Quote(canonicalRepo)+"]\ntrust_level = \"trusted\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hooks.Install(filepath.Join(home, hooks.FileName), hooks.SyncPlans()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hooks.Install(filepath.Join(repo, ".codex", hooks.FileName), hooks.GuardPlans("Bash|apply_patch")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	list, err := hooks.Check(ctx, home, repo)
	if err != nil {
		t.Fatal(err)
	}
	userCount, projectCount := 0, 0
	for _, h := range list {
		t.Logf("event=%q source=%q sourcePath=%q trust=%q", h.EventName, h.Source, h.SourcePath, h.TrustStatus)
		if projectHookPath(repo, h.SourcePath) {
			projectCount++
		} else {
			userCount++
		}
	}
	if userCount != 2 || projectCount != 1 {
		t.Fatalf("user=%d project=%d; want 2 and 1", userCount, projectCount)
	}
}
