// Package regex implements a generic version-stamping adapter for files that
// aren't a recognized package manifest. A repo declares them in
// .changeset/config.json under a "regex" block:
//
//	"regex": {
//	  "packages": [
//	    { "name": "chart", "file": "deploy/Chart.yaml",
//	      "pattern": "version: (?<version>.*)" }
//	  ]
//	}
//
// Each entry names a file and a regex with a named `version` capture group; the
// adapter reads the current version from that group and SetVersion rewrites just
// the captured text (format-preserving). This is the unified, adapter-contract
// home for net-changesets' `packages.versionRegex` — version read/write stays in
// one place (PLUGIN-PROTOCOL.md) rather than a second stamping mechanism in the
// release config. Like Go, regex packages have no registry push: they're
// released by the publish tagging phase.
package regex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// Adapter is the in-process regex version-file adapter.
type Adapter struct{}

// New returns a regex adapter.
func New() *Adapter { return &Adapter{} }

var _ plugin.Ecosystem = (*Adapter)(nil)

// regexConfig is the `.changeset/config.json` "regex" block.
type regexConfig struct {
	Packages []regexPackage `json:"packages"`
}

// regexPackage is one declared file to version by pattern.
type regexPackage struct {
	Name    string `json:"name"`    // changeset identity
	File    string `json:"file"`    // repo-relative path to the file holding the version
	Pattern string `json:"pattern"` // regex with a named `version` capture group
}

// namedGroupJS rewrites a JS/.NET-style named group `(?<name>` to Go's
// `(?P<name>`, so a pattern copied from the net-changesets / @changesets world
// compiles unchanged. Lookbehind `(?<=` / `(?<!` is left alone (RE2 rejects it).
var namedGroupJS = regexp.MustCompile(`\(\?<([A-Za-z_][A-Za-z0-9_]*)>`)

// Info returns the regex adapter's identity. It owns no dev commands (it isn't a
// language) and no manifest globs (its "manifests" are config-declared).
func (a *Adapter) Info() plugin.EcosystemInfo {
	return plugin.EcosystemInfo{
		APIVersion:   plugin.APIVersion,
		ID:           "regex",
		DisplayName:  "Regex",
		Capabilities: []string{plugin.MethodDiscover, plugin.MethodSetVersion, plugin.MethodPublish},
	}
}

// Detect reports whether the repo declares any regex packages.
func (a *Adapter) Detect(ctx context.Context, root string) (bool, error) {
	return len(loadRegexPackages(root)) > 0, nil
}

// Discover returns one Package per declared entry whose file exists and whose
// pattern matches (so each has a current version and a place to write). Entries
// that don't resolve are skipped — they can't be versioned.
func (a *Adapter) Discover(ctx context.Context, req plugin.DiscoverRequest) (plugin.DiscoverResponse, error) {
	var resp plugin.DiscoverResponse
	for _, p := range loadRegexPackages(req.RepoRoot) {
		if p.Name == "" || p.File == "" || p.Pattern == "" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(req.RepoRoot, p.File))
		if err != nil {
			continue
		}
		re, idx, err := compilePattern(p.Pattern)
		if err != nil || idx < 0 {
			continue
		}
		m := re.FindStringSubmatch(string(content))
		if m == nil {
			continue
		}
		resp.Packages = append(resp.Packages, plugin.Package{
			Name:         p.Name,
			Version:      strings.TrimSpace(m[idx]),
			Dir:          filepath.Dir(p.File),
			ManifestPath: p.File,
		})
	}
	return resp, nil
}

// SetVersion rewrites the matched `version` group in the package's file with the
// new version, preserving everything else byte-for-byte.
func (a *Adapter) SetVersion(ctx context.Context, req plugin.SetVersionRequest) error {
	var spec *regexPackage
	for _, p := range loadRegexPackages(req.RepoRoot) {
		if p.Name == req.Package.Name {
			p := p
			spec = &p
			break
		}
	}
	if spec == nil {
		return fmt.Errorf("regex: no package %q declared in config", req.Package.Name)
	}
	path := filepath.Join(req.RepoRoot, spec.File)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	re, idx, err := compilePattern(spec.Pattern)
	if err != nil {
		return fmt.Errorf("regex: pattern for %q: %w", spec.Name, err)
	}
	if idx < 0 {
		return fmt.Errorf("regex: pattern for %q has no (?<version>) capture group", spec.Name)
	}
	loc := re.FindStringSubmatchIndex(string(content))
	if loc == nil || loc[2*idx] < 0 {
		return fmt.Errorf("regex: pattern for %q did not match %s", spec.Name, spec.File)
	}
	start, end := loc[2*idx], loc[2*idx+1]
	updated := string(content[:start]) + req.NewVersion + string(content[end:])
	return os.WriteFile(path, []byte(updated), 0o644)
}

// Publish is a no-op at the registry level: a regex package is "published" by
// the publish tagging phase (name@version), exactly like a Go module.
func (a *Adapter) Publish(ctx context.Context, req plugin.PublishRequest) (plugin.PublishResponse, error) {
	return plugin.PublishResponse{Skipped: true, Message: "versioned in place; released via git tag"}, nil
}

// loadRegexPackages reads the "regex" block from the repo's .changeset config.
// Best-effort: a missing/empty/invalid block yields no packages.
func loadRegexPackages(root string) []regexPackage {
	cfg, err := config.Load(filepath.Join(root, ".changeset"))
	if err != nil {
		return nil
	}
	var rc regexConfig
	if _, err := cfg.Ecosystem("regex", &rc); err != nil {
		return nil
	}
	return rc.Packages
}

// compilePattern normalizes a JS/.NET named group to Go syntax, compiles it, and
// returns the submatch index of the `version` group (-1 when absent).
func compilePattern(pattern string) (*regexp.Regexp, int, error) {
	re, err := regexp.Compile(namedGroupJS.ReplaceAllString(pattern, `(?P<$1>`))
	if err != nil {
		return nil, -1, err
	}
	return re, re.SubexpIndex("version"), nil
}
