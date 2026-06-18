// Package cliconsistency holds the cross-tool CLI consistency gate: it builds
// every tool's command tree and runs core/cliguard against all of them at once.
// It lives in its own package because it's the only place that imports all four
// roots together (rig / shiprig / changerig / clauderig).
//
// Report-only for now: the test prints every violation but does not fail, so the
// remaining items (mostly command groups not yet wired to a menu) can be driven
// to zero. Flip `enforce` to true to gate CI against regressions.
package cliconsistency

import (
	"sort"
	"testing"

	"github.com/rigsmith/rigsmith/core/cliguard"
	changerig "github.com/rigsmith/rigsmith/internal/changerig/commands"
	clauderig "github.com/rigsmith/rigsmith/internal/clauderig/commands"
	rig "github.com/rigsmith/rigsmith/internal/rig/cli"
	shiprig "github.com/rigsmith/rigsmith/internal/shiprig/cli"
	"github.com/spf13/cobra"
)

// enforce flips the guard from report-only (t.Log) to hard-fail (t.Error). Now
// that the surface is clean, it's true: any new command that breaks a convention
// (a canonical flag with the wrong shorthand, a --list flag, a doctor without
// --fix, a bare command group that won't open a menu) fails CI.
const enforce = true

func roots() []*cobra.Command {
	return []*cobra.Command{
		rig.NewRootCmd(),
		shiprig.NewRootCmd(),
		changerig.NewRootCmd(),
		clauderig.NewRootCmd("dev"),
	}
}

func TestCLIConsistency(t *testing.T) {
	var all []cliguard.Violation
	for _, root := range roots() {
		all = append(all, cliguard.Check(root)...)
	}
	if len(all) == 0 {
		return
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Rule != all[j].Rule {
			return all[i].Rule < all[j].Rule
		}
		return all[i].Path < all[j].Path
	})
	report := cliguard.Report(all)
	if enforce {
		t.Errorf("CLI consistency: %d violation(s)\n%s", len(all), report)
		return
	}
	t.Logf("CLI consistency (report-only): %d violation(s)\n%s\nFlip `enforce` to true once these reach zero.", len(all), report)
}
