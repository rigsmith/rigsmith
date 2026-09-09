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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
			// Any -X ...version= from a template, wherever the seam lives: rig
			// and shiprig keep theirs in an internal package, clauderig and
			// changerig in main. The template need not be {{.Version}} — the
			// window is its own module at its own version, and takes that from
			// the environment — but there must be one, or the binary reports
			// itself as a source build.
			if strings.Contains(f, "version={{") {
				stamped = true
			}
		}
		if !stamped {
			t.Errorf("build %q has no version ldflag, so the released binary reports itself as a source build", b.ID)
		}
	}
}

// The window is a separate module at a separate version. Stamping it with the
// repository's tag would have it report a version it does not have — the whole
// reason it was split out — so it must not use {{.Version}}, and whatever it
// does use has to be set for it.
func TestTheWindowIsStampedWithItsOwnVersion(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	// .goreleaser.ui.yaml, not .goreleaser.yaml: the window has its own release
	// lane now, on its own tag. The guard follows it there rather than passing
	// because the build it was watching is no longer in the file it was reading.
	body, err := os.ReadFile(filepath.Join(root, ".goreleaser.ui.yaml"))
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

	var found bool
	for _, b := range cfg.Builds {
		if b.ID != "clauderig-ui" {
			continue
		}
		found = true
		for _, f := range b.Ldflags {
			if strings.Contains(f, "version={{.Version}}") {
				t.Error("the window is stamped with the repository's tag, not its own version")
			}
		}
	}
	if !found {
		t.Fatal("no clauderig-ui build in .goreleaser.ui.yaml — this guard is checking nothing")
	}

	// And it is not back in the CLIs' lane, which would ship it twice — once
	// per release — under two different versions.
	cli, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cli), "id: clauderig-ui") {
		t.Error("the window is built by the CLIs' release too — it ships on ui/vX.Y.Z now")
	}

	// And the module it comes from carries a version for the release to read.
	mod, err := os.ReadFile(filepath.Join(root, "ui", "go.mod"))
	if err != nil {
		t.Fatalf("ui is not its own module: %v", err)
	}
	if !strings.Contains(string(mod), "rigsmith:version") {
		t.Error("ui/go.mod has no rigsmith:version, so nothing decides what the window reports")
	}

	// The workflow is what puts it in the environment — the window's own one,
	// since it ships on ui/vX.Y.Z rather than with the CLIs.
	wf, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release-ui.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wfText := string(wf)
	if !strings.Contains(wfText, "UI_VERSION") {
		t.Error("the release workflow never sets UI_VERSION, so the stamp would be empty")
	}

	// The window ships from two jobs — GoReleaser builds the Windows binary, a
	// macOS job packages the app — and a third publishes them. All of them need
	// the same number, and they read it through one script, because two copies
	// of "where the version comes from" is how the same window ends up shipping
	// under two of them. That script also refuses a tag the module disagrees
	// with, which is the check that cannot be written in YAML.
	if n := strings.Count(wfText, "scripts/ui-release-version.sh"); n < 2 {
		t.Errorf("only %d job(s) read the window's version through scripts/ui-release-version.sh; "+
			"the Windows build, the macOS packaging and the publish all need it", n)
	}
	// And the app is not packaged straight from the tag. The tag does name this
	// version now, but only after ui-release-version.sh has checked it against
	// the module — taking it raw would skip the one thing standing between a
	// mistyped tag and a window that reports a number nothing else agrees with.
	for _, line := range strings.Split(wfText, "\n") {
		if strings.Contains(line, "package-ui.sh") && strings.Contains(line, "GITHUB_REF_NAME") {
			t.Error("the macOS app is packaged with the repository's tag, not the window's version")
		}
		// The cask needs both: the version it is named for, and the tag whose
		// release holds the download. They encode the same number now that the
		// window has its own tag, but they are still different strings — "0.2.0"
		// and "ui/v0.2.0" — and writing either where the other belongs gives a
		// cask that 404s or one that claims the wrong version.
		if strings.Contains(line, "publish-ui-cask.sh") {
			if !strings.Contains(line, "$UI_VERSION") {
				t.Error("the Homebrew cask is named for the repository's tag, not the window's version")
			}
			if !strings.Contains(line, "GITHUB_REF_NAME") {
				t.Error("the cask is not told which release its download lives in, so its url will 404")
			}
		}
	}
}

// Every binary shipped for Windows needs version resources, and the way to find
// out that one does not is for somebody to right-click the .exe — or, worse, for
// winget to classify it from the metadata it does not have. komac reads
// FileDescription and OriginalFilename to decide whether a binary is an
// installer or a portable, and an .exe with neither is an .exe it has to guess
// about.
//
// The window shipped exactly like that for its whole life: build/winres/ had an
// entry per CLI and none for it, so scripts/winres.sh embedded nothing, and
// nothing anywhere said so.
func TestEveryWindowsBinaryHasVersionResources(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	type build struct {
		ID     string   `yaml:"id"`
		Binary string   `yaml:"binary"`
		Goos   []string `yaml:"goos"`
	}
	var binaries []string
	for _, cfg := range []string{".goreleaser.yaml", ".goreleaser.ui.yaml"} {
		body, rerr := os.ReadFile(filepath.Join(root, cfg))
		if rerr != nil {
			t.Fatal(rerr)
		}
		var parsed struct {
			Builds []build `yaml:"builds"`
		}
		if uerr := yaml.Unmarshal(body, &parsed); uerr != nil {
			t.Fatalf("%s: %v", cfg, uerr)
		}
		for _, b := range parsed.Builds {
			if slices.Contains(b.Goos, "windows") {
				binaries = append(binaries, b.Binary)
			}
		}
	}
	if len(binaries) == 0 {
		t.Fatal("no Windows builds found in either config — this guard is checking nothing")
	}

	for _, bin := range binaries {
		path := filepath.Join(root, "build", "winres", bin+".json")
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Errorf("%s.exe ships for Windows with no build/winres/%s.json, so it carries no icon, "+
				"no version and no description: %v", bin, bin, rerr)
			continue
		}
		// And the resource describes THAT binary. A config copied from another
		// tool names the wrong file, which is how one .exe ends up reporting
		// another's identity in its properties dialog.
		var cfg struct {
			Version map[string]map[string]struct {
				Info map[string]map[string]string `json:"info"`
			} `json:"RT_VERSION"`
		}
		if uerr := json.Unmarshal(raw, &cfg); uerr != nil {
			t.Errorf("%s: %v", path, uerr)
			continue
		}
		var named bool
		for _, block := range cfg.Version {
			for _, lang := range block {
				for _, fields := range lang.Info {
					if fields["OriginalFilename"] == bin+".exe" {
						named = true
					}
				}
			}
		}
		if !named {
			t.Errorf("build/winres/%s.json does not name %s.exe as its OriginalFilename", bin, bin)
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
