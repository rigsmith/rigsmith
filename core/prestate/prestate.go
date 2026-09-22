// Package prestate reads and writes the prerelease state: .changeset/pre.json
// plus the .changeset/pre/ directory of changesets a prerelease has consumed.
// The layout mirrors @changesets v3 so both tools share it.
package prestate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const fileName = "pre.json"

// DirName is the directory, under .changeset/, that holds the changesets a
// prerelease `version` run has consumed. They wait there until `pre exit`
// graduates them into one stable release.
const DirName = "pre"

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
	// Changesets is the @changesets v2 record of consumed ids, whose files
	// stayed at the top level of .changeset/. It is read so a prerelease begun
	// under v2 carries on without consuming them twice, and never written:
	// MoveToPre moves those files into pre/ and clears it.
	Changesets []string `json:"changesets,omitempty"`
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

// Dir returns the consumed-changesets directory for changesetDir.
func Dir(changesetDir string) string {
	return filepath.Join(changesetDir, DirName)
}

// MoveToPre moves each id's changeset file from the top of changesetDir into
// pre/, plus any a v2 pre.json listed, and clears that list. An id with no
// file at the top level (already moved, or synthesized from a commit) is
// skipped. It is all or nothing: on error every file it moved is put back.
// On success the returned undo does the same, for a caller whose later write
// fails.
func (p *PreState) MoveToPre(changesetDir string, ids []string) (undo func(), err error) {
	legacy := p.Changesets
	var moved []string
	undo = func() {
		for _, id := range moved {
			_ = os.Rename(filepath.Join(Dir(changesetDir), id+".md"), filepath.Join(changesetDir, id+".md"))
		}
		p.Changesets = legacy
	}
	if err := os.MkdirAll(Dir(changesetDir), 0o755); err != nil {
		return nil, err
	}
	for _, id := range append(append([]string{}, legacy...), ids...) {
		err := os.Rename(filepath.Join(changesetDir, id+".md"), filepath.Join(Dir(changesetDir), id+".md"))
		switch {
		case err == nil:
			moved = append(moved, id)
		case !errors.Is(err, os.ErrNotExist):
			undo()
			return nil, err
		}
	}
	p.Changesets = nil
	return undo, nil
}

// ReturnToTop moves every changeset still in pre/ back to the top of
// changesetDir. The run that exits prerelease mode calls it for the ones it
// did not consume (their packages are now ignored), so they stay visible to a
// later `version` once pre.json is gone.
func ReturnToTop(changesetDir string) error {
	entries, err := os.ReadDir(Dir(changesetDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Rename(filepath.Join(Dir(changesetDir), e.Name()), filepath.Join(changesetDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// RemoveDir deletes the pre/ directory once it is empty. Anything left in it
// (a changeset naming only ignored packages) keeps it, as Node leaves those
// files too.
func RemoveDir(changesetDir string) {
	_ = os.Remove(Dir(changesetDir))
}

// Contains reports whether id is in the v2 consumed-changesets list.
func (p *PreState) Contains(id string) bool {
	for _, c := range p.Changesets {
		if c == id {
			return true
		}
	}
	return false
}
