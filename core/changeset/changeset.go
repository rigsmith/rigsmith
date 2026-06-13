// Package changeset models the on-disk changeset format — a markdown file with a
// YAML-ish frontmatter block naming packages and their bump, followed by a
// summary. It is the shared @changesets format, so files written here are
// readable by the JS @changesets tool and vice versa.
//
// Ported from net-changesets' Shared/ChangesetsRepository.cs and ChangesetFile.cs.
package changeset

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Bump is the version bump a changeset requests for a package.
type Bump int

const (
	BumpNone Bump = iota
	BumpPatch
	BumpMinor
	BumpMajor
)

// ParseBump parses the lowercase changeset spelling (major/minor/patch/none).
// "auto" means "no explicit bump — derive it from the changeset's conventional
// type", and parses to BumpNone (the planner derives the effective bump).
func ParseBump(s string) (Bump, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "major":
		return BumpMajor, true
	case "minor":
		return BumpMinor, true
	case "patch":
		return BumpPatch, true
	case "none", "auto":
		return BumpNone, true
	default:
		return BumpNone, false
	}
}

// String returns the lowercase changeset spelling.
func (b Bump) String() string {
	switch b {
	case BumpMajor:
		return "major"
	case BumpMinor:
		return "minor"
	case BumpPatch:
		return "patch"
	default:
		return "none"
	}
}

// Max returns the higher-precedence of two bumps (major > minor > patch > none).
func (b Bump) Max(other Bump) Bump {
	if other > b {
		return other
	}
	return b
}

// Release is a single package named by a changeset, with its own bump — one
// frontmatter line.
type Release struct {
	Name string
	Bump Bump
}

// Changeset is a parsed changeset file.
type Changeset struct {
	// Releases are the packages named by the changeset, each with its own bump.
	Releases []Release
	// Summary is the changeset body (everything after the frontmatter).
	Summary string
	// ID is the file name without extension, used to track which changesets a
	// prerelease version run has consumed. Empty for in-memory changesets.
	ID string
	// Type is the conventional-commit type for the changeset (e.g. "feat",
	// "fix"), from an explicit `type:` frontmatter line or parsed from the
	// summary's conventional prefix. Empty when none is given.
	Type string
	// Breaking marks a breaking change (a `!` on the type, e.g. `feat!`). A
	// breaking changeset bumps major and renders under "Breaking Changes".
	Breaking bool
	// Commit is the source commit SHA for a changeset synthesized from a commit
	// (commit-based versioning). Empty for on-disk changeset files. When set, the
	// changelog generators decorate the release line straight from this commit —
	// the commit IS the provenance — instead of hunting for the commit that added
	// a changeset file.
	Commit string
}

// EffectiveType resolves the changeset's conventional type: the explicit
// frontmatter `type:` wins; otherwise it is parsed from the summary's
// conventional-commit prefix (`feat: …`, `fix!: …`). Returns the type, whether
// it is breaking, and ok=false when neither source yields a type.
func (c *Changeset) EffectiveType() (typ string, breaking bool, ok bool) {
	if c.Type != "" {
		return c.Type, c.Breaking, true
	}
	return ParseConventional(c.Summary)
}

// conventionalRe matches a conventional-commit prefix: type(scope)!: subject.
var conventionalRe = regexp.MustCompile(`^([a-zA-Z]+)(?:\([^)]*\))?(!)?:\s`)

// ParseConventional extracts the type and breaking flag from a conventional-
// commit-style first line.
func ParseConventional(summary string) (typ string, breaking bool, ok bool) {
	first := summary
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	m := conventionalRe.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return "", false, false
	}
	return strings.ToLower(m[1]), m[2] == "!", true
}

// ChangedNames returns the package names the changeset touches.
func (c *Changeset) ChangedNames() []string {
	names := make([]string, len(c.Releases))
	for i, r := range c.Releases {
		names[i] = r.Name
	}
	return names
}

// Frontmatter package line. The bump is OPTIONAL: `"Name": minor` is an explicit
// bump (override), while a bare `"Name"` (or `"Name":` with nothing after) means
// "derive the bump from the changeset's conventional type". This matches the
// shared @changesets `"Name": bump` shape while allowing type-driven changesets.
var moduleRe = regexp.MustCompile(`^\s*"([^"]+)"\s*(?::\s*([A-Za-z]+))?\s*$`)

// Parse parses changeset file content. The id (typically the filename without
// extension) is attached to the result.
func Parse(content, id string) (*Changeset, error) {
	// Normalize line endings so CRLF files parse identically.
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	cs := &Changeset{ID: id}

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("changeset %q: missing opening frontmatter delimiter", id)
	}

	i := 1
	// Empty changeset: closing '---' immediately follows the opening one.
	if i < len(lines) && lines[i] == "---" {
		cs.Summary = joinFrom(lines, i+2)
		return cs, nil
	}

	for i < len(lines) && lines[i] != "---" {
		line := lines[i]
		// Optional `type:` line (conventional-commit type, `!` => breaking).
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "type:") {
			val := strings.TrimSpace(strings.TrimPrefix(t, "type:"))
			val = strings.Trim(val, `"'`)
			cs.Breaking = strings.HasSuffix(val, "!")
			cs.Type = strings.ToLower(strings.TrimSuffix(val, "!"))
			i++
			continue
		}
		m := moduleRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("changeset %q: malformed frontmatter line %q", id, line)
		}
		// Missing bump (`"Name"` with no `: bump`) means BumpNone → derive from type.
		bump := BumpNone
		if m[2] != "" {
			b, ok := ParseBump(m[2])
			if !ok {
				return nil, fmt.Errorf("changeset %q: invalid bump type %q", id, m[2])
			}
			bump = b
		}
		cs.Releases = append(cs.Releases, Release{Name: m[1], Bump: bump})
		i++
	}
	if i >= len(lines) {
		return nil, fmt.Errorf("changeset %q: missing closing frontmatter delimiter", id)
	}

	cs.Summary = joinFrom(lines, i+2) // skip the closing '---' and the blank line after it
	return cs, nil
}

// joinFrom joins lines[start:] with '\n', trimming a single trailing newline's
// worth of empty tail to match the C# Skip semantics.
func joinFrom(lines []string, start int) string {
	if start >= len(lines) {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

// Render produces the canonical on-disk representation of a changeset. A
// conventional type (with the breaking flag) is written as a `type:` line; a
// release with BumpNone is written bare (no `: bump`), meaning "derive from type".
func Render(releases []Release, summary, typ string, breaking bool) string {
	var b strings.Builder
	b.WriteString("---\n")
	if typ != "" {
		if breaking {
			typ += "!"
		}
		fmt.Fprintf(&b, "type: %s\n", typ)
	}
	for _, r := range releases {
		if r.Bump == BumpNone {
			fmt.Fprintf(&b, "%q\n", r.Name)
		} else {
			fmt.Fprintf(&b, "%q: %s\n", r.Name, r.Bump.String())
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(summary)
	return b.String()
}

const readmeName = "README.md"

// Dir reads every changeset file in a directory. interopExt, when non-empty,
// additionally includes files with that extension (the interop mode where the
// JS tool owns .md and this tool owns e.g. ".net.mkd").
func Dir(changesetDir, interopExt string) ([]*Changeset, error) {
	entries, err := os.ReadDir(changesetDir)
	if err != nil {
		return nil, err
	}
	var out []*Changeset
	for _, e := range entries {
		if e.IsDir() || !isChangesetFile(e.Name(), interopExt) {
			continue
		}
		path := filepath.Join(changesetDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		id := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		cs, err := Parse(string(data), id)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, nil
}

func isChangesetFile(name, interopExt string) bool {
	if strings.EqualFold(name, readmeName) {
		return false
	}
	if interopExt != "" && strings.HasSuffix(strings.ToLower(name), strings.ToLower(interopExt)) {
		return true
	}
	return strings.HasSuffix(strings.ToLower(name), ".md")
}
