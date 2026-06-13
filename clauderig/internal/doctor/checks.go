package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rigsmith/clauderig/internal/claudemd"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/ghrepo"
	"github.com/rigsmith/clauderig/internal/gitignore"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/hooks"
	"github.com/rigsmith/clauderig/internal/status"
	"github.com/rigsmith/core/pathmap"
)

// --- environment ---

func checkGit(ctx context.Context) Result {
	if !look("git") {
		return Result{Name: "git", Status: Fail, Detail: "not found", Hint: "install git"}
	}
	v, _ := runOut(ctx, "git", "--version")
	return Result{Name: "git", Status: OK, Detail: strings.TrimSpace(strings.TrimPrefix(firstLine(v), "git version "))}
}

func checkGh(ctx context.Context) Result {
	if !look("gh") {
		return Result{Name: "gh", Status: Warn, Detail: "not installed",
			Hint: "needed to verify the sync remote is private and to open PRs — https://cli.github.com"}
	}
	if _, err := runCombined(ctx, "gh", "auth", "status"); err != nil {
		return Result{Name: "gh", Status: Warn, Detail: "not authenticated", Hint: "run `gh auth login`"}
	}
	return Result{Name: "gh", Status: OK, Detail: "authenticated"}
}

func checkCode(ctx context.Context) Result {
	// The worktree opener is configurable (worktree.openCmd); check whatever
	// program it resolves to, defaulting to VS Code's `code`.
	openCmd := config.DefaultWorktreeOpenCmd
	if cfg, err := config.LoadOrDefault(); err == nil {
		openCmd = cfg.WorktreeOpenCmd()
	}
	prog := openCmd[0]
	name := fmt.Sprintf("%s (worktree opener)", prog)
	if !look(prog) {
		hint := fmt.Sprintf("`clauderig worktree open` can't launch a window with %q", prog)
		if prog == "code" {
			hint = "`clauderig worktree open` can't launch a window; in VS Code run “Shell Command: Install 'code' command in PATH”, or set another opener with `clauderig config set-worktree-opener`"
		}
		return Result{Name: name, Status: Warn, Detail: "not on PATH", Hint: hint}
	}
	return Result{Name: name, Status: OK, Detail: "available"}
}

func checkClauderigOnPath(_ context.Context) Result {
	if !look("clauderig") {
		return Result{Name: "clauderig on PATH", Status: Fail, Detail: "NOT on PATH",
			Hint: "hooks call bare `clauderig` and will silently no-op — install clauderig so it resolves on PATH"}
	}
	return Result{Name: "clauderig on PATH", Status: OK, Detail: "resolvable"}
}

// --- sync ---

func checkRemote(ctx context.Context, env Env) Result {
	if env.Cfg == nil || env.Cfg.Remote == "" {
		return Result{Name: "remote", Status: Warn, Detail: "not configured",
			Hint: "run `clauderig config set-remote <url>` (must be a PRIVATE repo)"}
	}
	remote := env.Cfg.Remote
	if !gitrepo.Reachable(ctx, remote) {
		return Result{Name: "remote", Status: Warn, Detail: "unreachable: " + remote,
			Hint: "check the URL, your network, or auth (the remote may still be fine)"}
	}
	if !ghrepo.Available() {
		return Result{Name: "remote", Status: Warn, Detail: "reachable; privacy unverified",
			Hint: "install gh so clauderig can confirm the remote is private"}
	}
	if err := ghrepo.EnsurePrivate(ctx, remote); err != nil {
		return Result{Name: "remote", Status: Fail, Detail: "NOT private (or unverifiable): " + remote,
			Hint: "clauderig only syncs to a private repo — make it private or change the remote"}
	}
	return Result{Name: "remote", Status: OK, Detail: "private · reachable"}
}

func checkLastSync(ctx context.Context, env Env) Result {
	if env.Cfg == nil {
		return Result{Name: "last sync", Status: Info, Detail: "no config"}
	}
	info := status.Gather(ctx, env.Cfg, env.Machine, env.Staging, env.UserSettings)
	if !info.HasStaging || info.LastSync == "" {
		return Result{Name: "last sync", Status: Warn, Detail: "never synced", Hint: "run `clauderig sync`"}
	}
	if info.Dirty {
		return Result{Name: "last sync", Status: Warn, Detail: info.LastSync + " (staging has uncommitted changes)"}
	}
	return Result{Name: "last sync", Status: OK, Detail: info.LastSync}
}

func checkPaths(env Env) Result {
	if env.Cfg == nil {
		return Result{Name: "path resolution", Status: Info, Detail: "no config"}
	}
	var unresolved []string
	total := 0
	for _, r := range env.Cfg.Roots {
		if !r.Enabled {
			continue
		}
		total++
		if _, st := env.Cfg.RootLocation(r.ID, env.Machine); st != pathmap.StatusResolved {
			unresolved = append(unresolved, r.ID)
		}
	}
	if len(unresolved) > 0 {
		return Result{Name: "path resolution", Status: Warn,
			Detail: fmt.Sprintf("%d/%d roots resolve; unmapped: %s", total-len(unresolved), total, strings.Join(unresolved, ", ")),
			Hint:   "add a machine map for the unmapped folders via `clauderig config`"}
	}
	return Result{Name: "path resolution", Status: OK, Detail: fmt.Sprintf("%d roots resolve for %s", total, env.Machine.OS)}
}

// --- worktree discipline ---

func checkGlobalHooks(env Env) Result {
	present, _ := hooks.Status(env.UserSettings)
	if contains(present, "SessionStart") && contains(present, "Stop") {
		return Result{Name: "global sync hooks", Status: OK, Detail: "SessionStart, Stop"}
	}
	detail := "not installed"
	if len(present) > 0 {
		detail = "partial: " + strings.Join(present, ", ")
	}
	return Result{Name: "global sync hooks", Status: Warn, Detail: detail,
		FixLabel: "install global sync hooks (~/.claude/settings.json)",
		Fix: func(ctx context.Context) error {
			_, err := hooks.Install(env.UserSettings, hooks.SyncPlans())
			return err
		}}
}

func checkProjectGuard(env Env) Result {
	proj, _ := hooks.Status(env.ProjectSettings)
	local, _ := hooks.Status(env.LocalSettings)
	if contains(proj, "PreToolUse") || contains(local, "PreToolUse") {
		where := "project"
		if !contains(proj, "PreToolUse") {
			where = "local"
		}
		return Result{Name: "guard hook", Status: OK, Detail: "installed (" + where + ")"}
	}
	return Result{Name: "guard hook", Status: Warn, Detail: "not installed in this repo",
		FixLabel: "install project guard (.claude/settings.json)",
		Fix: func(ctx context.Context) error {
			_, err := hooks.Install(env.ProjectSettings, hooks.GuardPlans())
			return err
		}}
}

func checkGuide(env Env) Result {
	ok, _ := claudemd.Present(env.ClaudeMd)
	if ok {
		return Result{Name: "CLAUDE.md guide", Status: OK, Detail: "present"}
	}
	return Result{Name: "CLAUDE.md guide", Status: Warn, Detail: "block missing",
		FixLabel: "add CLAUDE.md guide block",
		Fix: func(ctx context.Context) error {
			_, err := claudemd.Install(env.ClaudeMd)
			return err
		}}
}

// checkLocalGitignore only applies when a local settings file actually exists;
// ok is false to omit the check entirely otherwise.
func checkLocalGitignore(env Env) (Result, bool) {
	if _, err := os.Stat(env.LocalSettings); err != nil {
		return Result{}, false
	}
	const entry = ".claude/settings.local.json"
	if repo, err := gitrepo.Open(context.Background(), env.RepoRoot); err == nil && repo.IsIgnored(context.Background(), entry) {
		return Result{Name: "local settings gitignored", Status: OK, Detail: "ignored"}, true
	}
	return Result{Name: "local settings gitignored", Status: Warn, Detail: entry + " is not gitignored",
		FixLabel: "gitignore .claude/settings.local.json",
		Fix: func(ctx context.Context) error {
			return ensureIgnored(env.RepoRoot, entry)
		}}, true
}

func ensureIgnored(root, entry string) error {
	gi := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(gi)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	next, changed := gitignore.EnsureLine(string(b), entry)
	if !changed {
		return nil
	}
	return os.WriteFile(gi, []byte(next), 0o644)
}

// --- helpers ---

func look(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

func runOut(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func runCombined(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
