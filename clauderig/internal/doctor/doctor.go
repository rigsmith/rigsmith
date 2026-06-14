// Package doctor is clauderig's health check: it runs a set of independent checks
// over the environment, the sync setup, and this repo's worktree-discipline wiring,
// and returns a structured report. Each check reports OK/Warn/Fail with a detail
// and, when the problem is something clauderig can repair, a Fix closure. The
// presentation layer (commands/doctor.go) renders the report and lets the user pick
// which fixes to apply — the package itself does no I/O to a terminal.
package doctor

import (
	"context"

	"github.com/rigsmith/clauderig/internal/config"
)

// Status is a single check's verdict.
type Status int

const (
	OK   Status = iota // healthy
	Warn               // degraded but usable
	Fail               // broken
	Info               // neutral context, never a problem
)

// Result is one check's outcome. Fix is non-nil only when clauderig can repair the
// issue itself; FixLabel is the one-line description shown in the fix selector.
type Result struct {
	Name     string
	Status   Status
	Detail   string
	Hint     string                      // manual remediation when Fix is nil
	Fix      func(context.Context) error // nil ⇒ not auto-fixable
	FixLabel string
}

// Section groups related checks under a heading.
type Section struct {
	Title   string
	Results []Result
}

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

// Counts tallies the report. fixable counts Warn/Fail results that carry a Fix.
func Counts(sections []Section) (fails, warns, fixable int) {
	for _, s := range sections {
		for _, r := range s.Results {
			switch r.Status {
			case Fail:
				fails++
			case Warn:
				warns++
			}
			if r.Fix != nil && (r.Status == Fail || r.Status == Warn) {
				fixable++
			}
		}
	}
	return fails, warns, fixable
}

// Fixable returns the repairable results, in report order.
func Fixable(sections []Section) []Result {
	var out []Result
	for _, s := range sections {
		for _, r := range s.Results {
			if r.Fix != nil && (r.Status == Fail || r.Status == Warn) {
				out = append(out, r)
			}
		}
	}
	return out
}
