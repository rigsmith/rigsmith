// Package stackspace finds a rig stackspace's manifest and answers the one
// question the release tools have about it: which paths belong to a member.
//
// A stackspace fuses upstream repos into one history, each under a prefix, and
// everything under a prefix leaves again in a pull request to that member's
// upstream. So a file there is not this repository's to write: a version
// stamped into a member's manifest would show up in the next `rig stack
// propose` as a bump the upstream maintainer never asked for. The tools that
// write versions ask here first.
package stackspace

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/jsonc"
)

// FileBase is the manifest's base name at the stackspace root, tried as
// rig.stack.jsonc and rig.stack.json; a `stack` key in .rig.json is the other
// place it may live. The same locations `rig stack` reads.
const FileBase = "rig.stack"

// Stackspace is what the manifest says about the members.
type Stackspace struct {
	// Members are the prefixes the members live under, sorted.
	Members []string
	// Origin says where the manifest was found, for messages.
	Origin string
}

// manifest is the part of the stack manifest this package reads: the member
// prefixes are the keys of `repos`, and nothing else matters here.
type manifest struct {
	Repos map[string]json.RawMessage `json:"repos"`
}

// Find reads the stack manifest at root. A nil result with a nil error means
// root is not a stackspace; a manifest that is there but cannot be read is an
// error, since guessing "not a stackspace" would put writes exactly where they
// must not go.
func Find(root string) (*Stackspace, error) {
	src, err := cfgfind.Find(cfgfind.Spec{
		Label:   "stack manifest",
		Probe:   []cfgfind.DirNames{{Dir: root, Names: []string{FileBase}}},
		RigPath: filepath.Join(root, ".rig.json"),
		RigKeys: []string{"stack"},
	})
	if err != nil || src == nil {
		return nil, err
	}
	var m manifest
	if err := jsonc.Unmarshal(src.Data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", src.Origin, err)
	}
	s := &Stackspace{Origin: src.Origin}
	for name := range m.Repos {
		if name = strings.Trim(filepath.ToSlash(name), "/"); name != "" {
			s.Members = append(s.Members, name)
		}
	}
	sort.Strings(s.Members)
	return s, nil
}

// MemberOf returns the member whose prefix holds rel — a repo-relative path,
// native or slash-separated — or "" when rel is the stackspace's own. The
// prefix directory itself counts as the member's.
func (s *Stackspace) MemberOf(rel string) string {
	if s == nil {
		return ""
	}
	rel = strings.Trim(filepath.ToSlash(filepath.Clean(rel)), "/")
	for _, m := range s.Members {
		if rel == m || strings.HasPrefix(rel, m+"/") {
			return m
		}
	}
	return ""
}

// Owns reports whether rel lies under a member prefix — that is, whether the
// path belongs to a member's upstream rather than to this repository.
func (s *Stackspace) Owns(rel string) bool { return s.MemberOf(rel) != "" }
