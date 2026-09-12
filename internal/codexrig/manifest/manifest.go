// Package manifest models codexrig-manifest.json, the one file in the synced
// repo that describes where the snapshot came from.
//
// It is deliberately much smaller than clauderig's. clauderig's manifest exists
// mainly to translate Claude Code's project SLUGS — a lossy flattening of the
// working directory into a folder name — so a restore can file a transcript
// under the right directory on a machine whose paths differ. Codex has no slug:
// rollouts are sharded by date, and the working directory is recorded INSIDE the
// rollout. There is nothing to rename on the way out.
//
// So this carries only what a reader cannot derive: which machine and Codex
// version produced the snapshot, and a portable spelling of each working
// directory seen in it, so `codexrig recent` on another machine can show a path
// that means something locally. Nothing here is used to rewrite a rollout —
// see the package comment on engine for why rollout bytes are never edited.
package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// FileName is the manifest's name in the synced repo.
const FileName = "codexrig-manifest.json"

const schemaVersion = 1

// Manifest describes one snapshot's origin.
type Manifest struct {
	Schema       int    `json:"schema"`
	CodexVersion string `json:"codexVersion,omitempty"`
	SourceOS     string `json:"sourceOS"`
	// Cwds maps a working directory as the source machine spelled it to its
	// portable template ("$HOME/Git/thing"), so another machine can resolve it
	// for display. Absent when a path could not be portablized, which is the
	// honest answer rather than a guess.
	Cwds map[string]string `json:"cwds,omitempty"`
}

// New builds an empty manifest for a source machine.
func New(codexVersion, sourceOS string) *Manifest {
	return &Manifest{Schema: schemaVersion, CodexVersion: codexVersion, SourceOS: sourceOS, Cwds: map[string]string{}}
}

// Note records a working directory and its portable spelling.
func (m *Manifest) Note(cwd, template string) {
	if cwd == "" || template == "" {
		return
	}
	if m.Cwds == nil {
		m.Cwds = map[string]string{}
	}
	m.Cwds[cwd] = template
}

// Load reads the manifest from a repo directory. An absent manifest is an error:
// every caller of this either needs the snapshot's origin or should not be
// restoring at all.
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

// Save writes the manifest into a repo directory.
func (m *Manifest) Save(dir string) error {
	m.Schema = schemaVersion
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), append(b, '\n'), 0o644)
}

// MergeFrom folds another machine's manifest in, without letting it overwrite
// what this machine knows. Every machine's syncs land in one repo, so a
// machine-local spelling has to be preserved rather than replaced by whichever
// machine synced last.
func (m *Manifest) MergeFrom(other *Manifest) {
	if other == nil {
		return
	}
	if m.Cwds == nil {
		m.Cwds = map[string]string{}
	}
	for cwd, tmpl := range other.Cwds {
		if _, mine := m.Cwds[cwd]; !mine {
			m.Cwds[cwd] = tmpl
		}
	}
}

// Templates returns the portable spellings, sorted, for deterministic output.
func (m *Manifest) Templates() []string {
	out := make([]string, 0, len(m.Cwds))
	for _, t := range m.Cwds {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
