// Package versionstate reads and writes .changeset/versions.json — the current
// version of each package whose manifest is not where its version lives.
//
// Two kinds of package need it. One computes its version at build time (MinVer
// reading git tags, a CI-stamped build) and carries no number in the tree at
// all. The other has a number in the tree that a release deliberately does not
// touch — a member of a stackspace, whose manifest belongs to its upstream, or
// any run with stamping switched off. Either way the release still computes a
// version, and it has to live somewhere the next plan can bump from and a
// resumed pipeline can read back; this file, beside the changesets, is that
// somewhere. It is written by `version` and read by discovery.
package versionstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// FileName is the state file's name inside the changeset directory.
const FileName = "versions.json"

// State is the persisted map of package name to version.
type State struct {
	// Packages maps a package's name (the one changesets use) to the version
	// the last release computed for it.
	Packages map[string]string `json:"packages"`
}

// Read returns the recorded versions; an absent file is an empty state.
func Read(changesetDir string) (*State, error) {
	s := &State{Packages: map[string]string{}}
	data, err := os.ReadFile(filepath.Join(changesetDir, FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.Packages == nil {
		s.Packages = map[string]string{}
	}
	return s, nil
}

// Write persists the state (indented, keys sorted, trailing newline — the
// shape of the other files beside it).
func Write(changesetDir string, s *State) error {
	if s == nil {
		s = &State{}
	}
	if s.Packages == nil {
		s.Packages = map[string]string{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(changesetDir, FileName), append(data, '\n'), 0o644)
}

// Get returns the recorded version for name, or "".
func (s *State) Get(name string) string {
	if s == nil {
		return ""
	}
	return s.Packages[name]
}

// Set records version for name.
func (s *State) Set(name, version string) {
	if s.Packages == nil {
		s.Packages = map[string]string{}
	}
	s.Packages[name] = version
}

// Names lists the recorded packages, sorted.
func (s *State) Names() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.Packages))
	for n := range s.Packages {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
