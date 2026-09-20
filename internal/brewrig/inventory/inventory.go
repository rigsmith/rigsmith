// Package inventory models what one machine has deliberately installed from
// Homebrew, and the file it publishes to the shared repo.
//
// A machine writes only its own file. The shared truth is the set of those
// files, never a single merged Brewfile — see docs/BREWRIG-DESIGN.md for why a
// merged file cannot distinguish "not installed here yet" from "deliberately
// removed here", which is the one distinction the whole tool turns on.
package inventory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the published file's format version. Bump it only for a
// change a reader of the old format could not survive; additive fields don't
// count, since encoding/json ignores what it doesn't know.
const SchemaVersion = 1

// Kind distinguishes the two namespaces Homebrew keeps. They are separate: a
// formula and a cask may share a name (`docker` does), so every reference
// carries its kind and no map is ever keyed by bare name.
type Kind string

const (
	Formula Kind = "formula"
	Cask    Kind = "cask"
)

// Ref identifies one package within its namespace.
type Ref struct {
	Kind Kind
	Name string
}

// String renders the stable "kind:name" key used for map keys and JSON object
// fields. It is parsed back by ParseRef, so the two must stay in step.
func (r Ref) String() string { return string(r.Kind) + ":" + r.Name }

// ParseRef reads back String. An unknown or missing kind is an error rather
// than a guess: silently treating a malformed key as a formula would make
// brewrig propose uninstalling the wrong thing.
func ParseRef(s string) (Ref, error) {
	k, name, ok := strings.Cut(s, ":")
	if !ok || name == "" {
		return Ref{}, fmt.Errorf("malformed package reference %q: want \"kind:name\"", s)
	}
	switch Kind(k) {
	case Formula, Cask:
		return Ref{Kind: Kind(k), Name: name}, nil
	default:
		return Ref{}, fmt.Errorf("unknown package kind %q in reference %q", k, s)
	}
}

// Label renders a Ref the way a person reads it in output: the bare name for a
// formula, and a marked one for a cask, because "install kitty" is ambiguous
// between the two namespaces and the cask is the surprising one.
func (r Ref) Label() string {
	if r.Kind == Cask {
		return r.Name + " (cask)"
	}
	return r.Name
}

// Package is one installed item as this machine sees it.
type Package struct {
	Name string `json:"name"`
	// Tap is the source tap when it is not Homebrew's default (homebrew/core
	// for formulae, homebrew/cask for casks). Recorded because installing a
	// third-party package on the other machine requires tapping first.
	Tap string `json:"tap,omitempty"`
	// Version is what is installed here. It drives skew reporting; it is
	// never used to pin an install, since brewrig converges machines onto
	// current versions rather than freezing them.
	Version string `json:"version,omitempty"`
	// InstalledAt is brew's own record of when this was installed, as a Unix
	// timestamp. It settles retire-vs-reinstall races: a reinstall later than
	// another machine's retire stamp wins.
	InstalledAt int64 `json:"installedAt,omitempty"`
}

// OptOut is the deliberate divergence list: packages this machine does not want
// even though another machine has them. Published rather than kept local so the
// other machine can see the decision instead of re-proposing it forever.
type OptOut struct {
	Formulae []string `json:"formulae,omitempty"`
	Casks    []string `json:"casks,omitempty"`
}

// Machine is one machine's published inventory — the whole contents of
// machines/<name>.json.
type Machine struct {
	Schema   int       `json:"schema"`
	Name     string    `json:"machine"`
	OS       string    `json:"os"`
	Arch     string    `json:"arch,omitempty"`
	SyncedAt time.Time `json:"syncedAt"`

	// BrewVersion and Prefix are diagnostic: an Intel Mac on /usr/local and an
	// Apple Silicon one on /opt/homebrew is the usual explanation for a cask
	// that refuses to install on one of them.
	BrewVersion string `json:"brewVersion,omitempty"`
	Prefix      string `json:"prefix,omitempty"`

	Taps     []string  `json:"taps,omitempty"`
	Formulae []Package `json:"formulae"`
	Casks    []Package `json:"casks"`

	OptOut OptOut `json:"optOut,omitzero"`

	// Retired records packages this machine published before and has since
	// uninstalled, with when the drop was first observed. It is the only
	// source of a proposed uninstall anywhere in brewrig.
	Retired map[string]time.Time `json:"retired,omitempty"`

	// Present is everything brew has installed here, including the dependency
	// closure — local only, never published (`json:"-"`), because publishing
	// the closure is exactly what the design rejects.
	//
	// It exists so a report can tell "you don't have this" apart from "you
	// have this, but as a dependency of something else". Installing the latter
	// is a near-instant no-op that just records you meant to have it, and
	// calling it "missing" reads as a much bigger change than it is.
	Present map[string]bool `json:"-"`
}

// PresentAsDependency reports whether the package is installed here but was
// never asked for, so it is absent from the published inventory.
func (m *Machine) PresentAsDependency(r Ref) bool {
	return m.Present[r.String()] && !m.Has(r)
}

// Has reports whether the machine currently has the package installed.
func (m *Machine) Has(r Ref) bool {
	_, ok := m.find(r)
	return ok
}

// Lookup returns the installed package for a ref.
func (m *Machine) Lookup(r Ref) (Package, bool) { return m.find(r) }

func (m *Machine) find(r Ref) (Package, bool) {
	list := m.Formulae
	if r.Kind == Cask {
		list = m.Casks
	}
	for _, p := range list {
		if p.Name == r.Name {
			return p, true
		}
	}
	return Package{}, false
}

// Installed lists every package the machine currently has, in a stable order.
func (m *Machine) Installed() []Ref {
	refs := make([]Ref, 0, len(m.Formulae)+len(m.Casks))
	for _, p := range m.Formulae {
		refs = append(refs, Ref{Formula, p.Name})
	}
	for _, p := range m.Casks {
		refs = append(refs, Ref{Cask, p.Name})
	}
	SortRefs(refs)
	return refs
}

// OptedOut reports whether this machine has declined the package.
func (m *Machine) OptedOut(r Ref) bool {
	list := m.OptOut.Formulae
	if r.Kind == Cask {
		list = m.OptOut.Casks
	}
	for _, n := range list {
		if n == r.Name {
			return true
		}
	}
	return false
}

// RetiredAt returns when this machine retired the package, if it did.
func (m *Machine) RetiredAt(r Ref) (time.Time, bool) {
	t, ok := m.Retired[r.String()]
	return t, ok
}

// Retire records a deliberate uninstall, keeping the earliest observation. The
// first sync that notices the drop is the honest timestamp; a later sync
// re-observing the same absence must not push the stamp forward, or a retire
// would keep overtaking an older reinstall on the other machine and the two
// would never settle.
func (m *Machine) Retire(r Ref, at time.Time) {
	if m.Retired == nil {
		m.Retired = map[string]time.Time{}
	}
	if prev, ok := m.Retired[r.String()]; ok && !prev.IsZero() && prev.Before(at) {
		return
	}
	m.Retired[r.String()] = at.UTC()
}

// Unretire drops the record, for when the package is installed here again.
func (m *Machine) Unretire(r Ref) { delete(m.Retired, r.String()) }

// Normalize sorts every list and fills defaults, so a sync that changed nothing
// produces a byte-identical file and therefore no commit. Without this the two
// machines would commit reordered noise at each other forever.
func (m *Machine) Normalize() {
	if m.Schema == 0 {
		m.Schema = SchemaVersion
	}
	sort.Strings(m.Taps)
	sort.Strings(m.OptOut.Formulae)
	sort.Strings(m.OptOut.Casks)
	sortPackages(m.Formulae)
	sortPackages(m.Casks)
	m.SyncedAt = m.SyncedAt.UTC().Truncate(time.Second)
	for k, v := range m.Retired {
		m.Retired[k] = v.UTC().Truncate(time.Second)
	}
	if len(m.Retired) == 0 {
		m.Retired = nil
	}
	if m.Formulae == nil {
		m.Formulae = []Package{}
	}
	if m.Casks == nil {
		m.Casks = []Package{}
	}
}

func sortPackages(ps []Package) {
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
}

// SortRefs orders refs for display: formulae then casks, alphabetical within
// each, so two runs over the same data read the same way.
func SortRefs(refs []Ref) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind == Formula
		}
		return refs[i].Name < refs[j].Name
	})
}

// Marshal renders the file exactly as it is committed: normalized, indented,
// newline-terminated. Everything that writes a machine file goes through here,
// so "no real change" reliably means "no diff".
func Marshal(m *Machine) ([]byte, error) {
	m.Normalize()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Unmarshal reads a published machine file.
func Unmarshal(b []byte) (*Machine, error) {
	var m Machine
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Schema > SchemaVersion {
		return nil, fmt.Errorf("machine file is schema %d, this brewrig understands %d — upgrade brewrig on this machine", m.Schema, SchemaVersion)
	}
	if m.Name == "" {
		return nil, fmt.Errorf("machine file has no machine name")
	}
	m.Normalize()
	return &m, nil
}

// FileName is the repo-relative path a machine publishes to.
func FileName(machine string) string { return "machines/" + machine + ".json" }
