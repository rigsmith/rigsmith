package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
)

func TestCheckGuide_FixInstalls(t *testing.T) {
	dir := t.TempDir()
	env := Env{RepoRoot: dir, ClaudeMd: filepath.Join(dir, "CLAUDE.md")}

	r := checkGuide(env)
	if r.Status != Warn || r.Fix == nil {
		t.Fatalf("absent guide: got %+v, want Warn with Fix", r)
	}
	if err := r.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r2 := checkGuide(env); r2.Status != OK {
		t.Fatalf("after fix: %+v, want OK", r2)
	}
}

func TestCheckGlobalHooks_FixInstalls(t *testing.T) {
	env := Env{UserSettings: filepath.Join(t.TempDir(), "settings.json")}
	r := checkGlobalHooks(env)
	if r.Status != Warn || r.Fix == nil {
		t.Fatalf("no hooks: got %+v, want Warn with Fix", r)
	}
	if err := r.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r2 := checkGlobalHooks(env); r2.Status != OK {
		t.Fatalf("after fix: %+v, want OK", r2)
	}
}

func TestCheckLocalGitignore_SkippedWhenNoLocalFile(t *testing.T) {
	env := Env{RepoRoot: t.TempDir(), LocalSettings: filepath.Join(t.TempDir(), "settings.local.json")}
	if _, ok := checkLocalGitignore(env); ok {
		t.Error("expected the local-gitignore check to be skipped when no local settings file exists")
	}
}

func TestRun_NoRepoSkipsRepoChecks(t *testing.T) {
	env := Env{
		UserSettings: filepath.Join(t.TempDir(), "settings.json"),
		// RepoRoot empty ⇒ not in a repo
	}
	sections := Run(context.Background(), env)
	var wt *Section
	for i := range sections {
		if sections[i].Title == "worktree discipline" {
			wt = &sections[i]
		}
	}
	if wt == nil {
		t.Fatal("no worktree section")
	}
	for _, r := range wt.Results {
		if r.Name == "guard hook" {
			t.Error("guard check should be skipped outside a repo")
		}
	}
	_ = os.Stdout
}

// The pair that misled for ten days: a fresh local commit (so `last sync` is
// green) sitting on top of a push that has been rejected every time. `pushed`
// exists to fail in exactly that state.
func TestCheckPushed_FailsWhenNothingReachedTheRemote(t *testing.T) {
	ctx := context.Background()
	bare := t.TempDir()
	if err := exec.Command("git", "init", "--bare", "-b", "main", bare).Run(); err != nil {
		t.Skip("git unavailable")
	}
	staging := t.TempDir()
	repo, err := gitrepo.Init(ctx, staging)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetRemote(ctx, "origin", bare); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	// A second commit that never gets pushed — the ten-day state.
	if err := os.WriteFile(filepath.Join(staging, "b.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "never pushed"); err != nil {
		t.Fatal(err)
	}

	env := Env{Cfg: &config.Config{}, Staging: staging, UserSettings: filepath.Join(t.TempDir(), "settings.json")}
	if got := checkPushed(ctx, env); got.Status != Fail {
		t.Fatalf("pushed = %v (%q), want Fail — an unpushed backup is broken, not untidy", got.Status, got.Detail)
	}
	// And it passes once the commit actually lands.
	if err := repo.Push(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if got := checkPushed(ctx, env); got.Status != OK {
		t.Fatalf("after pushing: pushed = %v (%q), want OK", got.Status, got.Detail)
	}
}

// The gap Copilot found in the first cut: a remote is configured, commits are
// piling up locally, and nothing has ever reached it — so there is no
// remote-tracking ref and ahead/behind are both 0. Reporting "up to date with
// origin/main" there is the same false green this check exists to remove.
func TestCheckPushed_NeverPushedToAConfiguredRemote(t *testing.T) {
	ctx := context.Background()
	staging := t.TempDir()
	repo, err := gitrepo.Init(ctx, staging)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetRemote(ctx, "origin", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, "local only"); err != nil {
		t.Fatal(err)
	}

	env := Env{Cfg: &config.Config{Remote: "https://example.invalid/x.git"}, Staging: staging,
		UserSettings: filepath.Join(t.TempDir(), "settings.json")}
	got := checkPushed(ctx, env)
	if got.Status == OK {
		t.Fatalf("pushed = OK (%q) — nothing has ever reached the remote", got.Detail)
	}
}

// A guard hook installed by an older release is present but not current, and
// "present" was all this check used to ask. Covers both scopes, since which
// settings file gets repaired depends on where the hook lives.
func TestCheckProjectGuard_FlagsAndFixesAStaleHook(t *testing.T) {
	for _, scope := range []string{"project", "local"} {
		t.Run(scope, func(t *testing.T) {
			dir := t.TempDir()
			proj := filepath.Join(dir, "settings.json")
			local := filepath.Join(dir, "settings.local.json")
			stale := filepath.Join(dir, "settings.json")
			env := Env{RepoRoot: dir, ProjectSettings: proj, LocalSettings: local}
			if scope == "local" {
				stale = local
				// The project file must have no clauderig hook, or it wins.
				if err := os.WriteFile(proj, []byte(`{}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// An older release's matcher: no Monitor.
			if _, err := hooks.Install(stale, []hooks.Plan{{
				Event: "PreToolUse", Matcher: "Edit|Write|Bash", Command: "clauderig guard",
			}}); err != nil {
				t.Fatal(err)
			}

			got := checkProjectGuard(env)
			if got.Status != Warn {
				t.Fatalf("status = %v (%q), want Warn — installed but out of date", got.Status, got.Detail)
			}
			if got.Fix == nil {
				t.Fatal("no Fix offered for a stale hook")
			}
			if err := got.Fix(context.Background()); err != nil {
				t.Fatal(err)
			}
			if after := checkProjectGuard(env); after.Status != OK {
				t.Fatalf("after Fix: status = %v (%q), want OK", after.Status, after.Detail)
			}
			// And it repaired the file the hook actually lives in.
			if drift, _ := hooks.Drift(stale, hooks.GuardPlans()); len(drift) != 0 {
				t.Fatalf("%s settings still drifted: %v", scope, drift)
			}
		})
	}
}

// wedgedStaging builds a staging repo abandoned mid-merge — the state that makes
// every later session start print a git error and never clears itself.
func wedgedStaging(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeFile := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	writeFile("cli/projects/p/memory/note.md", "# note\nshared\n")
	run("add", "-A")
	run("commit", "-m", "base")
	run("checkout", "-b", "other")
	writeFile("cli/projects/p/memory/note.md", "# note\nshared\nfrom the desktop\n")
	run("add", "-A")
	run("commit", "-m", "machine B")
	run("checkout", "main")
	writeFile("cli/projects/p/memory/note.md", "# note\nshared\nfrom the laptop\n")
	run("add", "-A")
	run("commit", "-m", "machine A")
	_ = exec.Command("git", "-C", dir, "merge", "--no-edit", "other").Run() // expected to conflict
	return dir
}

// A wedged staging repo is invisible to every other check — `last sync` reads a
// genuinely recent commit and `pushed` looks like ordinary drift — so it gets its
// own, and its Fix must leave a committed, marker-free resolution.
func TestCheckStagingMerge_FailsAndFixCommitsAMarkerFreeResolution(t *testing.T) {
	dir := wedgedStaging(t)
	env := Env{Cfg: &config.Config{}, Staging: dir}
	ctx := context.Background()

	r := checkStagingMerge(ctx, env)
	if r.Status != Fail || r.Fix == nil {
		t.Fatalf("wedged repo: got %+v, want Fail with a Fix", r)
	}
	if err := r.Fix(ctx); err != nil {
		t.Fatal(err)
	}
	if r2 := checkStagingMerge(ctx, env); r2.Status != OK {
		t.Fatalf("after fix: %+v, want OK", r2)
	}

	b, err := os.ReadFile(filepath.Join(dir, "cli/projects/p/memory/note.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "<<<<<<<") {
		t.Fatalf("conflict markers survived the fix:\n%s", got)
	}
	// Both machines' additions survive — the policy, not just "it stopped failing".
	for _, want := range []string{"from the laptop", "from the desktop"} {
		if !strings.Contains(got, want) {
			t.Errorf("fix lost %q:\n%s", want, got)
		}
	}
	// And the merge is committed, not merely staged.
	repo, err := gitrepo.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if repo.InMerge(ctx) {
		t.Error("the fix left the merge in progress")
	}
	if dirty, _ := repo.Dirty(ctx); dirty {
		t.Error("the fix left uncommitted changes behind")
	}
}

// The normal case: a repo with no merge in progress reports OK and offers no fix.
func TestCheckStagingMerge_CleanRepoIsOK(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := gitrepo.Init(ctx, dir); err != nil {
		t.Fatal(err)
	}
	r := checkStagingMerge(ctx, Env{Cfg: &config.Config{}, Staging: dir})
	if r.Status != OK || r.Fix != nil {
		t.Fatalf("clean repo: got %+v, want OK with no Fix", r)
	}
}

func TestCheckIgnoredSettings(t *testing.T) {
	dir := t.TempDir()
	env := Env{RepoRoot: dir,
		ProjectSettings: filepath.Join(dir, ".claude", "settings.json"),
		LocalSettings:   filepath.Join(dir, ".claude", "settings.local.json")}
	if _, ok := checkIgnoredSettings(env); ok {
		t.Fatal("no settings files: expected the check to be skipped")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.LocalSettings, []byte(`{"defaultMode":"bypassPermissions"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, ok := checkIgnoredSettings(env)
	if !ok || r.Status != Warn || !strings.Contains(r.Detail, "bypassPermissions") || !strings.Contains(r.Detail, "local") {
		t.Fatalf("got ok=%v %+v, want a Warn naming the local file's value", ok, r)
	}
}
