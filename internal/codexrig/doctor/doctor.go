// Package doctor answers "is this actually working?" — which for a backup tool
// is the only question that matters, because every way it fails is quiet. A hook
// that is installed but untrusted runs nothing and says nothing. A push that is
// rejected leaves a machine looking synced. A remote that stopped being private
// looks exactly like one that never was.
package doctor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	coredoctor "github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/hooks"
	"github.com/rigsmith/rigsmith/internal/codexrig/journal"
)

// The shared result model, aliased so a check here reads without a package
// qualifier on every line.
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

// Counts and Fixable are re-exported for the command layer.
func Counts(s []Section) (fails, warns, fixable int) { return coredoctor.Counts(s) }
func Fixable(s []Section) []Result                   { return coredoctor.Fixable(s) }

// Env is the resolved context every check runs against. The command layer fills
// it once so the checks stay free of discovery logic — and so the same checks
// can be run from somewhere that is not a terminal.
type Env struct {
	Cwd        string
	Output     io.Writer // receives commands approved or refused by a trust fix
	Version    string
	Config     *config.Config
	Machine    config.Machine
	Staging    string
	CodexHome  string
	HooksPath  string
	RepoRoot   string
	ConfigPath string
}

// NewEnv resolves the environment, best-effort throughout: a missing piece
// leaves a field empty rather than failing, because a doctor that cannot run is
// worse than one that reports a problem.
func NewEnv(ctx context.Context, version string) Env {
	e := Env{Version: version}
	e.Config, _ = config.LoadOrDefault()
	if e.Config == nil {
		e.Config = config.Default()
	}
	e.Machine = config.DetectFor(e.Config)
	e.Staging, _ = config.StagingDir()
	if dir, err := config.Dir(); err == nil {
		e.ConfigPath = filepath.Join(dir, "config.json")
	}
	e.CodexHome, _ = codexhome.Default()
	if e.CodexHome != "" {
		e.HooksPath = filepath.Join(e.CodexHome, hooks.FileName)
	}
	if cwd, err := os.Getwd(); err == nil {
		e.Cwd = cwd
		if repo, err := gitrepo.Open(ctx, cwd); err == nil {
			e.RepoRoot, _ = repo.Toplevel(ctx)
		}
	}
	return e
}

// Run assembles the report.
func Run(ctx context.Context, env Env) []Section {
	return []Section{
		{Title: "environment", Results: environment(ctx, env)},
		{Title: "sync", Results: sync(ctx, env)},
		{Title: "hooks", Results: hookChecks(ctx, env)},
	}
}

func environment(ctx context.Context, env Env) []Result {
	var out []Result
	out = append(out, binary("git", "git", "install git"))
	out = append(out, codexCheck(ctx))
	// The hooks call a BARE `codexrig`, so that it keeps working when the same
	// hooks file is restored onto another machine. A binary that does not
	// resolve turns every hook into a silent no-op.
	if _, err := exec.LookPath("codexrig"); err != nil {
		out = append(out, Result{
			ID: "codexrig-on-path", Name: "codexrig on PATH", Status: Fail,
			Detail: "NOT on PATH",
			Hint:   "the hooks call a bare `codexrig` and will silently do nothing — install it so it resolves on PATH",
		})
	} else {
		out = append(out, Result{ID: "codexrig-on-path", Name: "codexrig on PATH", Status: OK, Detail: "resolvable"})
	}
	out = append(out, ghCheck(ctx))
	if r, show := relocatedHome(); show {
		out = append(out, r)
	}
	return out
}

func binary(id, name, hint string) Result {
	if _, err := exec.LookPath(name); err != nil {
		return Result{ID: id, Name: name, Status: Fail, Detail: "not found", Hint: hint}
	}
	return Result{ID: id, Name: name, Status: OK, Detail: "found"}
}

func codexCheck(ctx context.Context) Result {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return Result{
			ID: "codex", Name: "codex", Status: Warn, Detail: "not found",
			Hint: "codexrig can still back up a Codex home it can see, but it cannot ask Codex anything — including whether your hooks are trusted",
		}
	}
	// Bounded like ghCheck: doctor is what a person runs when things are
	// already broken, and a codex that hangs must not take doctor with it.
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probe, bin, "--version").Output()
	if err != nil {
		return Result{ID: "codex", Name: "codex", Status: Warn, Detail: "found, but would not report a version"}
	}
	return Result{ID: "codex", Name: "codex", Status: OK, Detail: strings.TrimSpace(string(out))}
}

func ghCheck(ctx context.Context) Result {
	if _, err := exec.LookPath("gh"); err != nil {
		return Result{
			ID: "gh", Name: "gh", Status: Warn, Detail: "not installed",
			Hint: "needed to verify the sync remote is private, and to create one — https://cli.github.com",
		}
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(probe, "gh", "auth", "status").Run(); err != nil {
		return Result{ID: "gh", Name: "gh", Status: Warn, Detail: "not authenticated", Hint: "run `gh auth login`"}
	}
	return Result{ID: "gh", Name: "gh", Status: OK, Detail: "authenticated"}
}

// relocatedHome is reported only when it is true, so an ordinary machine does
// not carry a permanent line saying nothing is unusual.
func relocatedHome() (Result, bool) {
	env, set := codexhome.Env()
	if !set {
		return Result{}, false
	}
	def, err := codexhome.Default()
	if err == nil && codexhome.SameDir(env, def) {
		return Result{}, false
	}
	return Result{
		ID: "relocated-home", Name: "CODEX_HOME", Status: Warn,
		Detail: env + " — this shell is not looking at the machine's Codex home",
		Hint:   "commands that act on the machine will refuse; unset it, or use `codexrig account run` for an isolated session",
	}, true
}

func sync(ctx context.Context, env Env) []Result {
	var out []Result

	// The remote. A remote that stopped being private looks exactly like one
	// that never was, so this is a Fail rather than a warning.
	switch {
	case env.Config.Remote == "":
		out = append(out, Result{ID: "remote", Name: "remote", Status: Warn, Detail: "not configured", Hint: "run `codexrig init`"})
	default:
		probe, cancel := context.WithTimeout(ctx, 8*time.Second)
		reach := gitrepo.Reachable(probe, env.Config.Remote)
		perr := ghrepo.EnsurePrivate(probe, env.Config.Remote)
		cancel()
		switch {
		case perr != nil:
			out = append(out, Result{
				ID: "remote", Name: "remote", Status: Fail, Detail: perr.Error(),
				Hint: "codexrig only syncs to a private repo; nothing will be pushed until this is settled",
			})
		case !reach:
			out = append(out, Result{ID: "remote", Name: "remote", Status: Warn, Detail: "private, but unreachable right now"})
		default:
			out = append(out, Result{ID: "remote", Name: "remote", Status: OK, Detail: "private · reachable"})
		}
	}

	// A wedged merge blocks every sync, and nothing else says so.
	if repo, err := gitrepo.Open(ctx, env.Staging); err == nil {
		if repo.InMerge(ctx) {
			files, _ := repo.UnmergedPaths(ctx)
			out = append(out, Result{
				ID: "staging-repo", Name: "staging repo", Status: Fail,
				Detail: fmt.Sprintf("a merge was left in progress (%d conflicted file(s)) — sync is blocked", len(files)),
				Hint:   "run `codexrig sync` in a terminal; codexrig settles what it can and names what it cannot",
			})
		} else {
			out = append(out, Result{ID: "staging-repo", Name: "staging repo", Status: OK, Detail: "clean"})
		}
	} else {
		out = append(out, Result{ID: "staging-repo", Name: "staging repo", Status: Info, Detail: "none yet"})
	}

	// Last run. A refusal is the case worth surfacing loudly: it means nothing
	// has been backed up since, and the only other place it was said was a
	// hook's stderr.
	if recs, err := journal.Read(env.Staging, 1); err == nil && len(recs) > 0 {
		r := recs[0]
		switch r.Outcome {
		case journal.OutcomeRefused:
			out = append(out, Result{
				ID: "last-run", Name: "last run", Status: Fail, Detail: r.Summary(),
				Hint: "nothing has been backed up since; remove the credential from the file it names, then sync again",
			})
		case journal.OutcomeFailed:
			out = append(out, Result{ID: "last-run", Name: "last run", Status: Fail, Detail: r.Summary()})
		default:
			out = append(out, Result{ID: "last-run", Name: "last run", Status: OK, Detail: r.At.Local().Format("2006-01-02 15:04") + " — " + r.Summary()})
		}
	} else {
		out = append(out, Result{ID: "last-run", Name: "last run", Status: Warn, Detail: "never synced", Hint: "run `codexrig sync`"})
	}

	// Unpushed work. A backup tool that is not backing up is broken, not untidy.
	if repo, err := gitrepo.Open(ctx, env.Staging); err == nil && env.Config.Remote != "" {
		if d, derr := repo.DivergenceFrom(ctx, "origin/main"); derr == nil {
			switch {
			case !d.Tracked:
				out = append(out, Result{ID: "pushed", Name: "pushed", Status: Warn, Detail: "never pushed to this remote", Hint: "run `codexrig sync`"})
			case d.Ahead > 0:
				out = append(out, Result{
					ID: "pushed", Name: "pushed", Status: Fail,
					Detail: fmt.Sprintf("%d commit(s) never reached the remote", d.Ahead),
					Hint:   "a backup that is not leaving the machine is not a backup — run `codexrig sync`",
				})
			case d.Behind > 0:
				out = append(out, Result{ID: "pushed", Name: "pushed", Status: Warn, Detail: fmt.Sprintf("%d commit(s) on the remote are not here yet", d.Behind), Hint: "run `codexrig pull`"})
			default:
				out = append(out, Result{ID: "pushed", Name: "pushed", Status: OK, Detail: "up to date with origin/main"})
			}
		}
	}

	// Roots that do not resolve here.
	resolved, total := 0, 0
	var unmapped []string
	for _, r := range env.Config.Roots {
		if !r.Enabled {
			continue
		}
		total++
		if _, st := r.ResolveOn(env.Machine); st == pathmap.StatusResolved {
			resolved++
		} else {
			unmapped = append(unmapped, r.ID)
		}
	}
	if total > 0 && resolved < total {
		out = append(out, Result{
			ID: "path-resolution", Name: "paths", Status: Warn,
			Detail: fmt.Sprintf("%d/%d roots resolve; unmapped: %s", resolved, total, strings.Join(unmapped, ", ")),
		})
	}

	out = append(out, loginCheck())
	if r, show := sessionsCheck(env); show {
		out = append(out, r)
	}
	return out
}

func loginCheck() Result {
	raw, err := account.ReadLive()
	if err != nil {
		return Result{ID: "login", Name: "Codex login", Status: Warn, Detail: "not logged in", Hint: "run `codex login`"}
	}
	if !account.HasTokens(raw) {
		return Result{
			ID: "login", Name: "Codex login", Status: Fail,
			Detail: "auth.json holds no usable token — logged out in all but name",
			Hint:   "run `codex login`",
		}
	}
	id := account.IdentityOf(raw)
	detail := id.Email
	if detail == "" {
		detail = "an API-key login"
	}
	if id.PlanType != "" {
		detail += " · " + id.PlanType
	}
	if s, serr := account.DefaultStore(); serr == nil {
		if o := s.Diagnose(); o.PointerEmail != "" {
			return Result{
				ID: "login", Name: "Codex login", Status: Warn,
				Detail: detail + " — but codexrig's active account says " + o.PointerEmail,
				Hint:   "`codexrig account doctor --fix` repoints codexrig at the live login",
			}
		}
	}
	return Result{ID: "login", Name: "Codex login", Status: OK, Detail: detail}
}

// sessionsCheck says plainly that conversations are not being backed up, when
// they are not. Someone who set this tool up and assumed otherwise has been
// misled by omission, and nothing else on the machine will tell them.
func sessionsCheck(env Env) (Result, bool) {
	if env.Config.SyncSessions {
		return Result{}, false
	}
	return Result{
		ID: "sessions", Name: "session rollouts", Status: Info,
		Detail: "not backed up — configuration only",
		Hint:   "`codexrig config set syncSessions true` carries your conversations too",
	}, true
}

func hookChecks(ctx context.Context, env Env) []Result {
	return hookChecksWithProbe(ctx, env, appserver.Available(), hooks.Check)
}

func hookChecksWithProbe(ctx context.Context, env Env, available bool, check func(context.Context, string, string, ...hooks.Scope) (hooks.CheckResult, error)) []Result {
	out, installed := userHookChecks(env)
	globalPresent := installed.HasCandidates()
	projectPresent := false
	if env.RepoRoot != "" {
		path := filepath.Join(env.RepoRoot, ".codex", hooks.FileName)
		status, err := hooks.Status(path, hooks.ProjectScope(env.RepoRoot, env.CodexHome))
		projectPresent = status.HasCandidates()
		switch {
		case err != nil:
			out = append(out, Result{ID: "project-guard", Name: "repo guard", Status: Warn, Detail: hooks.Display(err.Error())})
		case len(status.Lookalikes) > 0:
			out = append(out, Result{ID: "project-guard", Name: "repo guard", Status: Warn,
				Detail:   "lookalike or out of date, reinstall: " + describeHookCommands(status.Lookalikes),
				Hint:     "Run `codexrig project install`, review the result, then `codexrig project trust`",
				FixLabel: "reinstall the project guard",
				Fix: func(context.Context) error {
					_, _, err := hooks.InstallScope(hooks.ProjectScope(env.RepoRoot, env.CodexHome), env.Output)
					return err
				},
			})
		case len(status.Ours) > 0:
			out = append(out, Result{ID: "project-guard", Name: "repo guard", Status: OK, Detail: hooks.Display(strings.Join(status.Events(), ", "))})
		case len(status.Foreign) > 0:
			// A repository with only somebody else's hooks has no codexrig state to diagnose.
		default:
			out = append(out, Result{ID: "project-guard", Name: "repo guard", Status: Info,
				Detail: "not installed in this repository", Hint: "`codexrig project install` keeps code changes off a base branch"})
		}
	}
	for i := range out {
		if out[i].ID == "project-guard" && !hooks.CanInstall(hooks.ProjectScope(env.RepoRoot, env.CodexHome)) {
			out[i].Fix = nil
			out[i].FixLabel = ""
			if out[i].Status == Warn {
				out[i].Hint = "Review lookalikes manually in the hooks file; the safe installer cannot repair them."
			}
		}
	}
	if globalPresent || projectPresent || available {
		home := env.CodexHome
		if home == "" && env.HooksPath != "" {
			home = filepath.Dir(env.HooksPath)
		}
		cwd := env.Cwd
		if cwd == "" {
			cwd = env.RepoRoot
		}
		if cwd == "" {
			cwd = home
		}
		var checked hooks.CheckResult
		var probeErr error
		if available {
			probe, cancel := context.WithTimeout(ctx, 25*time.Second)
			var scopes []hooks.Scope
			if globalHookScope(env).Validate() == nil {
				scopes = append(scopes, globalHookScope(env))
			}
			if env.RepoRoot != "" && hooks.ProjectScope(env.RepoRoot, env.CodexHome).Validate() == nil {
				scopes = append(scopes, hooks.ProjectScope(env.RepoRoot, env.CodexHome))
			}
			if len(scopes) > 0 {
				checked, probeErr = check(probe, home, cwd, scopes...)
			}
			cancel()
		}
		if globalPresent {
			out = append(out, hookTrustResult(env, false, available, checked, probeErr))
		}
		if projectPresent {
			out = append(out, hookTrustResult(env, true, available, checked, probeErr))
		}
		covered := func(h appserver.HookInfo) bool {
			return (globalPresent && (h.SourcePath == "" || globalHookScope(env).References(h.SourcePath))) || (projectPresent && (h.SourcePath == "" || hooks.ProjectScope(env.RepoRoot, env.CodexHome).References(h.SourcePath)))
		}
		for _, h := range checked.StillTrusted {
			if !covered(h) {
				out = append(out, Result{ID: "other-hook-trust", Name: "other hooks still trusted", Status: Fail, Detail: "STILL TRUSTED from " + hooks.Display(h.SourcePath) + ": " + hooks.Display(h.Command), Hint: hooks.Display(hooks.RevokeHint(home, h))})
			}
		}
		for _, h := range checked.Incompatible {
			if !covered(h) {
				out = append(out, Result{ID: "hook-compatibility", Name: "Codex hook compatibility", Status: Warn, Detail: hooks.Display(h.ValidationIssue), Hint: "Update codexrig; no trust was changed or revocation recommended."})
			}
		}
		if len(checked.Warnings) > 0 {
			warnings := make([]string, len(checked.Warnings))
			for i, w := range checked.Warnings {
				warnings[i] = hooks.Display(w)
			}
			out = append(out, Result{ID: "hook-warnings", Name: "Codex hook warnings", Status: Warn, Detail: strings.Join(warnings, "\n")})
		}
	}

	return out
}

func hookTrustResult(env Env, project, available bool, checked hooks.CheckResult, probeErr error) Result {
	r := Result{ID: "hook-trust", Name: "hooks trusted", Status: Warn}
	cwd, command := env.CodexHome, "codexrig global trust"
	scope := globalHookScope(env)
	if project {
		r.ID, r.Name = "project-hook-trust", "repo hooks trusted"
		cwd, command = env.RepoRoot, "codexrig project trust"
		scope = hooks.ProjectScope(env.RepoRoot, env.CodexHome)
	}
	if !available {
		r.Status = Info
		r.Detail = "not checked — Codex app server is unavailable"
		return r
	}
	if probeErr != nil {
		r.Detail = "could not ask Codex: " + hooks.Display(probeErr.Error())
		return r
	}
	var untrusted []string
	var disabled []string
	seen := false
	for _, h := range checked.Ours {
		if !scope.References(h.SourcePath) {
			continue
		}
		seen = true
		if h.Trusted() && !h.Enabled {
			disabled = append(disabled, hooks.Display(h.EventName))
		}
		if !h.Trusted() {
			untrusted = append(untrusted, hooks.Display(h.EventName))
		}
	}
	var lookalikes []appserver.HookInfo
	for _, h := range checked.Lookalikes {
		if scope.References(h.SourcePath) {
			lookalikes = append(lookalikes, h)
		}
	}
	if len(untrusted) > 0 {
		r.Status = Fail
		if project {
			r.Status = Warn
		}
		r.Detail = "Codex is silently skipping: " + strings.Join(untrusted, ", ")
		r.Hint = "Run `" + command + "`"
		// Repository hooks require deliberate trust outside doctor --fix.
		if !project {
			r.FixLabel = "record these hooks as trusted"
			r.Fix = func(c context.Context) error {
				res, err := hooks.Trust(c, env.CodexHome, cwd, scope)
				hooks.WriteTrustResult(env.Output, res)
				return err
			}
		}
	} else if len(disabled) > 0 {
		r.Status, r.Detail = Warn, "trusted but disabled: "+strings.Join(disabled, ", ")
		r.Hint = "Enable these hooks in Codex after reviewing them"
	} else if seen {
		r.Status, r.Detail = OK, "Codex will run them"
	} else if len(lookalikes) == 0 && !hasScopedDiagnostics(scope, checked) {
		// Callers only request this row when the file has current hooks or lookalikes.
		r.Detail = "Codex did not report the installed codexrig hooks or lookalikes"
	}
	if len(lookalikes) > 0 {
		if r.Status == OK {
			r.Status = Warn
		}
		if r.Detail != "" {
			r.Detail += "\n"
		}
		r.Detail += "lookalike or out of date: " + describeHookCommands(lookalikes)
		r.Hint += "\nReinstall outdated codexrig hooks; review lookalikes in Codex itself."
	}
	for _, h := range checked.Unreported {
		if scope.References(h.SourcePath) {
			if r.Status != Fail {
				r.Status = Warn
			}
			r.Detail += "\ntrust unknown: Codex did not list " + hooks.Display(h.EventName) + ": " + hooks.Display(h.Command)
		}
	}
	for _, h := range checked.StillTrusted {
		if h.SourcePath == "" || scope.References(h.SourcePath) {
			r.Status = Fail
			r.Fix = nil
			r.FixLabel = ""
			r.Detail += "\nSTILL TRUSTED: Codex has approved this hook; codexrig did not approve it today: " + hooks.Display(h.Command)
			r.Hint += "\n" + hooks.Display(hooks.RevokeHint(env.CodexHome, h))
		}
	}
	for _, h := range checked.Incompatible {
		if scope.References(h.SourcePath) {
			if r.Status != Fail {
				r.Status = Warn
			}
			r.Fix = nil
			r.FixLabel = ""
			r.Detail += "\n" + hooks.Display(h.ValidationIssue)
			r.Hint += "\nUpdate codexrig; no revocation is recommended for an unrecognised listing field."
		}
	}
	r.Detail = strings.TrimSpace(r.Detail)
	r.Hint = strings.TrimSpace(r.Hint)
	return r
}

func hasScopedDiagnostics(scope hooks.Scope, checked hooks.CheckResult) bool {
	for _, list := range [][]appserver.HookInfo{checked.StillTrusted, checked.Incompatible, checked.Unreported} {
		for _, h := range list {
			if h.SourcePath == "" || scope.References(h.SourcePath) {
				return true
			}
		}
	}
	return false
}

func globalHookScope(env Env) hooks.Scope {
	home := env.CodexHome
	if home == "" && env.HooksPath != "" {
		home = filepath.Dir(env.HooksPath)
	}
	return hooks.GlobalScope(home)
}

func describeHookCommands(list []appserver.HookInfo) string {
	var details []string
	for _, h := range list {
		text := hooks.Display(h.EventName) + ": " + hooks.Display(h.Command)
		if h.ValidationIssue != "" {
			text += " (" + hooks.Display(h.ValidationIssue) + ")"
		}
		details = append(details, text)
	}
	return strings.Join(details, "; ")
}

func userHookChecks(env Env) ([]Result, hooks.CheckResult) {
	var out []Result
	if env.HooksPath == "" {
		return out, hooks.CheckResult{}
	}
	installed, err := hooks.Status(env.HooksPath, globalHookScope(env))
	if err != nil {
		return append(out, Result{ID: "user-hooks", Name: "sync hooks", Status: Warn, Detail: hooks.Display(err.Error())}), hooks.CheckResult{}
	}

	want := hooks.SyncPlans()
	have := map[string]bool{}
	for _, ev := range installed.Events() {
		have[ev] = true
	}
	var missing []string
	for _, pl := range want {
		if !have[string(pl.Event)] {
			missing = append(missing, string(pl.Event))
		}
	}
	switch {
	case len(installed.Lookalikes) > 0:
		detail := "lookalike or out of date, reinstall: " + describeHookCommands(installed.Lookalikes)
		if len(missing) > 0 {
			detail += "; missing current hooks: " + strings.Join(missing, ", ")
		}
		out = append(out, Result{ID: "user-hooks", Name: "sync hooks", Status: Warn, Detail: detail,
			Hint:     "Run `codexrig global install`, review the result, then `codexrig global trust`",
			FixLabel: "reinstall the sync hooks", Fix: func(context.Context) error {
				_, _, err := hooks.InstallScope(globalHookScope(env), env.Output)
				return err
			},
		})
	case len(installed.Ours) == 0:
		out = append(out, Result{ID: "user-hooks", Name: "sync hooks", Status: Warn, Detail: "not installed",
			FixLabel: "install the sync hooks into your Codex home", Fix: func(context.Context) error {
				_, _, err := hooks.InstallScope(globalHookScope(env), env.Output)
				return err
			},
		})
	case len(missing) > 0:
		out = append(out, Result{ID: "user-hooks", Name: "sync hooks", Status: Warn, Detail: "missing: " + strings.Join(missing, ", "),
			FixLabel: "install the missing sync hooks", Fix: func(context.Context) error {
				_, _, err := hooks.InstallScope(globalHookScope(env), env.Output)
				return err
			},
		})
	default:
		out = append(out, Result{ID: "user-hooks", Name: "sync hooks", Status: OK, Detail: hooks.Display(strings.Join(installed.Events(), ", "))})
	}
	if len(installed.Lookalikes) == 0 {
		if drifted, derr := hooks.Drift(env.HooksPath, want); derr == nil && len(drifted) > 0 {
			out = append(out, Result{ID: "hook-drift", Name: "hook commands", Status: Warn, Detail: "out of date, reinstall: " + strings.Join(drifted, ", "),
				FixLabel: "bring the hook commands up to date", Fix: func(context.Context) error {
					_, _, err := hooks.InstallScope(globalHookScope(env), env.Output)
					return err
				},
			})
		}
	}
	for i := range out {
		if !hooks.CanInstall(globalHookScope(env)) {
			out[i].Fix = nil
			out[i].FixLabel = ""
			if len(installed.Lookalikes) > 0 {
				out[i].Hint = "Review lookalikes manually in the hooks file; the safe installer cannot repair them."
			}
		}
	}
	return out, installed
}
