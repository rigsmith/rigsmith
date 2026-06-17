package cli

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/spf13/cobra"
)

// newDoctorCmd builds `shiprig doctor` — the changeset baseline (shared with
// changerig) plus shiprig's release-readiness checks: the GitHub CLI (forge /
// releases) and the publish toolchain each detected ecosystem needs. The release
// checks are report-only — shiprig can't install a system package manager, so it
// reports presence and points at how to get it.
func newDoctorCmd() *cobra.Command {
	var fixAll bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Health-check the release setup: changesets, forge, publish tooling",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return commands.RunDoctor(cmd, "shiprig", brand.AccentShip, fixAll, releaseDoctorSections)
		},
	}
	cmd.Flags().BoolVar(&fixAll, "fix", false, "apply every fixable issue without prompting")
	return cmd
}

// releaseDoctorSections is the extra section shiprig appends to the changeset
// baseline: the forge CLI plus the per-ecosystem publish tools. It reuses the
// baseline's Discovery, so the workspace is scanned once.
func releaseDoctorSections(ctx context.Context, ws *commands.Workspace, disc commands.Discovery) []doctor.Section {
	results := append([]doctor.Result{checkGh(ctx)}, publishToolChecks(disc)...)
	return []doctor.Section{
		{Title: "release", Results: results},
		{Title: "packages", Results: packageChecks(ctx, ws)},
	}
}

// packageChecks reports the packages a release will build: a summary line
// (releasing / private / ignored), and a fixable scope warning whose fix opens
// the include/exclude picker. It computes the disposition, then defers the
// result shaping to the pure packageResults.
func packageChecks(ctx context.Context, ws *commands.Workspace) []doctor.Result {
	rps, err := commands.ReleasePackages(ctx, ws)
	if err != nil {
		return []doctor.Result{{Name: "release plan", Status: doctor.Warn,
			Detail: "could not compute the release plan: " + err.Error()}}
	}
	return packageResults(rps, len(ws.Config.Paths) > 0,
		func(ctx context.Context) error { return RunPackagePicker(ctx, ws) })
}

// packageResults builds the packages-section results from the disposition list:
// always a summary, plus a scope warning when packages would publish with no
// planned version bump (the drift demos and test fixtures cause). The warning is
// gated on pathsScoped: a configured `paths` already curates which roots are
// scanned, so an unplanned package within it is just one with no changes this
// run — not drift worth nagging about. Pure (no discovery) so it's unit-testable.
func packageResults(rps []commands.ReleasePkg, pathsScoped bool, fix func(context.Context) error) []doctor.Result {
	var releasing, private, ignored int
	var unplanned []string // would publish (not ignored, not private) but no planned bump
	for _, p := range rps {
		switch {
		case p.Ignored:
			ignored++
		case p.Private:
			private++
		case p.Releasing():
			releasing++
		default:
			unplanned = append(unplanned, p.Name)
		}
	}

	rs := []doctor.Result{{Name: "release plan", Status: doctor.Info,
		Detail: fmt.Sprintf("%d releasing · %d private · %d ignored (of %d discovered)",
			releasing, private, ignored, len(rps))}}

	if len(unplanned) > 0 && !pathsScoped {
		sort.Strings(unplanned)
		rs = append(rs, doctor.Result{
			Name:     "publish scope",
			Status:   doctor.Warn,
			Detail:   fmt.Sprintf("%d package(s) would publish with no planned version bump: %s", len(unplanned), previewList(unplanned, 4)),
			Hint:     "scope discovery with `paths`, or exclude them — `shiprig packages` toggles the ignore list",
			FixLabel: "review which packages to build (opens the include/exclude picker)",
			Fix:      fix,
		})
	}
	return rs
}

// previewList joins up to n names, summarizing the remainder as "+K more".
func previewList(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(names[:n], ", "), len(names)-n)
}

func checkGh(ctx context.Context) doctor.Result {
	if _, err := exec.LookPath("gh"); err != nil {
		return doctor.Result{Name: "gh", Status: doctor.Warn, Detail: "not installed",
			Hint: "needed to create GitHub releases and open PRs — https://cli.github.com"}
	}
	if err := exec.CommandContext(ctx, "gh", "auth", "status").Run(); err != nil {
		return doctor.Result{Name: "gh", Status: doctor.Warn, Detail: "not authenticated", Hint: "run `gh auth login`"}
	}
	return doctor.Result{Name: "gh", Status: doctor.OK, Detail: "authenticated"}
}

// publishBinaries maps a detected ecosystem to the binary shiprig publishes with.
// Go modules publish by pushing a git tag, so they need no tool (reported as info).
var publishBinaries = map[string]string{
	"dotnet": "dotnet",
	"node":   "npm",
	"cargo":  "cargo",
}

// desktopArtifactTools names the toolchain each desktop ecosystem builds its
// installers with. Like Go they need no registry publish tool — they release by
// git tag + forge artifacts — so they're reported as info rows (a PATH check
// would be unreliable: the Tauri CLI is a cargo subcommand and electron's builder
// is usually run via npx from node_modules).
var desktopArtifactTools = map[string]string{
	"tauri":    "cargo tauri build",
	"electron": "electron-builder / electron-forge",
}

// publishToolChecks reports, per detected ecosystem, whether the tool shiprig
// would publish it with is installed. Missing is a warn — it only bites at
// `shiprig publish` time — and stays report-only (no install command shiprig owns).
func publishToolChecks(disc commands.Discovery) []doctor.Result {
	if disc.Err != nil {
		return nil
	}
	return publishResults(commands.UniqueEcosystems(disc.Ecosystems))
}

// publishResults is the pure ecosystem→tool mapping, split out so it's testable
// without a real workspace. Unknown ecosystems contribute nothing; Go is an info
// row (no publish tool); the rest report their publish binary's presence.
func publishResults(ecos []string) []doctor.Result {
	var rs []doctor.Result
	for _, id := range ecos {
		bin, ok := publishBinaries[id]
		if !ok {
			switch {
			case id == "go":
				rs = append(rs, doctor.Result{Name: "go", Status: doctor.Info,
					Detail: "publishes via git tags — no tool needed"})
			case desktopArtifactTools[id] != "":
				rs = append(rs, doctor.Result{Name: id, Status: doctor.Info,
					Detail: "released as forge artifacts — built with " + desktopArtifactTools[id]})
			}
			continue
		}
		if _, err := exec.LookPath(bin); err != nil {
			rs = append(rs, doctor.Result{Name: bin, Status: doctor.Warn,
				Detail: "not installed — needed to publish " + id + " packages",
				Hint:   "install " + bin + " so `shiprig publish` can release " + id})
			continue
		}
		rs = append(rs, doctor.Result{Name: bin, Status: doctor.OK, Detail: "publishes " + id})
	}
	return rs
}
