// Package forge creates per-package forge releases (GitHub / GitLab / Gitea),
// idempotently.
//
// Ported from net-changesets Commands/Release/ForgeReleaseService.cs (the
// release native step) and generalized from GitHub-only to a Provider seam. For
// each released package it creates a release tagged {package}@{version} with the
// notes lifted from that package's CHANGELOG.md, skipping releases that already
// exist, and attaches the build's assets. The forge is chosen by Selection:
// `auto` picks the first provider whose host matches origin and whose CLI is
// ready; an explicit `github|gitlab|gitea` forces one; `none` skips. A missing
// or unauthenticated CLI degrades to "tags only" (a no-op here), never an
// error. Versioning/changelog/tagging stay with the other steps; this only adds
// the forge release, per the "orchestrate, don't reimplement" scope.
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

// Selection is how the release step chooses a forge, from the `release` step's
// `forge`/`forgeURL` config (and the CLI's --git-only override).
type Selection struct {
	// Forge is "" or "auto" (auto-detect), "none" (tags only), or an explicit
	// "github" | "gitlab" | "gitea".
	Forge string
	// URL is the self-hosted forge base URL, for an explicit gitlab/gitea on a
	// non-SaaS host. Informational for selection — the forge CLI infers the repo
	// from its own login/remote config; github.com/gitlab.com need none.
	URL string
}

// selectProvider resolves the Selection to a concrete Provider, or returns a
// nil provider plus the user-facing reason the release step is being skipped
// (tags-only). Selection never errors: a missing CLI or unsupported remote is a
// skip, matching the original gh "degrade to tags only" contract, now per-forge.
func selectProvider(sel Selection, repoRoot string, run Runner) (Provider, string) {
	switch strings.ToLower(strings.TrimSpace(sel.Forge)) {
	case "none":
		return nil, "Forge releases disabled; tags are handled by the publish/tag steps."
	case "", "auto":
		origin := originURL(repoRoot, run)
		for _, p := range defaultProviders() {
			if p.Matches(origin) && p.Ready(repoRoot, run) {
				return p, ""
			}
		}
		return nil, "No supported forge remote or its CLI is unavailable; skipped releases (tags only)."
	default:
		p := providerByName(sel.Forge)
		if p == nil {
			return nil, fmt.Sprintf("Unknown forge %q; skipped releases (tags only).", sel.Forge)
		}
		if !p.Ready(repoRoot, run) {
			return nil, fmt.Sprintf("%s CLI unavailable or not authenticated; skipped releases (tags only).", p.Name())
		}
		return p, ""
	}
}

// originURL returns the trimmed `git remote get-url origin`, or "" on failure
// (treated as "no recognizable remote", never an error).
func originURL(repoRoot string, run Runner) string {
	out, err := run(repoRoot, "git", "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Runner executes a command in dir and returns its combined output. A spawn
// failure or non-zero exit must be reported as a non-nil error (the same shape
// as core/changelog.Runner). Probe failures (git remote lookup, CLI auth
// status, release lookup) are degraded to "unavailable"/"missing", never
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
// to attach. Uploads are idempotent on forges that support it (gh --clobber).
func Run(packages []plugin.Package, ecoOf map[string]string, attach map[string][]string, cfg *config.Config, sel Selection, repoRoot string, run Runner, report func(lines ...string)) (ok bool, message string) {
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

	provider, skip := selectProvider(sel, repoRoot, run)
	if provider == nil {
		return true, skip
	}

	for _, pkg := range released {
		// The positional tag must be the one the tag/publish steps actually pushed
		// (Go: dir/vX.Y.Z), so the forge attaches the release to it instead of
		// creating a new, divergent tag at HEAD. The human-facing title keeps the
		// friendly DisplayName@version form.
		tag := gitutil.PackageTag(ecoOf[pkg.Name], pkg.Dir, pkg.Name, pkg.Version)
		releaseTitle := title(pkg) + "@" + pkg.Version

		if provider.ReleaseExists(tag, repoRoot, run) {
			report(tag + ": release already exists, skipped create.")
		} else {
			notes := extractNotes(pkg, repoRoot)
			if notes == "" {
				notes = releaseTitle
			}
			if ok, msg := runForgeCmd(provider.CreateReleaseCmd(Release{Tag: tag, Title: releaseTitle, Notes: notes}), repoRoot, run, report); !ok {
				return false, fmt.Sprintf("release %s failed: %s", tag, msg)
			}
		}

		// Attach the build's assets (binaries/archives). On a forge that can't
		// upload to an existing release (Gitea), report a skip rather than fail.
		if files := attach[pkg.Name]; len(files) > 0 {
			argv := provider.UploadAssetsCmd(tag, files)
			if argv == nil {
				report(fmt.Sprintf("%s: %s cannot attach assets to an existing release; skipped %d asset(s).", tag, provider.Name(), len(files)))
				continue
			}
			if ok, msg := runForgeCmd(argv, repoRoot, run, report); !ok {
				return false, fmt.Sprintf("release upload %s failed: %s", tag, msg)
			}
		}
	}

	return true, ""
}

// Notes returns the package's CHANGELOG section for its current version (the
// ${changelog.<pkg>} value), or "" when absent. Exported wrapper over the same
// extraction the release step uses for release notes.
func Notes(pkg plugin.Package, repoRoot string) string {
	return extractNotes(pkg, repoRoot)
}

// ReleaseURL returns the web URL of the forge release for tag (the
// ${releaseUrl.<pkg>} value), or "" when the forge is disabled/unavailable, has
// no URL command, the release does not exist yet, or the lookup fails. The forge
// is chosen exactly as the release step chooses it.
func ReleaseURL(sel Selection, tag, repoRoot string, run Runner) string {
	provider, _ := selectProvider(sel, repoRoot, run)
	if provider == nil {
		return ""
	}
	argv := provider.ReleaseURLCmd(tag)
	if argv == nil {
		return ""
	}
	out, err := run(repoRoot, argv[0], argv[1:]...)
	if err != nil {
		return ""
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
}

// runForgeCmd reports and executes one forge argv, streaming its output. It
// returns ok=false with the error string on a non-zero exit, so the caller can
// frame the failure with the tag.
func runForgeCmd(argv []string, repoRoot string, run Runner, report func(...string)) (ok bool, msg string) {
	report("release: " + strings.Join(argv, " "))
	out, err := run(repoRoot, argv[0], argv[1:]...)
	if out != "" {
		report(strings.Split(out, "\n")...)
	}
	if err != nil {
		return false, err.Error()
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
