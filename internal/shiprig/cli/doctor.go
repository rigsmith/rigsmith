package cli

import (
	"context"
	"os/exec"

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
func newDoctorCmd(version string) *cobra.Command {
	var fixAll bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Health-check the release setup: changesets, forge, publish tooling",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return commands.RunDoctor(cmd, "shiprig", version, brand.AccentShip, fixAll, releaseDoctorSections)
		},
	}
	cmd.Flags().BoolVar(&fixAll, "fix", false, "apply every fixable issue without prompting")
	return cmd
}

// releaseDoctorSections is the extra section shiprig appends to the changeset
// baseline: the forge CLI plus the per-ecosystem publish tools.
func releaseDoctorSections(ctx context.Context, ws *commands.Workspace) []doctor.Section {
	results := append([]doctor.Result{checkGh(ctx)}, publishToolChecks(ctx, ws)...)
	return []doctor.Section{{Title: "release", Results: results}}
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

// publishToolChecks reports, per detected ecosystem, whether the tool shiprig
// would publish it with is installed. Missing is a warn — it only bites at
// `shiprig publish` time — and stays report-only (no install command shiprig owns).
func publishToolChecks(ctx context.Context, ws *commands.Workspace) []doctor.Result {
	_, ecoOf, err := ws.Discover(ctx)
	if err != nil {
		return nil
	}
	return publishResults(commands.UniqueEcosystems(ecoOf))
}

// publishResults is the pure ecosystem→tool mapping, split out so it's testable
// without a real workspace. Unknown ecosystems contribute nothing; Go is an info
// row (no publish tool); the rest report their publish binary's presence.
func publishResults(ecos []string) []doctor.Result {
	var rs []doctor.Result
	for _, id := range ecos {
		bin, ok := publishBinaries[id]
		if !ok {
			if id == "go" {
				rs = append(rs, doctor.Result{Name: "go", Status: doctor.Info,
					Detail: "publishes via git tags — no tool needed"})
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
