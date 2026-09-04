package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/claudemd"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/gitignore"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
	"github.com/rigsmith/rigsmith/internal/clauderig/status"
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

func checkClauderigOnPath(_ context.Context) Result {
	if !look("clauderig") {
		return Result{Name: "clauderig on PATH", Status: Fail, Detail: "NOT on PATH",
			Hint: "hooks call bare `clauderig` and will silently no-op — install clauderig so it resolves on PATH"}
	}
	return Result{Name: "clauderig on PATH", Status: OK, Detail: "resolvable"}
}

func checkRigOnPath(_ context.Context) Result {
	if !look("rig") {
		return Result{Name: "rig on PATH", Status: Warn, Detail: "not on PATH",
			Hint: "the worktree discipline points at `rig worktree new` — install rig (it ships alongside clauderig) so that guidance works"}
	}
	return Result{Name: "rig on PATH", Status: OK, Detail: "resolvable"}
}

// desktopPruneThreshold is how much a `desktop prune --vm` would have to free
// before doctor mentions it. The Cowork VM image alone reaches this within
// days of use, and there is no other signal that it has.
var desktopPruneThreshold int64 = 4 << 30

// desktopStore opens the profile store doctor measures; a variable so a test
// can point it at a store of its own rather than the real home directory.
var desktopStore = desktop.DefaultStore

// desktopSizeBudget bounds the profile walk on its own, under the doctor's
// deadline rather than sharing it: the checks after this one — the remote,
// the staging repo — run on the same context, and a walk that used the
// whole budget would hand them a cancelled one they would report as a
// failure of their own.
const desktopSizeBudget = 3 * time.Second

// checkDesktopSize reports when the Desktop profiles are holding space that
// `desktop prune` could give back — the VM image and caches, never chat
// history. It stays silent below the threshold so a normal machine does not
// carry a permanent line about disk it is not short of.
//
// It is the one environment check that walks a filesystem, and a profile can
// hold tens of GB, so the walk runs under the doctor's own deadline: past it
// the check gives up silently rather than hold the report.
func checkDesktopSize(ctx context.Context) (Result, bool) {
	st, err := desktopStore()
	if err != nil {
		return Result{}, false
	}
	profiles, err := st.List()
	if err != nil || len(profiles) == 0 {
		return Result{}, false
	}
	walk, cancel := context.WithTimeout(ctx, desktopSizeBudget)
	defer cancel()
	var total, reclaim int64
	for _, p := range profiles {
		u, merr := desktop.MeasureContext(walk, p)
		if walk.Err() != nil {
			return Result{}, false
		}
		if merr != nil {
			continue
		}
		total += u.Total
		reclaim += u.Reclaimable(desktop.PruneVM)
	}
	if reclaim < desktopPruneThreshold {
		return Result{}, false
	}
	return Result{Name: "desktop profiles", Status: Warn,
		Detail: fmt.Sprintf("%s on disk, %s of it reclaimable (Cowork VM image and caches)",
			desktop.HumanSize(total), desktop.HumanSize(reclaim)),
		Hint: "`clauderig desktop prune --dry-run` shows the breakdown; `--vm` resets the VM image, leaving logins and chat history alone",
	}, true
}

// --- sync ---

func checkRemote(ctx context.Context, env Env) Result {
	if env.Cfg == nil || env.Cfg.Remote == "" {
		return Result{Name: "remote", Status: Warn, Detail: "not configured",
			Hint: "run `clauderig config set remote <url>` (must be a PRIVATE repo)"}
	}
	remote := env.Cfg.Remote
	if !gitrepo.Reachable(ctx, remote) {
		return Result{Name: "remote", Status: Warn, Detail: "unreachable: " + remote,
			Hint: "check the URL, your network, or auth (the remote may still be fine)"}
	}
	if !ghrepo.Available() {
		return Result{Name: "remote", Status: Warn, Detail: "reachable; privacy unverified",
			Hint: "install gh so claudeRig can confirm the remote is private"}
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

// checkPushed asks the question `last sync` cannot: did any of it leave the
// machine?
//
// These are separate checks because they fail separately, and the pair that
// misleads is exactly "recent local commit + rejected push" — sync keeps
// committing, `last sync` keeps looking fresh, and nothing has been backed up
// since the day the remote diverged. Reported as a failure rather than a
// warning: a backup tool that is not backing up is broken, not untidy.
func checkPushed(ctx context.Context, env Env) Result {
	if env.Cfg == nil {
		return Result{Name: "pushed", Status: Info, Detail: "no config"}
	}
	info := status.Gather(ctx, env.Cfg, env.Machine, env.Staging, env.UserSettings)
	if !info.HasStaging {
		return Result{Name: "pushed", Status: Info, Detail: "no staging repo yet"}
	}
	if !info.TrackingKnown {
		if info.Remote == "" {
			return Result{Name: "pushed", Status: Info, Detail: "no remote configured"}
		}
		return Result{Name: "pushed", Status: Warn,
			Detail: "never pushed to this remote",
			Hint:   "run `clauderig sync`"}
	}
	switch {
	case info.Unpushed > 0 && info.Unmerged > 0:
		return Result{Name: "pushed", Status: Fail,
			Detail: fmt.Sprintf("%d commit(s) never pushed; remote has %d this machine lacks", info.Unpushed, info.Unmerged),
			Hint:   "the remote diverged — run `clauderig sync` to reconcile"}
	case info.Unpushed > 0:
		return Result{Name: "pushed", Status: Fail,
			Detail: fmt.Sprintf("%d commit(s) never reached the remote", info.Unpushed),
			Hint:   "run `clauderig sync`"}
	case info.Unmerged > 0:
		return Result{Name: "pushed", Status: Warn,
			Detail: fmt.Sprintf("%d commit(s) on the remote are not here yet", info.Unmerged),
			Hint:   "run `clauderig pull`"}
	}
	return Result{Name: "pushed", Status: OK, Detail: "up to date with origin/main"}
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
		where, settings := "project", env.ProjectSettings
		if !contains(proj, "PreToolUse") {
			where, settings = "local", env.LocalSettings
		}
		// Installed is not the same as current. The matcher lists the tools the
		// guard runs for, so a hook written by an older release quietly stops
		// covering whatever tools have been added since.
		if drift, derr := hooks.Drift(settings, hooks.GuardPlans()); derr == nil && len(drift) > 0 {
			return Result{Name: "guard hook", Status: Warn,
				Detail:   "installed (" + where + ") but out of date — it does not cover every tool it should",
				FixLabel: "update the guard hook (" + where + " settings.json)",
				Fix: func(ctx context.Context) error {
					_, _, err := hooks.InstallOrUpdate(settings, hooks.GuardPlans())
					return err
				}}
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
	ok, _ := claudemd.AllPresent(env.ClaudeMd)
	if ok {
		return Result{Name: "CLAUDE.md guide", Status: OK, Detail: "present"}
	}
	return Result{Name: "CLAUDE.md guide", Status: Warn, Detail: "block missing",
		FixLabel: "add CLAUDE.md guide blocks",
		Fix: func(ctx context.Context) error {
			_, err := claudemd.InstallAll(env.ClaudeMd)
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

// checkStagingMerge catches a staging repo abandoned part-way through a merge.
//
// It gets its own check because nothing else here can see it: `last sync` reads a
// commit that is genuinely recent, `pushed` reports a divergence that looks like
// ordinary drift, and neither says why every session start now prints a git error.
// The state is also self-perpetuating — the hook's pull is fast-forward-only, so
// it fails on the unmerged index instead of clearing it, every session, until
// someone opens the repo by hand. Reported as a failure: while it lasts, nothing
// from this machine is being backed up.
func checkStagingMerge(ctx context.Context, env Env) Result {
	if env.Cfg == nil || env.Staging == "" {
		return Result{Name: "staging repo", Status: Info, Detail: "no config"}
	}
	repo, err := gitrepo.Open(ctx, env.Staging)
	if err != nil {
		return Result{Name: "staging repo", Status: Info, Detail: "no staging repo yet"}
	}
	if !repo.InMerge(ctx) {
		return Result{Name: "staging repo", Status: OK, Detail: "clean (no merge in progress)"}
	}
	conflicts, _ := repo.Conflicts(ctx)
	return Result{
		Name:   "staging repo",
		Status: Fail,
		Detail: fmt.Sprintf("a merge was left in progress (%d conflicted file(s)) — sync is blocked", len(conflicts)),
		Hint:   "clauderig can settle it with its merge policies",
		Fix: func(ctx context.Context) error {
			rep, err := mergepolicy.Resolve(ctx, repo)
			if err != nil {
				return err
			}
			if len(rep.Unresolved) > 0 {
				return fmt.Errorf("%d conflict(s) need a human (%s) — run `clauderig sync` in a terminal",
					len(rep.Unresolved), rep.Unresolved[0])
			}
			return repo.CommitMerge(ctx)
		},
	}
}
