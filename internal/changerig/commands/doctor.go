package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/doctorui"
	"github.com/spf13/cobra"
)

// NewDoctorCmd builds `changerig doctor` — a health check of the changeset setup:
// git, the repo, .changeset/config.json (scaffolded on request), and the detected
// workspace. shiprig reuses the same baseline via RunDoctor/ChangesetDoctorSections
// and layers its release-readiness checks on top, so the two tools never diverge
// on what "a healthy changeset setup" means. The report model and the fix flow are
// the shared core/doctor + internal/doctorui.
func NewDoctorCmd() *cobra.Command {
	var fixAll bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Health-check the changeset setup (--fix to scaffold a missing config)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return RunDoctor(cmd, "changerig", brand.AccentChange, fixAll, nil)
		},
	}
	cmd.Flags().BoolVar(&fixAll, "fix", false, "apply every fixable issue without prompting")
	return cmd
}

// RunDoctor renders a doctor report and runs the fix flow for a changeset-based
// tool. The changeset baseline (git/repo/config/workspace) is always included;
// extra appends tool-specific sections (shiprig's release checks) and may be nil.
// It exits non-zero while any failing check remains, so it's usable in scripts.
func RunDoctor(cmd *cobra.Command, tool string, accent lipgloss.AdaptiveColor, fixAll bool, extra func(context.Context, *Workspace, Discovery) []doctor.Section) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()

	ws, err := Open()
	if err != nil {
		return err
	}
	// Scan the workspace once and share it across the sections — the baseline's
	// workspace row and shiprig's per-ecosystem publish checks both need it, and
	// walking a monorepo twice is wasteful.
	disc := discover(ctx, ws)
	sections := ChangesetDoctorSections(ctx, ws, disc)
	if extra != nil {
		sections = append(sections, extra(ctx, ws, disc)...)
	}

	fmt.Fprintf(out, "%s   %s\n\n", HeaderStyle.Render(tool+" doctor"), DimStyle.Render(runtime.GOOS))
	doctorui.RenderSections(out, sections)
	doctorui.RenderSummary(out, sections)

	fails := doctorui.RunFixes(cmd, sections, doctorui.Options{
		Accent:      accent,
		FixAll:      fixAll,
		Interactive: Interactive(),
	})
	if fails > 0 {
		os.Exit(1)
	}
	return nil
}

// Discovery is a single workspace scan, shared across the doctor sections so the
// repo is walked once. It summarizes ws.Discover: the package count, the
// name→ecosystem map (also consumed by shiprig's publish checks), and any scan
// error for the sections to surface.
type Discovery struct {
	PackageCount int
	Ecosystems   map[string]string
	Err          error
}

func discover(ctx context.Context, ws *Workspace) Discovery {
	pkgs, ecoOf, err := ws.Discover(ctx)
	return Discovery{PackageCount: len(pkgs), Ecosystems: ecoOf, Err: err}
}

// ChangesetDoctorSections builds the shared changeset health checks: the git
// toolchain, then the repo / config / workspace / pending-changeset state. shiprig
// appends its own "release" section to these, reusing the same Discovery.
func ChangesetDoctorSections(ctx context.Context, ws *Workspace, disc Discovery) []doctor.Section {
	return []doctor.Section{
		{Title: "environment", Results: []doctor.Result{checkGit(ctx)}},
		{Title: "changesets", Results: changesetChecks(ctx, ws, disc)},
	}
}

func checkGit(ctx context.Context) doctor.Result {
	if _, err := exec.LookPath("git"); err != nil {
		return doctor.Result{Name: "git", Status: doctor.Fail, Detail: "not found",
			Hint: "install git — changesets diff and tag against git history"}
	}
	v, err := exec.CommandContext(ctx, "git", "--version").Output()
	if err != nil {
		// On PATH but won't run (broken install, permissions) — fail like "not
		// found" rather than report OK with an empty version.
		return doctor.Result{Name: "git", Status: doctor.Fail, Detail: "present but not runnable: " + err.Error(),
			Hint: "git is on PATH but failed to execute — check the install/permissions"}
	}
	return doctor.Result{Name: "git", Status: doctor.OK,
		Detail: strings.TrimSpace(strings.TrimPrefix(firstLine(string(v)), "git version "))}
}

func changesetChecks(ctx context.Context, ws *Workspace, disc Discovery) []doctor.Result {
	var rs []doctor.Result

	if _, err := gitrepo.Open(ctx, ws.Root); err != nil {
		rs = append(rs, doctor.Result{Name: "git repo", Status: doctor.Warn, Detail: "not a git repo",
			Hint: "changeset diffs, since-tags and `tag` need git history — run `git init`"})
	} else {
		rs = append(rs, doctor.Result{Name: "git repo", Status: doctor.OK, Detail: ws.Root})
	}

	rs = append(rs, checkChangesetConfig(ws), checkWorkspace(disc))

	// Pending changesets — neutral context, never a problem.
	if css, err := changeset.Dir(ws.ChangesetDir, ""); err == nil {
		rs = append(rs, doctor.Result{Name: "pending", Status: doctor.Info,
			Detail: fmt.Sprintf("%d changeset(s)", len(css))})
	}
	return rs
}

// checkChangesetConfig reports on .changeset/config.json. An absent config is a
// warn that clauderig-style scaffolds on request (via the same init path); a
// present-but-invalid one is a fail with a manual hint — scaffolding is
// deliberately non-destructive and won't clobber a file the user may be editing.
func checkChangesetConfig(ws *Workspace) doctor.Result {
	if !ws.Initialized() {
		return doctor.Result{Name: "config", Status: doctor.Warn, Detail: "no .changeset/config.json",
			FixLabel: "scaffold .changeset/config.json",
			Fix: func(context.Context) error {
				_, err := Scaffold(ws, config.SourceChangesets)
				return err
			}}
	}
	if _, err := config.Load(ws.ChangesetDir); err != nil {
		return doctor.Result{Name: "config", Status: doctor.Fail,
			Detail: ".changeset/config.json is invalid: " + err.Error(),
			Hint:   "fix it by hand, or remove it and run `changerig init` to regenerate"}
	}
	detail := "valid"
	if src := ws.Config.Versioning.Source; src != "" && src != config.SourceChangesets {
		detail = "valid (source: " + string(src) + ")"
	}
	return doctor.Result{Name: "config", Status: doctor.OK, Detail: detail}
}

func checkWorkspace(disc Discovery) doctor.Result {
	if disc.Err != nil {
		return doctor.Result{Name: "workspace", Status: doctor.Warn, Detail: "discovery failed: " + disc.Err.Error()}
	}
	if disc.PackageCount == 0 {
		return doctor.Result{Name: "workspace", Status: doctor.Warn, Detail: "no packages detected",
			Hint: "changerig versions packages it can find — none matched .NET/Node/Go/Cargo here"}
	}
	return doctor.Result{Name: "workspace", Status: doctor.OK,
		Detail: fmt.Sprintf("%d package(s) across %s", disc.PackageCount, strings.Join(UniqueEcosystems(disc.Ecosystems), ", "))}
}

// UniqueEcosystems returns the distinct ecosystem ids from a name→ecosystem map,
// sorted. Shared with shiprig's release checks so both speak the same set.
func UniqueEcosystems(ecoOf map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ecoOf {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
