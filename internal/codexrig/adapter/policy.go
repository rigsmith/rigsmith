// Package adapter owns Codex source selection and native-format policy.
// Inventory candidates are not permission to copy or publish their contents.
package adapter

import (
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/agentrig/allowlist"
)

type RootKind string

const (
	CodexHome  RootKind = "codex-home"
	UserSkills RootKind = "user-skills"
)

// Candidate describes only a file's role and the work required before capture.
// No kind has a raw-copy fallback. Native sessions are deliberately not selected.
type Candidate struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Requires string `json:"requires"`
}

// Selection uses the same matching/pruning mechanics as Claude, with independent
// vendor rules. Classify is the final shape check: literal includes in the shared
// matcher also cover descendants, whereas config and instruction files are exact.
func Selection(root RootKind) allowlist.List {
	var rules []allowlist.Rule
	switch root {
	case CodexHome:
		for _, p := range []string{"config.toml", "*.config.toml", "AGENTS.md", "AGENTS.override.md", "hooks.json", "rules", "skills"} {
			rules = append(rules, allowlist.Rule{Pattern: p, Action: allowlist.Include})
		}
	case UserSkills:
		rules = append(rules, allowlist.Rule{Pattern: "*", Action: allowlist.Include})
	}
	for _, p := range []string{".*", "node_modules", "__pycache__", "auth.json", "credentials.json", "*.pem", "*.key", "*.sqlite*", "*.db*"} {
		rules = append(rules, allowlist.Rule{Pattern: "**/" + p, Action: allowlist.Exclude})
	}
	return allowlist.List{Rules: rules}
}

func Classify(root RootKind, rel string) (Candidate, bool) {
	if !validPath(rel) || deniedSegment(rel) || !Selection(root).Match(rel) {
		return Candidate{}, false
	}
	c := Candidate{Path: rel}
	if root == UserSkills && strings.Count(rel, "/") >= 1 ||
		(root == CodexHome && strings.HasPrefix(rel, "skills/") && strings.Count(rel, "/") >= 2) {
		c.Kind, c.Requires = "skill", "skill-content-policy"
		return c, true
	}
	if root != CodexHome {
		return Candidate{}, false
	}
	switch {
	case rel == "config.toml":
		c.Kind, c.Requires = "config", "structured-toml"
	case profileName(rel) != "":
		c.Kind, c.Requires = "config-profile", "structured-toml"
	case rel == "AGENTS.md" || rel == "AGENTS.override.md":
		c.Kind, c.Requires = "instructions", "text-scan-and-path-policy"
	case rel == "hooks.json":
		c.Kind, c.Requires = "hooks", "structured-hooks"
	case strings.HasPrefix(rel, "rules/") && strings.Count(rel, "/") == 1 && strings.HasSuffix(rel, ".rules"):
		c.Kind, c.Requires = "rules", "text-scan-and-path-policy"
	default:
		return Candidate{}, false
	}
	return c, true
}

func profileName(rel string) string {
	name, ok := strings.CutSuffix(rel, ".config.toml")
	if !ok || name == "" {
		return ""
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return ""
		}
	}
	return name
}

func validPath(rel string) bool {
	return rel != "." && fs.ValidPath(rel) && utf8.ValidString(rel) &&
		!strings.ContainsAny(rel, "\\:") && strings.IndexFunc(rel, unicode.IsControl) < 0
}

// Also apply exclusions case-insensitively at the final boundary, independent
// of the filesystem's case behavior and the matcher's case-sensitive patterns.
func deniedSegment(rel string) bool {
	for _, p := range strings.Split(strings.ToLower(rel), "/") {
		if strings.HasPrefix(p, ".") || p == "node_modules" || p == "__pycache__" || p == "auth.json" || p == "credentials.json" {
			return true
		}
		for _, pattern := range []string{"*.pem", "*.key", "*.sqlite*", "*.db*"} {
			if match, _ := path.Match(pattern, p); match {
				return true
			}
		}
	}
	return false
}
