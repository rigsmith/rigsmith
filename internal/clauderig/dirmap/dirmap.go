// Package dirmap binds a directory to the accounts that should be used in it.
//
// One table serves both surfaces — the Claude Code CLI login (`clauderig
// account`) and the Claude Desktop profile (`clauderig desktop`) — because they
// answer the same question about the same directory: "which of my identities is
// this work under?" A repo mapped to the work account almost always wants the
// work Desktop window too, and keeping one file means `map` in either command
// shows you the whole picture rather than half of it.
//
// The two fields stay independent: mapping one never invents the other, since a
// machine may track CLI accounts and no Desktop profiles, or the reverse.
//
// Mappings are per-machine and deliberately NOT synced. They name absolute paths
// that mean nothing on another machine, and they live under ~/.clauderig, which
// is outside every sync root.
package dirmap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one directory's bindings. Either field may be empty: a directory can
// name a CLI account, a Desktop profile, or both.
type Entry struct {
	Dir     string `json:"dir"`
	Account string `json:"account,omitempty"` // clauderig account id
	Desktop string `json:"desktop,omitempty"` // clauderig desktop profile name
}

// Empty reports whether an entry still binds anything. An entry that binds
// nothing is dropped rather than stored.
func (e Entry) Empty() bool { return e.Account == "" && e.Desktop == "" }

// file is the on-disk document.
type file struct {
	Mappings []Entry `json:"mappings"`
}

// Store is the mapping table at Path.
type Store struct{ Path string }

// New roots the table at path (…/.clauderig/dir-map.json).
func New(path string) *Store { return &Store{Path: path} }

// ErrNoMapping means no mapping covers the directory.
var ErrNoMapping = errors.New("no account mapped for this directory")

// List returns every mapping, ordered by directory.
func (s *Store) List() ([]Entry, error) {
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if uerr := json.Unmarshal(raw, &f); uerr != nil {
		return nil, fmt.Errorf("parse %s: %w", s.Path, uerr)
	}
	sort.Slice(f.Mappings, func(i, j int) bool { return f.Mappings[i].Dir < f.Mappings[j].Dir })
	return f.Mappings, nil
}

// Lookup finds the mapping governing dir: an exact match, else the NEAREST
// mapped ancestor, so a mapping on a repo root covers everything beneath it and
// a mapping deeper inside still wins over it.
func (s *Store) Lookup(dir string) (Entry, error) {
	all, err := s.List()
	if err != nil {
		return Entry{}, err
	}
	target := normalize(dir)
	var best Entry
	bestLen := -1
	for _, e := range all {
		md := normalize(e.Dir)
		if !covers(md, target) {
			continue
		}
		// Longest match wins — that is what "nearest ancestor" means once
		// several ancestors are mapped.
		if len(md) > bestLen {
			best, bestLen = e, len(md)
		}
	}
	if bestLen < 0 {
		return Entry{}, fmt.Errorf("%w: %s", ErrNoMapping, dir)
	}
	return best, nil
}

// covers reports whether ancestor is dir or a parent of it. The separator check
// is what stops /a/foo from matching /a/foobar.
func covers(ancestor, dir string) bool {
	if ancestor == dir {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(ancestor, sep) {
		ancestor += sep
	}
	return strings.HasPrefix(dir, ancestor)
}

// normalize makes a directory comparable: absolute, cleaned, and symlink-resolved
// so the same directory reached by two names maps once.
//
// Resolution walks up to the nearest ancestor that EXISTS and re-appends the
// rest. EvalSymlinks fails outright on a path whose leaf is missing, and taking
// that as "leave it alone" would compare a raw path against stored paths that
// were resolved — on macOS, where /var is itself a symlink, a lookup for a
// not-yet-created subdirectory would then match none of its mapped ancestors.
func normalize(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	dir = filepath.Clean(dir)
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	var missing []string
	cur := dir
	for {
		parent := filepath.Dir(cur)
		if parent == cur { // reached the root without finding anything real
			return dir
		}
		missing = append([]string{filepath.Base(cur)}, missing...)
		if r, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{r}, missing...)...)
		}
		cur = parent
	}
}

// Set binds dir, applying mutate to its entry (creating one if needed). An entry
// left binding nothing is removed rather than stored empty.
func (s *Store) Set(dir string, mutate func(*Entry)) (Entry, error) {
	all, err := s.List()
	if err != nil {
		return Entry{}, err
	}
	nd := normalize(dir)
	idx := -1
	for i, e := range all {
		if normalize(e.Dir) == nd {
			idx = i
			break
		}
	}
	if idx < 0 {
		all = append(all, Entry{Dir: nd})
		idx = len(all) - 1
	}
	mutate(&all[idx])
	all[idx].Dir = nd
	if all[idx].Empty() {
		entry := all[idx]
		all = append(all[:idx], all[idx+1:]...)
		return entry, s.write(all)
	}
	return all[idx], s.write(all)
}

// Remove drops the mapping for exactly this directory (not its ancestors).
func (s *Store) Remove(dir string) error {
	all, err := s.List()
	if err != nil {
		return err
	}
	nd := normalize(dir)
	for i, e := range all {
		if normalize(e.Dir) == nd {
			return s.write(append(all[:i], all[i+1:]...))
		}
	}
	return fmt.Errorf("%w: %s", ErrNoMapping, dir)
}

// PruneAccount drops every binding to a removed CLI account, and PruneDesktop
// the same for a removed Desktop profile. An entry left binding nothing goes
// with it — a mapping to something that no longer exists would silently do
// nothing at the moment it was most expected to work.
func (s *Store) PruneAccount(id string) error {
	return s.prune(func(e *Entry) {
		if e.Account == id {
			e.Account = ""
		}
	})
}

// PruneDesktop drops every binding to a removed Desktop profile.
func (s *Store) PruneDesktop(name string) error {
	return s.prune(func(e *Entry) {
		if e.Desktop == name {
			e.Desktop = ""
		}
	})
}

func (s *Store) prune(clear func(*Entry)) error {
	all, err := s.List()
	if err != nil {
		return err
	}
	kept := all[:0]
	for _, e := range all {
		clear(&e)
		if !e.Empty() {
			kept = append(kept, e)
		}
	}
	return s.write(kept)
}

func (s *Store) write(all []Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	if len(all) == 0 {
		// Nothing left to record: remove the file rather than leave an empty
		// document behind, so `map` reports a clean slate.
		if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Dir < all[j].Dir })
	body, err := json.MarshalIndent(file{Mappings: all}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, append(body, '\n'), 0o600)
}
