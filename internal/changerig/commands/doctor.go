package commands

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/core/doctorui"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/core/versionstate"
	"github.com/spf13/cobra"
)

// NewDoctorCmd builds `changerig doctor` — a health check of the changeset setup:
// git, the repo, .changeset/config.json (scaffolded on request), and the detected
// workspace. shiprig reuses the same baseline via RunDoctor/ChangesetDoctorSections
// and layers its release-readiness checks on top, so the two tools never diverge
// on what "a healthy changeset setup" means. The report model and the fix flow are
// the shared core/doctor + core/doctorui.
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

	rs = append(rs, pendingChecks(ctx, ws)...)
	rs = append(rs, checkReleaseRecord(ctx, ws)...)
	return rs
}

// pendingChecks reports the pending changesets: how many there are (neutral
// context), any that can't be parsed, and any that can never release. It reads
// the directory leniently, so one broken file neither hides the others nor
// stops the targets check from running on the ones that did parse. A missing
// .changeset/ says nothing here — checkChangesetConfig already reports it.
func pendingChecks(ctx context.Context, ws *Workspace) []doctor.Result {
	css, bad, err := changeset.DirLenient(ws.ChangesetDir, "")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []doctor.Result{{Name: "pending", Status: doctor.Fail,
			Detail: "can't read .changeset/: " + err.Error(),
			Hint:   "status and version read every changeset there; check the directory's permissions"}}
	}
	rs := []doctor.Result{{Name: "pending", Status: doctor.Info,
		Detail: fmt.Sprintf("%d changeset(s)", len(css)+len(bad))}}
	if r, ok := checkUnparseable(ws, bad); ok {
		rs = append(rs, r)
	}
	if len(css) == 0 && len(bad) > 0 {
		// Every file is broken: "no pending changesets" would contradict the
		// row above, and there is nothing parsed to check targets on.
		return rs
	}
	return append(rs, checkStranded(ctx, ws, css))
}

// checkUnparseable reports changeset files that can't be read or parsed, each
// by file and error. It is a failure, not a warning: status and version refuse
// to run while any one of them is there, so no release can be planned.
func checkUnparseable(ws *Workspace, bad []*changeset.FileError) (doctor.Result, bool) {
	if len(bad) == 0 {
		return doctor.Result{}, false
	}
	items := make([]string, 0, len(bad))
	for _, b := range bad {
		file := b.Path
		if rel, err := filepath.Rel(ws.Root, b.Path); err == nil {
			file = filepath.ToSlash(rel)
		}
		items = append(items, fmt.Sprintf("%s (%v)", file, b.Err))
	}
	return doctor.Result{Name: "changeset files", Status: doctor.Fail,
		Detail: fmt.Sprintf("%d changeset(s) can't be parsed: %s", len(bad), previewNames(items, 3)),
		Hint:   "fix the frontmatter by hand (`\"package\": patch|minor|major` per line), or delete the file; status and version refuse to run until every changeset parses"}, true
}

// checkReleaseRecord compares the release record (versioning.record) with the
// tree and the tags. Two things drift from it without anything else noticing:
// a manifest edited by hand, which the next plan bumps from as if it had been
// released, and a release that was versioned but never tagged, which leaves
// the next commit-sourced plan counting from an older tag. It reports nothing
// when no record is kept.
func checkReleaseRecord(ctx context.Context, ws *Workspace) []doctor.Result {
	const name = "release record"
	if !ws.Config.Versioning.Record {
		return nil
	}
	recorded, err := versionstate.Read(ws.ChangesetDir)
	if err != nil {
		return []doctor.Result{{Name: name, Status: doctor.Fail,
			Detail: fmt.Sprintf("can't read .changeset/%s: %v", versionstate.FileName, err),
			Hint:   "fix the JSON by hand; `version` rewrites it on the next release"}}
	}
	names := recorded.ReleasedNames()
	if len(names) == 0 {
		return []doctor.Result{{Name: name, Status: doctor.Info,
			Detail: "nothing recorded yet: the next `version` records what it releases"}}
	}
	pkgs, ecoOf, err := ws.Discover(ctx)
	if err != nil {
		return []doctor.Result{{Name: name, Status: doctor.Info,
			Detail: "not checked — package discovery failed"}}
	}
	byName := make(map[string]plugin.Package, len(pkgs))
	for _, p := range pkgs {
		byName[p.Name] = p
	}
	solo := len(pkgs) == 1
	var edited, untagged []string
	for _, n := range names {
		p, ok := byName[n]
		if !ok {
			continue // renamed or removed: nothing left to disagree with
		}
		v := recorded.ReleasedAt(n)
		if p.Version != v {
			edited = append(edited, fmt.Sprintf("%s (%s here, %s recorded)", n, p.Version, v))
		}
		if ws.Config.SkipsTag(n) {
			continue
		}
		// Only a package that has been tagged before is expected to be
		// tagged now: one that never is (tagging happens elsewhere, or
		// not at all) says nothing by having no tag.
		anyTag := gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[n], p.Dir, n, "*", solo)
		tag := gitutil.RenderTag(ws.Config.TagTemplate, ecoOf[n], p.Dir, n, v, solo)
		if gitutil.AnyTagMatches(ctx, ws.Root, anyTag) && !gitutil.TagExists(ctx, ws.Root, tag) {
			untagged = append(untagged, tag)
		}
	}
	// Each drift is its own problem with its own remedy, so each gets its own
	// row: a hand-edited manifest must not hide a release that was never tagged.
	var rs []doctor.Result
	if len(edited) > 0 {
		rs = append(rs, doctor.Result{Name: name, Status: doctor.Warn,
			Detail: fmt.Sprintf("%d version(s) differ from the record: %s", len(edited), previewNames(edited, 3)),
			Hint:   "a manifest edited by hand is bumped from as if it had been released; put it back, or release it with a changeset"})
	}
	if len(untagged) > 0 {
		rs = append(rs, doctor.Result{Name: name, Status: doctor.Warn,
			Detail: fmt.Sprintf("%d recorded release(s) have no tag: %s", len(untagged), previewNames(untagged, 3)),
			Hint:   "expected between the version PR's merge and the publish that tags it; otherwise that publish didn't finish — `shiprig tag` (or a re-run of the release) creates them"})
	}
	if len(rs) > 0 {
		return rs
	}
	return []doctor.Result{{Name: name, Status: doctor.OK,
		Detail: fmt.Sprintf("%d package(s) recorded; versions and tags agree", len(names))}}
}

// checkStranded reports changesets that can never release — they name no
// package, an unknown one, or only ignored ones. Nothing else surfaces them: a
// stranded file is counted as pending, parses cleanly, and simply never appears
// in a plan, so it stays invisible until someone wonders why a release came out
// empty. Sixteen accumulated in this repo across a full release cycle, carrying
// three weeks of work that would have shipped with no changelog entry.
func checkStranded(ctx context.Context, ws *Workspace, css []*changeset.Changeset) doctor.Result {
	if len(css) == 0 {
		return doctor.Result{Name: "changeset targets", Status: doctor.OK, Detail: "no pending changesets"}
	}
	pkgs, _, err := ws.Discover(ctx)
	if err != nil {
		// checkWorkspace already reports discovery failures, and without packages
		// every changeset would look like it names an unknown one.
		return doctor.Result{Name: "changeset targets", Status: doctor.Info,
			Detail: "not checked — package discovery failed"}
	}
	stranded := FindStranded(css, pkgs, ws.Config)
	if len(stranded) == 0 {
		return doctor.Result{Name: "changeset targets", Status: doctor.OK,
			Detail: "every pending changeset names a releasable package"}
	}
	names := make([]string, 0, len(stranded))
	for _, s := range stranded {
		names = append(names, s.ID)
	}
	return doctor.Result{
		Name:   "changeset targets",
		Status: doctor.Warn,
		Detail: fmt.Sprintf("%d changeset(s) can never release: %s", len(stranded), previewNames(names, 4)),
		Hint:   StrandedHint("changerig", pkgs, ws.Config) + " `changerig status` lists each one and why.",
	}
}

// previewNames joins up to n names, summarizing the rest as "+K more".
func previewNames(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(names[:n], ", "), len(names)-n)
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
