// Package manifest models clauderig-manifest.json — the small index sync writes
// and restore reads. It records, per project, the portable cwd template (so
// restore rewrites slugs without reopening any transcript), plus the producing
// Claude Code version and the source OS for skew warnings.
package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

// FileName is the manifest's name at the sync repo root.
const FileName = "clauderig-manifest.json"

// schemaVersion is bumped when the manifest layout changes incompatibly.
const schemaVersion = 1

// Manifest is the synced index.
type Manifest struct {
	Schema        int                `json:"schema"`
	ClaudeVersion string             `json:"claudeVersion,omitempty"`
	SourceOS      string             `json:"sourceOS"`
	Projects      map[string]Project `json:"projects"`
}

// Project is one ~/.claude/projects/<slug> entry. Slug is the source-machine
// directory name; Template is its portable cwd ($HOME/Git/x) — empty when the cwd
// couldn't be portablized (not under a known folder), in which case Cwd holds the
// original absolute path and the slug is carried across unchanged on restore.
type Project struct {
	Template string `json:"template,omitempty"`
	Cwd      string `json:"cwd"`
}

// Build scans a ~/.claude/projects directory and records each project's portable
// cwd template, reading the cwd once per slug via a bounded header scan. srcOS and
// srcFolders describe the machine the scan runs on; claudeVersion is stamped for
// skew warnings (pass "" if unknown). Project dirs with no readable cwd (empty or
// cleaned) are skipped.
func Build(projectsDir, claudeVersion, srcOS string, srcFolders map[string]string) (*Manifest, error) {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}
	m := &Manifest{
		Schema:        schemaVersion,
		ClaudeVersion: claudeVersion,
		SourceOS:      srcOS,
		Projects:      map[string]Project{},
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		cwd, ok, err := project.CwdFromProjectDir(filepath.Join(projectsDir, slug))
		if err != nil || !ok {
			continue // unreadable / empty project dir — nothing to translate
		}
		tmpl, _ := pathmap.Portablize(cwd, srcFolders, srcOS)
		m.Projects[slug] = Project{Template: tmpl, Cwd: cwd}
	}
	return m, nil
}

// Save writes the manifest as pretty JSON to dir/FileName.
func (m *Manifest) Save(dir string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(filepath.Join(dir, FileName), b, 0o644)
}

// Load reads the manifest from dir/FileName.
func Load(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Slugs returns the project slugs in sorted order (deterministic iteration).
func (m *Manifest) Slugs() []string {
	out := make([]string, 0, len(m.Projects))
	for s := range m.Projects {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
