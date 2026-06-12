// Package engine orchestrates the sync pipeline: walk each root's allowlist,
// redact secrets, copy into the staging repo, build the project manifest, and run
// the secret tripwire. Pure of git — the caller commits/pushes the staging dir
// via internal/gitrepo. This is where the standalone units (allowlist, redact,
// manifest) compose into the actual operation.
package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/clauderig/internal/allowlist"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/manifest"
	"github.com/rigsmith/clauderig/internal/redact"
	"github.com/rigsmith/core/pathmap"
)

// RootResult summarises one root's contribution to a sync.
type RootResult struct {
	ID             string
	Files          int // files written this sync (new or changed)
	Unchanged      int // files already current in staging (incremental skip)
	Redactions     int
	RetentionByAge int  // project transcripts dropped as older than the window
	SkippedFiles   int  // files that vanished/were unreadable mid-sync (live churn)
	Skipped        bool // root absent on this machine
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
	// RetentionDays drops project transcripts older than this many days (0 = keep
	// all). Now() is the reference; the cutoff is computed once per sync.
	RetentionDays int
	// SourceOverride maps a root id to an absolute source dir, used verbatim
	// instead of resolving the root location via the machine. The machine still
	// drives path translation (portablize/manifest); this only decouples WHERE the
	// files are read from — symmetric with restore's TargetOverride.
	SourceOverride map[string]string
}

// Sync materialises the allowlisted, redacted file set for each enabled root into
// StagingDir/<root-id>/…, writes the project manifest, and runs the tripwire over
// the config JSON it wrote. A tripwire hit fails the sync loudly (a secret slipped
// past redaction) — that is the safety property; nothing is pushed in that case.
func Sync(opts Options) (*Report, error) {
	rep := &Report{}
	policy := redact.DefaultPolicy()

	var cutoff time.Time
	if opts.RetentionDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -opts.RetentionDays)
	}

	for _, r := range opts.Config.Roots {
		if !r.Enabled {
			continue
		}
		rr := RootResult{ID: r.ID}
		loc, st := sourceLoc(opts, r.ID)
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
			srcPath := filepath.Join(loc, filepath.FromSlash(rel))
			dstPath := filepath.Join(stageRoot, filepath.FromSlash(rel))
			isJSON := strings.HasSuffix(rel, ".json")

			info, err := os.Stat(srcPath)
			if err != nil {
				// The live ~/.claude churns under us; a file that vanished mid-sync
				// must not abort the whole sync — skip it.
				rr.SkippedFiles++
				continue
			}

			// Retention: drop project transcripts older than the window.
			if !cutoff.IsZero() && strings.HasPrefix(rel, "projects/") && info.ModTime().Before(cutoff) {
				rr.RetentionByAge++
				continue
			}

			// Non-JSON (transcripts, skill files): copy verbatim, but skip if the
			// staging copy is already current (same size+mtime) — incremental sync.
			if !isJSON {
				if d, derr := os.Stat(dstPath); derr == nil && d.Size() == info.Size() && d.ModTime().Equal(info.ModTime()) {
					rr.Unchanged++
					continue
				}
				if err := copyPreserveMtime(srcPath, dstPath, info.ModTime()); err != nil {
					if os.IsNotExist(err) {
						rr.SkippedFiles++
						continue
					}
					return nil, err
				}
				rr.Files++
				continue
			}

			// JSON: redact secret-bearing fields (nested MCP/oauth configs carry real
			// tokens), portablize path values, scan — regenerated each sync (small).
			data, err := os.ReadFile(srcPath)
			if err != nil {
				rr.SkippedFiles++
				continue
			}
			out := data
			var v any
			if json.Unmarshal(data, &v) == nil {
				v = applyKeepFilter(r.ID, rel, v)
				red, paths := redact.Redact(v, policy)
				v, rr.Redactions = red, rr.Redactions+len(paths)
				v, _ = pathmap.PortablizeJSONValues(v, opts.Machine.Folders(), opts.Machine.OS)
				if b, e := json.MarshalIndent(v, "", "  "); e == nil {
					out = append(b, '\n')
				}
				for _, f := range redact.Scan(v) {
					rep.Findings = append(rep.Findings, redact.Finding{
						Path: r.ID + "/" + rel + ":" + f.Path, Kind: f.Kind,
					})
				}
			}
			if err := writeFile(dstPath, out); err != nil {
				return nil, err
			}
			rr.Files++
		}
		rep.Roots = append(rep.Roots, rr)
	}

	// Build the project manifest from the CLI root's projects dir.
	if cliLoc, st := sourceLoc(opts, "cli"); st == pathmap.StatusResolved {
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

// sourceLoc resolves where a root's files are read from: the explicit override if
// given (verbatim), else the machine-resolved root location.
func sourceLoc(opts Options, rootID string) (string, pathmap.Status) {
	if loc, ok := opts.SourceOverride[rootID]; ok {
		return loc, pathmap.StatusResolved
	}
	return opts.Config.RootLocation(rootID, opts.Machine)
}

// keepOnly returns the top-level keys to retain for a file that's mostly volatile,
// or nil to keep the whole document. The Desktop config.json is rewritten
// constantly with rotating caches/tokens; only its `preferences` are stable and
// worth syncing.
func keepOnly(rootID, rel string) []string {
	if rootID == "desktop" && rel == "config.json" {
		return []string{"preferences"}
	}
	return nil
}

// applyKeepFilter prunes a parsed JSON object to keepOnly's allowed top-level keys.
func applyKeepFilter(rootID, rel string, v any) any {
	keep := keepOnly(rootID, rel)
	if keep == nil {
		return v
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(keep))
	for _, k := range keep {
		if val, present := m[k]; present {
			out[k] = val
		}
	}
	return out
}

func allowlistFor(rootID string) allowlist.List {
	if rootID == "desktop" {
		return allowlist.Desktop()
	}
	return allowlist.CLI()
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

// copyPreserveMtime streams src to dst and stamps dst with src's mtime, so the
// next sync's size+mtime check can skip an unchanged file (incremental sync).
func copyPreserveMtime(src, dst string, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, mtime, mtime)
}
