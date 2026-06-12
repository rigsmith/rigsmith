// Package prestate reads and writes .changeset/pre.json — the prerelease state.
// The shape mirrors @changesets so the file is shared with the JS tool.
package prestate

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const fileName = "pre.json"

// Modes for PreState.Mode.
const (
	ModePre  = "pre"
	ModeExit = "exit"
)

// PreState is the prerelease state persisted in .changeset/pre.json.
type PreState struct {
	// Mode is "pre" while in prerelease mode, "exit" once `pre exit` has run.
	Mode string `json:"mode"`
	// Tag is the prerelease tag (e.g. "next", "rc") appended to versions.
	Tag string `json:"tag"`
	// InitialVersions is each package's version when pre mode was entered.
	InitialVersions map[string]string `json:"initialVersions"`
	// Changesets are the ids already consumed by a prerelease `version` run.
	Changesets []string `json:"changesets"`
}

// Read returns the pre-state, or nil when the file is absent.
func Read(changesetDir string) (*PreState, error) {
	data, err := os.ReadFile(filepath.Join(changesetDir, fileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ps PreState
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, err
	}
	return &ps, nil
}

// Write persists the pre-state (indented, trailing newline — matching the JS tool).
func Write(changesetDir string, ps *PreState) error {
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(changesetDir, fileName), append(data, '\n'), 0o644)
}

// Remove deletes the pre-state file if present.
func Remove(changesetDir string) error {
	err := os.Remove(filepath.Join(changesetDir, fileName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Has reports whether a pre-state file exists.
func Has(changesetDir string) bool {
	_, err := os.Stat(filepath.Join(changesetDir, fileName))
	return err == nil
}

// Contains reports whether id is in the consumed-changesets list.
func (p *PreState) Contains(id string) bool {
	for _, c := range p.Changesets {
		if c == id {
			return true
		}
	}
	return false
}
