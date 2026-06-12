// Package engine orchestrates the sync pipeline: walk each root's allowlist,
// redact secrets, copy into the staging repo, build the project manifest, and run
// the secret tripwire. Pure of git — the caller commits/pushes the staging dir
// via internal/gitrepo. This is where the standalone units (allowlist, redact,
// manifest) compose into the actual operation.
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/clauderig/internal/allowlist"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/manifest"
	"github.com/rigsmith/clauderig/internal/redact"
	"github.com/rigsmith/core/pathmap"
)

// RootResult summarises one root's contribution to a sync.
type RootResult struct {
	ID         string
	Files      int
	Redactions int
	Skipped    bool // root absent on this machine
}

// Report is the outcome of a sync into the staging dir.
type Report struct {
	Roots            []RootResult
	ManifestProjects int
	Findings         []redact.Finding // non-empty ⇒ Sync returned an error (tripwire)
}

// Options configure a sync.
type Options struct {
	StagingDir    string
	Config        *config.Config
	Machine       config.Machine
	ClaudeVersion string
}

// Sync materialises the allowlisted, redacted file set for each enabled root into
// StagingDir/<root-id>/…, writes the project manifest, and runs the tripwire over
// the config JSON it wrote. A tripwire hit fails the sync loudly (a secret slipped
// past redaction) — that is the safety property; nothing is pushed in that case.
func Sync(opts Options) (*Report, error) {
	rep := &Report{}
	policy := redact.DefaultPolicy()

	for _, r := range opts.Config.Roots {
		if !r.Enabled {
			continue
		}
		rr := RootResult{ID: r.ID}
		loc, st := opts.Config.RootLocation(r.ID, opts.Machine)
		if st != pathmap.StatusResolved || !dirExists(loc) {
			rr.Skipped = true
			rep.Roots = append(rep.Roots, rr)
			continue
		}

		files, err := allowlist.Walk(loc, allowlistFor(r.ID))
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", r.ID, err)
		}
		stageRoot := filepath.Join(opts.StagingDir, r.ID)

		for _, rel := range files {
			data, err := os.ReadFile(filepath.Join(loc, filepath.FromSlash(rel)))
			if err != nil {
				return nil, fmt.Errorf("read %s/%s: %w", r.ID, rel, err)
			}
			out := data
			if isRedactable(rel) {
				if red, paths, rerr := redact.RedactBytes(data, policy); rerr == nil {
					out, rr.Redactions = red, rr.Redactions+len(paths)
				}
			}
			if err := writeFile(filepath.Join(stageRoot, filepath.FromSlash(rel)), out); err != nil {
				return nil, err
			}
			if strings.HasSuffix(rel, ".json") {
				if found, _ := redact.ScanBytes(out); len(found) > 0 {
					for _, f := range found {
						rep.Findings = append(rep.Findings, redact.Finding{
							Path: r.ID + "/" + rel + ":" + f.Path, Kind: f.Kind,
						})
					}
				}
			}
			rr.Files++
		}
		rep.Roots = append(rep.Roots, rr)
	}

	// Build the project manifest from the CLI root's projects dir.
	if cliLoc, st := opts.Config.RootLocation("cli", opts.Machine); st == pathmap.StatusResolved {
		projects := filepath.Join(cliLoc, "projects")
		if dirExists(projects) {
			m, err := manifest.Build(projects, opts.ClaudeVersion, opts.Machine.OS, opts.Machine.Folders())
			if err != nil {
				return nil, fmt.Errorf("manifest: %w", err)
			}
			if err := m.Save(opts.StagingDir); err != nil {
				return nil, err
			}
			rep.ManifestProjects = len(m.Projects)
		}
	}

	if len(rep.Findings) > 0 {
		return rep, fmt.Errorf("secret tripwire: %d value(s) look like credentials and were not redacted; refusing to sync", len(rep.Findings))
	}
	return rep, nil
}

func allowlistFor(rootID string) allowlist.List {
	if rootID == "desktop" {
		return allowlist.Desktop()
	}
	return allowlist.CLI()
}

// isRedactable marks the top-level config JSON files (the ones that can carry
// inline secrets). Deeper JSON is still tripwire-scanned but not field-redacted.
func isRedactable(rel string) bool {
	return !strings.Contains(rel, "/") && strings.HasSuffix(rel, ".json")
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
