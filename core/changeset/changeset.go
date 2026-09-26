// Package changeset models the on-disk changeset format — a markdown file with a
// YAML-ish frontmatter block naming packages and their bump, followed by a
// summary. It is the shared @changesets format, so files written here are
// readable by the JS @changesets tool and vice versa.
//
// Ported from net-changesets' Shared/ChangesetsRepository.cs and ChangesetFile.cs.
package changeset

import (
	"errors"
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
	// Scope names which part of a monorepo the change belongs to — the tool, in
	// this family: `feat(rig): …`. From an explicit `scope:` frontmatter line or
	// the summary's conventional prefix. Empty when none is given. It groups
	// bullets inside a section; the type still decides which section.
	Scope string
	// Commit is the source commit SHA for a changeset synthesized from a commit
	// (commit-based versioning). Empty for on-disk changeset files. When set, the
	// changelog generators decorate the release line straight from this commit —
	// the commit IS the provenance — instead of hunting for the commit that added
	// a changeset file.
	Commit string
	// Ref is the commit, pull request and author the changeset came from,
	// when the run resolved them for a changelog generator that renders
	// references. Zero otherwise.
	Ref Ref
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

// EffectiveScope resolves the change's scope — which tool it belongs to in a
// monorepo. The explicit frontmatter `scope:` wins; otherwise it comes from the
// summary's conventional-commit prefix. Empty when neither names one.
func (c *Changeset) EffectiveScope() string {
	if c.Scope != "" {
		return c.Scope
	}
	_, scope, _, _ := ParseConventionalScope(c.Summary)
	return scope
}

// conventionalRe matches a conventional-commit prefix: type(scope)!: subject.
// The subject may be empty — "fix(rig):" with nothing after it is still a
// prefix, and callers trim before matching, so requiring a trailing space would
// stop recognising exactly that case.
var conventionalRe = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([^)]*)\))?(!)?:(?:\s|$)`)

// ParseConventional extracts the type and breaking flag from a conventional-
// commit-style first line.
func ParseConventional(summary string) (typ string, breaking bool, ok bool) {
	typ, _, breaking, ok = ParseConventionalScope(summary)
	return typ, breaking, ok
}

// ParseConventionalScope is ParseConventional including the optional scope —
// the part in parentheses, which in a monorepo names the tool a change belongs
// to (`feat(rig): …`). Kept separate so the older two-result signature, which
// several callers use, does not change.
func ParseConventionalScope(summary string) (typ, scope string, breaking bool, ok bool) {
	m := conventionalRe.FindStringSubmatch(strings.TrimSpace(firstLine(summary)))
	if m == nil {
		return "", "", false, false
	}
	return strings.ToLower(m[1]), strings.TrimSpace(m[2]), m[3] == "!", true
}

// normalizeConventional lifts a conventional prefix out of the summary and into
// the Type/Scope fields, once, at parse time. Doing it here rather than at
// render time is what makes it survive changelog decoration: `version` prepends
// a commit or PR reference to the summary before anything renders, and a prefix
// is only recognisable while it is still at the start of the line.
func normalizeConventional(cs *Changeset) {
	typ, scope, breaking, ok := ParseConventionalScope(cs.Summary)
	if !ok {
		return
	}
	if cs.Type == "" {
		cs.Type, cs.Breaking = typ, breaking
	}
	if cs.Scope == "" {
		cs.Scope = scope
	}
	cs.Summary = StripConventional(cs.Summary)
}

// StripConventional removes a conventional-commit prefix from a summary, so a
// renderer can present the type and scope as structure rather than as leading
// punctuation in the sentence. Returns the summary unchanged when there is none.
func StripConventional(summary string) string {
	first := firstLine(summary)
	m := conventionalRe.FindStringIndex(strings.TrimSpace(first))
	if m == nil {
		return summary
	}
	trimmed := strings.TrimSpace(first)
	rest := trimmed[m[1]:]
	if i := strings.IndexByte(summary, '\n'); i >= 0 {
		return rest + summary[i:]
	}
	return rest
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ChangedNames returns the package names the changeset touches.
func (c *Changeset) ChangedNames() []string {
	names := make([]string, len(c.Releases))
	for i, r := range c.Releases {
		names[i] = r.Name
	}
	return names
}

// plainKeyRe is an unquoted package name as YAML reads a plain key: no
// leading indicator character (so `@scope/name` has to be quoted, as it does
// for @changesets' YAML parser).
var plainKeyRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// bumpWordRe is a bump value once any quotes are off.
var bumpWordRe = regexp.MustCompile(`^[A-Za-z]+$`)

// errMalformedLine is a frontmatter line that isn't a package line at all.
var errMalformedLine = errors.New("malformed")

// errNoBump is a package line with a colon and nothing after it (`lib:`).
// YAML reads that as `lib: null`, which @changesets refuses as a bump; left
// alone it would release nothing and strand the changeset.
var errNoBump = errors.New("no bump")

// parseReleaseLine reads a frontmatter package line the way @changesets' YAML
// parser does: the name double-quoted (`"lib"`), single-quoted (`'lib'`, with
// `”` for a quote) or plain (`lib`), then optionally `: bump`, the bump plain
// or quoted, then an optional `# comment`. The bump is OPTIONAL: `"Name":
// minor` is an explicit bump (override), while a bare `"Name"` (no colon)
// means "derive the bump from the changeset's conventional type". A colon
// with nothing after it (`"Name":`) is errNoBump, as @changesets refuses it;
// any other line that isn't a package line is errMalformedLine.
func parseReleaseLine(line string) (name, bump string, err error) {
	s := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(s, `"`):
		end := strings.IndexByte(s[1:], '"')
		if end < 1 {
			return "", "", errMalformedLine
		}
		name, s = s[1:1+end], s[2+end:]
	case strings.HasPrefix(s, "'"):
		var b strings.Builder
		i := 1
		for ; i < len(s); i++ {
			if s[i] != '\'' {
				b.WriteByte(s[i])
				continue
			}
			if i+1 < len(s) && s[i+1] == '\'' { // '' is a quote
				b.WriteByte('\'')
				i++
				continue
			}
			break
		}
		if i >= len(s) || b.Len() == 0 {
			return "", "", errMalformedLine
		}
		name, s = b.String(), s[i+1:]
	default:
		// A plain name can't hold a comment, so one ends the line here.
		s = stripComment(s)
		// A plain package name holds no colon, so the first one ends it; the
		// check below wants whitespace after it, as YAML does.
		key, rest, colon := strings.Cut(s, ":")
		key = strings.TrimSpace(key)
		if !plainKeyRe.MatchString(key) {
			return "", "", errMalformedLine
		}
		name, s = key, ""
		if colon {
			s = ":" + rest
			if rest == "" {
				s = ":"
			}
		}
	}
	s = strings.TrimSpace(stripComment(s))
	if s == "" {
		return name, "", nil
	}
	if s == ":" {
		return name, "", errNoBump
	}
	// YAML separates a mapping's value from its colon with whitespace:
	// `lib:patch` is one scalar, not a key and a value.
	if !strings.HasPrefix(s, ": ") && !strings.HasPrefix(s, ":\t") {
		return "", "", errMalformedLine
	}
	s = strings.TrimSpace(s[1:])
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
		if s == "" {
			return "", "", errMalformedLine // an explicit empty bump isn't an omitted one
		}
	}
	if !bumpWordRe.MatchString(s) {
		return "", "", errMalformedLine
	}
	return name, s, nil
}

// tabIndented reports whether a line's indentation holds a tab. YAML forbids
// tabs there, and @changesets' YAML parser refuses the frontmatter.
func tabIndented(line string) bool {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	return strings.ContainsRune(indent, '\t')
}

// stripComment drops a YAML comment: a # at the start, or after whitespace.
func stripComment(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return strings.TrimRight(s[:i], " \t")
		}
	}
	return s
}

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
		cs.Summary = summaryAfter(lines, i)
		normalizeConventional(cs)
		return cs, nil
	}

	seen := map[string]bool{}
	bare := "" // the first package line with no bump, which needs a type
	for i < len(lines) && lines[i] != "---" {
		line := lines[i]
		// A tab in the indentation of anything but a blank or comment line is
		// invalid YAML, which @changesets refuses.
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") && tabIndented(line) {
			return nil, fmt.Errorf("changeset %q: frontmatter line %q is indented with a tab, which YAML does not allow", id, line)
		}
		// Optional `type:` line (conventional-commit type, `!` => breaking).
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "type:") {
			val := strings.TrimSpace(strings.TrimPrefix(t, "type:"))
			val = strings.Trim(val, `"'`)
			cs.Breaking = strings.HasSuffix(val, "!")
			cs.Type = strings.ToLower(strings.TrimSuffix(val, "!"))
			i++
			continue
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "scope:") {
			cs.Scope = strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, "scope:")), `"'`)
			i++
			continue
		}
		// Blank lines and comments are YAML, and @changesets reads the
		// frontmatter as YAML.
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			i++
			continue
		}
		name, bumpText, err := parseReleaseLine(line)
		if errors.Is(err, errNoBump) {
			return nil, fmt.Errorf("changeset %q: %q has a colon but no bump; give major, minor, patch or none, or drop the colon to let the type decide", id, name)
		}
		if err != nil {
			return nil, fmt.Errorf("changeset %q: malformed frontmatter line %q", id, line)
		}
		// YAML refuses a repeated mapping key, and so does @changesets, rather
		// than letting the last one win.
		if seen[name] {
			return nil, fmt.Errorf("changeset %q: %q is listed more than once in the frontmatter; keep one line for it (quoted or not, it is the same package)", id, name)
		}
		seen[name] = true
		// Missing bump (`"Name"` with no `: bump`) means BumpNone → derive from type.
		bump := BumpNone
		if bumpText == "" && bare == "" {
			bare = name
		}
		if bumpText != "" {
			b, ok := ParseBump(bumpText)
			if !ok {
				return nil, fmt.Errorf("changeset %q: invalid bump type %q", id, bumpText)
			}
			bump = b
		}
		cs.Releases = append(cs.Releases, Release{Name: name, Bump: bump})
		i++
	}
	if i >= len(lines) {
		return nil, fmt.Errorf("changeset %q: missing closing frontmatter delimiter", id)
	}

	cs.Summary = summaryAfter(lines, i)
	normalizeConventional(cs)
	// A bare package line takes its bump from the type. With no type, from
	// the frontmatter or the summary's prefix, it has no bump at all and would
	// strand the changeset; @changesets refuses it too, as a frontmatter that
	// isn't a mapping.
	if bare != "" && cs.Type == "" {
		return nil, fmt.Errorf("changeset %q: %q has no bump; give it one (`: patch`, `: minor`, `: major` or `: none`), or add a `type:` line for it to take the bump from", id, bare)
	}
	return cs, nil
}

// summaryAfter returns the changeset summary: everything after the closing '---'
// at closeIdx. It skips a single blank separator line when one is present (the
// canonical layout) but does NOT skip a summary that begins on the very next
// line, which previously dropped the entire summary.
func summaryAfter(lines []string, closeIdx int) string {
	start := closeIdx + 1
	if start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	return joinFrom(lines, start)
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
// With no type to derive from, BumpNone is written as `: none`, since Parse
// refuses a bare line that has no type.
func Render(releases []Release, summary, typ string, breaking bool) string {
	return RenderScoped(releases, summary, typ, "", breaking)
}

// RenderScoped is Render with a conventional scope — which tool the change
// belongs to — written as its own frontmatter line.
func RenderScoped(releases []Release, summary, typ, scope string, breaking bool) string {
	var b strings.Builder
	b.WriteString("---\n")
	if typ != "" {
		if breaking {
			typ += "!"
		}
		fmt.Fprintf(&b, "type: %s\n", typ)
	}
	if scope != "" {
		fmt.Fprintf(&b, "scope: %s\n", scope)
	}
	_, _, prefixed := ParseConventional(summary)
	typed := typ != "" || prefixed
	for _, r := range releases {
		if r.Bump == BumpNone && typed {
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
// JS tool owns .md and this tool owns e.g. ".net.mkd"). It fails on the first
// file that can't be read or parsed; DirLenient reads past them.
func Dir(changesetDir, interopExt string) ([]*Changeset, error) {
	out, bad, err := DirLenient(changesetDir, interopExt)
	if err != nil {
		return nil, err
	}
	if len(bad) > 0 {
		return nil, bad[0].Err
	}
	return out, nil
}

// FileError is a changeset file that could not be read or parsed. Path is the
// file's full path; Err is the read or parse error, unwrapped.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *FileError) Unwrap() error { return e.Err }

// DirLenient reads a directory the way Dir does, but keeps going past a file
// that can't be read or parsed: it returns the changesets that did parse and,
// separately, every file that didn't, in directory order. err is reserved for
// the directory itself being unreadable. A health check uses it to name every
// broken file rather than stopping at the first, or at none.
func DirLenient(changesetDir, interopExt string) (out []*Changeset, bad []*FileError, err error) {
	entries, err := os.ReadDir(changesetDir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !isChangesetFile(e.Name(), interopExt) {
			continue
		}
		path := filepath.Join(changesetDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			bad = append(bad, &FileError{Path: path, Err: err})
			continue
		}
		id := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		cs, err := Parse(string(data), id)
		if err != nil {
			bad = append(bad, &FileError{Path: path, Err: err})
			continue
		}
		out = append(out, cs)
	}
	return out, bad, nil
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

// Ref is where a changeset came from, for changelog references.
type Ref struct {
	Commit string // the full SHA of the commit that added the changeset (or its source commit)
	PR     int    // its pull request, 0 when unknown
	Author string // its author's login, when resolved
}
