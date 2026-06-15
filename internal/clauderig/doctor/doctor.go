// Package doctor is clauderig's health check: it runs a set of independent checks
// over the environment, the sync setup, and this repo's worktree-discipline wiring,
// and returns a structured report. Each check reports OK/Warn/Fail with a detail
// and, when the problem is something clauderig can repair, a Fix closure. The
// presentation layer (internal/doctorui) renders the report and lets the user pick
// which fixes to apply — the package itself does no I/O to a terminal.
//
// The result model (Status/Result/Section + Counts/Fixable) is the shared
// core/doctor model; the aliases below let clauderig's checks keep reading against
// the local package while every rig speaks the same types.
package doctor

import (
	"context"

	coredoctor "github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// Shared result model — see core/doctor.
type (
	Status  = coredoctor.Status
	Result  = coredoctor.Result
	Section = coredoctor.Section
)

const (
	OK   = coredoctor.OK
	Warn = coredoctor.Warn
	Fail = coredoctor.Fail
	Info = coredoctor.Info
)

// Counts and Fixable are re-exported from core/doctor so callers that already
// reach for doctor.Counts / doctor.Fixable keep working.
func Counts(sections []Section) (fails, warns, fixable int) { return coredoctor.Counts(sections) }
func Fixable(sections []Section) []Result                   { return coredoctor.Fixable(sections) }

// Env is the resolved context every check runs against. The command layer fills it
// in once (paths, config, machine) so checks stay pure of discovery logic.
type Env struct {
	Home     string
	RepoRoot string // "" when not inside a git repo
	RepoName string
	Version  string

	UserSettings    string // ~/.claude/settings.json
	ProjectSettings string // <repo>/.claude/settings.json ("" if no repo)
	LocalSettings   string // <repo>/.claude/settings.local.json ("" if no repo)
	ClaudeMd        string // <repo>/CLAUDE.md ("" if no repo)

	Cfg     *config.Config
	Machine config.Machine
	Staging string
}

// InRepo reports whether the doctor is running inside a git repository.
func (e Env) InRepo() bool { return e.RepoRoot != "" }

// Run executes every check and returns the report, grouped into sections.
func Run(ctx context.Context, env Env) []Section {
	environment := Section{Title: "environment", Results: []Result{
		checkGit(ctx),
		checkGh(ctx),
		checkClauderigOnPath(ctx),
		checkRigOnPath(ctx),
	}}

	sync := Section{Title: "sync", Results: []Result{
		checkRemote(ctx, env),
		checkLastSync(ctx, env),
		checkPaths(env),
	}}

	wt := Section{Title: "worktree discipline"}
	if env.RepoName != "" {
		wt.Title += " · repo: " + env.RepoName
	}
	wt.Results = append(wt.Results, checkGlobalHooks(env))
	if env.InRepo() {
		wt.Results = append(wt.Results, checkProjectGuard(env), checkGuide(env))
		if r, ok := checkLocalGitignore(env); ok {
			wt.Results = append(wt.Results, r)
		}
	} else {
		wt.Results = append(wt.Results, Result{
			Name: "repo checks", Status: Info,
			Detail: "not in a git repo — run inside one to check the guard/guide",
		})
	}

	return []Section{environment, sync, wt}
}
