// Package cfgfind resolves a tool's config from one of several allowed
// locations — dedicated files (each tried as .jsonc and .json) plus an embedded
// key in the repo's .rig.json. It deliberately refuses to guess: zero sources
// means "use defaults", exactly one is used, and more than one is an error that
// names every candidate, so a misconfiguration is loud rather than silent.
package cfgfind

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

// DirNames is a directory and the base names (no extension) to probe in it.
// Each name is tried as "<name>.jsonc" and "<name>.json".
type DirNames struct {
	Dir   string
	Names []string
}

// KeyedProbe is a set of files whose named key may carry the config — the same
// idea as RigKeys, but for arbitrary files (e.g. a `release` key inside the
// changeset config file, or a `changeset` key inside the release file). Each
// base name is tried as "<name>.jsonc" and "<name>.json"; each present, non-null
// key is a candidate.
type KeyedProbe struct {
	Dir   string
	Names []string
	Keys  []string
}

// Spec describes where a tool's config may live, in no particular precedence —
// at most one may exist.
type Spec struct {
	Label    string       // for messages, e.g. "release config"
	Probe    []DirNames   // dedicated config files to look for
	Keyed    []KeyedProbe // files whose named key may carry the config
	RigPath  string       // path to the repo .rig.json (or "" to skip)
	RigKeys  []string     // keys in .rig.json that may carry the inline config
	FlagHint string       // a CLI flag that forces an explicit file (e.g. "--config"); "" omits the hint
}

// Source is the single resolved config.
type Source struct {
	Data    []byte // raw config bytes — file contents, or the keyed value
	Path    string // the dedicated file's path, or "" when the config is an embedded key
	File    string // the underlying file (== Path for a whole file; the containing file for a keyed source)
	BaseDir string // directory for resolving relative refs inside the config
	Origin  string // human-readable location, for messages
}

// candidate is one discovered source before the count is known.
type candidate struct {
	source Source
}

// Find returns the single config source, nil if none exists (caller uses
// defaults), or an error naming every candidate when more than one is found.
func Find(spec Spec) (*Source, error) {
	var found []candidate

	for _, dn := range spec.Probe {
		for _, name := range dn.Names {
			for _, ext := range []string{".jsonc", ".json"} {
				p := filepath.Join(dn.Dir, name+ext)
				data, err := os.ReadFile(p)
				if errors.Is(err, fs.ErrNotExist) {
					continue // simply not present
				}
				if err != nil {
					return nil, fmt.Errorf("reading %s: %w", p, err) // exists but unreadable — be loud
				}
				found = append(found, candidate{Source{Data: data, Path: p, File: p, BaseDir: dn.Dir, Origin: p}})
			}
		}
	}

	// Keyed files: a named key inside an arbitrary config file (e.g. a `release`
	// key in config.json, or a `changeset` key in shiprig.jsonc).
	for _, kp := range spec.Keyed {
		for _, name := range kp.Names {
			for _, ext := range []string{".jsonc", ".json"} {
				p := filepath.Join(kp.Dir, name+ext)
				cands, err := keysFrom(p, kp.Dir, kp.Keys)
				if err != nil {
					return nil, err
				}
				found = append(found, cands...)
			}
		}
	}

	if spec.RigPath != "" && len(spec.RigKeys) > 0 {
		cands, err := keysFrom(spec.RigPath, filepath.Dir(spec.RigPath), spec.RigKeys)
		if err != nil {
			return nil, err
		}
		found = append(found, cands...)
	}

	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		s := found[0].source
		return &s, nil
	default:
		return nil, ambiguous(spec.Label, spec.FlagHint, found)
	}
}

// keysFrom reads the JSONC object at path and returns one candidate per present,
// non-null key in keys (in order). A missing file yields nothing; an unreadable
// or unparseable one is a loud error. The candidate's Path is left empty (the
// config is an embedded key, not a standalone file), and BaseDir is the caller's
// dir for resolving relative refs.
func keysFrom(path, baseDir string, keys []string) ([]candidate, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil // simply not present
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var keyed map[string]json.RawMessage
	if err := jsonc.Unmarshal(data, &keyed); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var out []candidate
	for _, key := range keys {
		raw, ok := keyed[key]
		if !ok || isJSONNull(raw) {
			continue
		}
		out = append(out, candidate{Source{
			Data:    raw,
			File:    path,
			BaseDir: baseDir,
			Origin:  path + " (\"" + key + "\" key)",
		}})
	}
	return out, nil
}

func ambiguous(label, flagHint string, found []candidate) error {
	origins := make([]string, len(found))
	for i, c := range found {
		origins[i] = c.source.Origin
	}
	sort.Strings(origins)
	if label == "" {
		label = "config"
	}
	hint := "keep exactly one"
	if flagHint != "" {
		hint += " (or pass " + flagHint + ")"
	}
	return fmt.Errorf(
		"multiple %s sources found — %s:\n  - %s",
		label, hint, strings.Join(origins, "\n  - "))
}

// isJSONNull reports whether raw is the JSON literal null (or empty).
func isJSONNull(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}
