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
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"

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

// Every released binary has to know its own version. Without a -X ldflag the
// version stays "dev", fang falls through to its source-build description, and
// the tool introduces itself to users as a build from somebody's laptop —
// naming a path on the release runner and the mtime of their own download.
// shiprig and changerig shipped that way from the first release to v1.13.0.
//
// Read out of .goreleaser.yaml rather than asserted per tool, so a fifth CLI
// added to the release cannot ship unversioned without this failing.
func TestEveryReleasedBinaryIsVersionStamped(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var cfg struct {
		Builds []struct {
			ID      string   `yaml:"id"`
			Ldflags []string `yaml:"ldflags"`
		} `yaml:"builds"`
	}
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Builds) == 0 {
		t.Fatal("no builds parsed from .goreleaser.yaml — this guard is checking nothing")
	}

	for _, b := range cfg.Builds {
		stamped := false
		for _, f := range b.Ldflags {
			// Any -X ...version=, wherever the seam lives: rig and shiprig keep
			// theirs in an internal package, clauderig and changerig in main.
			if strings.Contains(f, "version={{.Version}}") {
				stamped = true
			}
		}
		if !stamped {
			t.Errorf("build %q has no version ldflag, so the released binary reports itself as a source build", b.ID)
		}
	}
}

// repoRoot walks up from the test's directory to the module root.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the test's working directory")
		}
		dir = parent
	}
}
