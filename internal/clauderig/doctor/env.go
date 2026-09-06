package doctor

import (
	"context"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
)

// NewEnv resolves everything the checks run against: this machine's home, the
// repository the caller is standing in (if any), where its settings files would
// be, and the sync configuration.
//
// It lives here rather than in the command layer because the window runs the
// same checks and must run them against the same environment. Two definitions
// of "what are we checking" would disagree eventually, and the answer people
// would then get from `clauderig doctor` and from the window would differ
// without either being obviously wrong — which is the worst way for a health
// check to fail.
//
// Best-effort throughout: a missing repo, an unreadable config or a home
// directory that cannot be determined each leave their field empty, because a
// doctor that refuses to run is no use to someone trying to find out what is
// broken. The checks each decide what an empty field means for them.
func NewEnv(ctx context.Context, version string) Env {
	home, _ := os.UserHomeDir()
	root := RepoRoot(ctx)
	env := Env{Home: home, Version: version, RepoRoot: root}
	if root != "" {
		env.RepoName = filepath.Base(root)
		env.ProjectSettings, _ = settings.Project.Path(home, root)
		env.LocalSettings, _ = settings.Local.Path(home, root)
		env.ClaudeMd = filepath.Join(root, "CLAUDE.md")
	}
	env.UserSettings, _ = settings.User.Path(home, root)
	cfg, err := config.LoadOrDefault()
	if err != nil {
		cfg = config.Default()
	}
	env.Cfg = cfg
	// DetectFor, not Detect("this"). "this" is the placeholder for a host with
	// no resolvable identity, and it is load-bearing history here: one that
	// failed to resolve registered a ghost device under that literal name which
	// sat in the synced registry from June to August 2026. Nothing writes from
	// the doctor, so it never registered anything — but env.Machine feeds
	// RootLocation, so a machine with a per-machine root override keyed by its
	// real name would have had that override ignored and been told its paths
	// resolve when they resolve to something else. It also puts the right name
	// on the report, which matters when a window can be open while you are
	// thinking about a different machine.
	env.Machine = config.DetectFor(cfg)
	env.Staging, _ = config.StagingDir()
	return env
}

// NewMachineEnv is NewEnv with every repository-scoped field left empty, for a
// caller that has no repository to speak of.
//
// The window is the case this exists for. `clauderig doctor` is run from inside
// the repo you mean, so its working directory is the answer; a tray application
// has no such directory — it inherits whatever the launcher happened to use,
// which is the shell's cwd from a terminal and the filesystem root from Finder.
// Reporting worktree discipline for a repository nobody chose, that changes
// depending on how the application was started, is worse than not reporting it.
//
// The checks themselves already handle an absent repository: they say so and
// carry on, which is what makes this safe to ask for.
func NewMachineEnv(ctx context.Context, version string) Env {
	env := NewEnv(ctx, version)
	env.RepoRoot, env.RepoName = "", ""
	env.ProjectSettings, env.LocalSettings, env.ClaudeMd = "", "", ""
	return env
}

// RepoRoot is the top level of the repository the process is standing in, or ""
// when it is not in one. Not being in a repository is an ordinary state — the
// window is usually launched from nowhere in particular — so it is reported as
// an empty string rather than an error.
func RepoRoot(ctx context.Context) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	repo, err := gitrepo.Open(ctx, cwd)
	if err != nil {
		return ""
	}
	root, err := repo.Toplevel(ctx)
	if err != nil {
		return ""
	}
	return root
}
