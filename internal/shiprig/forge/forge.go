// Package forge creates per-package GitHub releases, idempotently.
//
// Ported from net-changesets Commands/Release/ForgeReleaseService.cs (the
// githubRelease native step). For each released package it creates a release
// tagged {package}@{version} with the notes lifted from that package's
// CHANGELOG.md, skipping releases that already exist. In auto mode it first
// checks that origin is a GitHub remote and gh is authenticated, degrading to
// "tags only" (a no-op here) otherwise — never erroring just because gh is
// missing. Versioning/changelog/tagging stay with the other steps; this only
// adds the forge release, per the "orchestrate, don't reimplement" scope.
package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// Mode is whether and how the githubRelease step creates forge releases.
type Mode int

const (
	// Auto creates GitHub releases only when origin is GitHub and gh is
	// available; otherwise skips.
	Auto Mode = iota
	// GitHub always creates GitHub releases.
	GitHub
	// None never creates forge releases (tags are handled by the publish/tag
	// steps). The CLI's --git-only override forces this mode.
	None
)

// String returns the canonical config spelling of the mode.
func (m Mode) String() string {
	switch m {
	case GitHub:
		return "github"
	case None:
		return "none"
	default:
		return "auto"
	}
}

// ParseMode interprets a config string case-insensitively; anything
// unrecognized (including the empty string) defaults to Auto. An explicit
// --git-only override should bypass this and pass None directly.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return None
	case "github":
		return GitHub
	default:
		return Auto
	}
}

// Runner executes a command in dir and returns its combined output. A spawn
// failure or non-zero exit must be reported as a non-nil error (the same shape
// as core/changelog.Runner). Probe failures (git remote lookup, gh auth
// status, gh release view) are degraded to "unavailable"/"missing", never
// surfaced as errors.
type Runner func(dir, name string, args ...string) (string, error)

// changelogFileName mirrors the C# Constants.ChangelogFileName.
const changelogFileName = "CHANGELOG.md"

// Run creates one GitHub release per package, idempotently. Packages matching
// the config ignore globs are filtered out and the rest are processed in
// ordinal order of their display title. Per-package progress (skips, command
// starts, command output) is sent through report; the returned message is the
// run-level summary for the early-exit paths ("no packages", mode disabled,
// auto-skip) or the failure description. ok is false only when a
// `gh release create` exits non-zero.
// ecoOf maps a package name to its ecosystem id (e.g. "go", "node"), so the
// release tag matches the one the tag/publish steps pushed. A package missing
// from the map falls back to the name@version convention.
// attach maps a package name to the artifact file paths to upload to its release
// (the `build` step's Attach:true outputs); nil/empty for packages with nothing
// to attach. Uploads are idempotent (`--clobber`), so a re-run replaces assets.
func Run(packages []plugin.Package, ecoOf map[string]string, attach map[string][]string, cfg *config.Config, mode Mode, repoRoot string, run Runner, report func(lines ...string)) (ok bool, message string) {
	if report == nil {
		report = func(...string) {}
	}

	released := make([]plugin.Package, 0, len(packages))
	for _, pkg := range packages {
		if cfg != nil && cfg.IsIgnored(pkg.Name) {
			continue
		}
		released = append(released, pkg)
	}
	sort.SliceStable(released, func(i, j int) bool {
		return title(released[i]) < title(released[j])
	})

	if len(released) == 0 {
		return true, "No packages found; nothing to release."
	}

	if !shouldCreateReleases(mode, repoRoot, run) {
		if mode == None {
			return true, "Forge releases disabled; tags are handled by the publish/tag steps."
		}
		return true, "No GitHub remote or gh unavailable; skipped GitHub releases (tags only)."
	}

	for _, pkg := range released {
		// The positional arg must be the tag the tag/publish steps actually pushed
		// (Go: dir/vX.Y.Z), so gh attaches the release to it instead of creating a
		// new, divergent tag at HEAD. The human-facing title keeps the friendly
		// DisplayName@version form.
		tag := gitutil.PackageTag(ecoOf[pkg.Name], pkg.Dir, pkg.Name, pkg.Version)
		releaseTitle := title(pkg) + "@" + pkg.Version

		if releaseExists(tag, repoRoot, run) {
			report(tag + ": release already exists, skipped create.")
		} else {
			notes := extractNotes(pkg, repoRoot)
			if notes == "" {
				notes = releaseTitle
			}

			argv := []string{"gh", "release", "create", tag, "--title", releaseTitle, "--notes", notes}
			report("githubRelease: " + strings.Join(argv, " "))
			out, err := run(repoRoot, argv[0], argv[1:]...)
			if out != "" {
				report(strings.Split(out, "\n")...)
			}
			if err != nil {
				return false, fmt.Sprintf("githubRelease %s failed: %v", tag, err)
			}
		}

		// Attach the build's assets (binaries/archives). Idempotent via --clobber,
		// so this also back-fills assets onto an already-existing release.
		if files := attach[pkg.Name]; len(files) > 0 {
			argv := append([]string{"gh", "release", "upload", tag}, files...)
			argv = append(argv, "--clobber")
			report("githubRelease: " + strings.Join(argv, " "))
			out, err := run(repoRoot, argv[0], argv[1:]...)
			if out != "" {
				report(strings.Split(out, "\n")...)
			}
			if err != nil {
				return false, fmt.Sprintf("githubRelease upload %s failed: %v", tag, err)
			}
		}
	}

	return true, ""
}

// title is the package's display title: DisplayName, falling back to Name.
func title(pkg plugin.Package) string {
	if pkg.DisplayName != "" {
		return pkg.DisplayName
	}
	return pkg.Name
}

func shouldCreateReleases(mode Mode, repoRoot string, run Runner) bool {
	switch mode {
	case None:
		return false
	case GitHub:
		return true
	case Auto:
		return isGithubRemote(repoRoot, run) && isGhReady(repoRoot, run)
	default:
		return false
	}
}

// isGithubRemote reports whether `git remote get-url origin` succeeds and its
// output mentions github.com (case-insensitive). Spawn failures and non-zero
// exits are treated as "not GitHub", never an error.
func isGithubRemote(repoRoot string, run Runner) bool {
	out, err := run(repoRoot, "git", "remote", "get-url", "origin")
	return err == nil && strings.Contains(strings.ToLower(out), "github.com")
}

// isGhReady reports whether `gh auth status` exits 0. A missing gh binary or
// unauthenticated state is treated as unavailable, never an error.
func isGhReady(repoRoot string, run Runner) bool {
	_, err := run(repoRoot, "gh", "auth", "status")
	return err == nil
}

// releaseExists reports whether `gh release view <tag>` exits 0.
func releaseExists(tag, repoRoot string, run Runner) bool {
	_, err := run(repoRoot, "gh", "release", "view", tag)
	return err == nil
}

// extractNotes lifts the `## {version}` section out of the package's
// CHANGELOG.md: the lines after the exact (trimmed) header up to the next line
// starting with "## ", joined with \n and trimmed. Returns "" when the file or
// section is missing, or the section is empty — the caller falls back to the
// tag string.
func extractNotes(pkg plugin.Package, repoRoot string) string {
	dir := pkg.Dir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoRoot, dir)
	}

	data, err := os.ReadFile(filepath.Join(dir, changelogFileName))
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}

	header := "## " + pkg.Version
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}

	var section []string
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "## ") {
			break
		}
		section = append(section, line)
	}

	return strings.TrimSpace(strings.Join(section, "\n"))
}
