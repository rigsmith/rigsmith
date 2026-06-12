package pathmap

import "strings"

// OS tokens the cascade and resolver evaluate against.
const (
	OSMacOS   = "macos"
	OSWindows = "windows"
	OSLinux   = "linux"
)

// maxDepth is a backstop only — the visited-set cycle check is the real guard.
// It bounds pathological-but-acyclic token nesting.
const maxDepth = 32

// Resolver expands a raw path template into an absolute path for a fixed target
// OS. Tokens may be predefined (via KnownFolders, always leaves) or custom (their
// own Cascade, expanded recursively). Recursion is bounded by a visited-set cycle
// check keyed on token name, so a self- or mutually-referential token yields
// StatusCycle rather than a stack overflow — which also makes the resolver a
// validator: resolve a proposed token value and reject a StatusCycle at edit time.
//
// Syntax: $NAME (ends at the first non-word char), ${NAME} (explicit braces), and
// a leading ~ as sugar for $HOME. Token names are case-insensitive. A token is
// recognised only at a path boundary — the start of the string or immediately
// after '/' or '\\' — so a stray '$' mid-segment stays literal. The final path
// must be rooted for the target OS.
type Resolver struct {
	known  KnownFolders
	os     string
	tokens map[string]Cascade // keys lower-cased
}

// NewResolver builds a resolver for the given target OS (OSMacOS/OSWindows/
// OSLinux). known resolves predefined tokens (at minimum "HOME"); tokens is the
// optional custom token table (name → cascade), matched case-insensitively.
func NewResolver(known KnownFolders, os string, tokens map[string]Cascade) *Resolver {
	lowered := make(map[string]Cascade, len(tokens))
	for k, v := range tokens {
		lowered[strings.ToLower(k)] = v
	}
	return &Resolver{known: known, os: os, tokens: lowered}
}

// Resolve expands template to an absolute path. A blank template is
// StatusUnconfigured (no path here).
func (r *Resolver) Resolve(template string) Resolution {
	if strings.TrimSpace(template) == "" {
		return Unconfigured("")
	}
	res := r.expand(template, map[string]bool{}, 0)
	if !res.IsResolved() {
		return res
	}
	native := r.normalize(res.Path)
	if r.isRooted(native) {
		return Resolved(native)
	}
	return Invalid("")
}

// expand is the recursive worker. visited holds the custom-token names currently
// being expanded (the cycle guard).
func (r *Resolver) expand(template string, visited map[string]bool, depth int) Resolution {
	if depth > maxDepth {
		return Invalid("")
	}
	runes := []rune(template)
	n := len(runes)
	var sb strings.Builder
	sb.Grow(len(template) + 32)

	for i := 0; i < n; {
		c := runes[i]
		atBoundary := i == 0 || runes[i-1] == '/' || runes[i-1] == '\\'

		// '~' is sugar for $HOME, only at a boundary and only when it stands alone
		// as a segment (followed by a separator or end-of-string).
		if atBoundary && c == '~' && (i+1 == n || runes[i+1] == '/' || runes[i+1] == '\\') {
			home, ok := r.known.Resolve("HOME")
			if !ok {
				return Unconfigured("HOME")
			}
			sb.WriteString(home)
			i++
			continue
		}

		if atBoundary && c == '$' {
			name, next, ok := readToken(runes, i)
			if !ok {
				// A lone '$' not forming a token — literal.
				sb.WriteRune(c)
				i++
				continue
			}
			sub := r.resolveTokenName(name, visited, depth)
			if !sub.IsResolved() {
				return sub
			}
			sb.WriteString(sub.Path)
			i = next
			continue
		}

		sb.WriteRune(c)
		i++
	}
	return Resolved(sb.String())
}

// resolveTokenName resolves a bare token name to an absolute path. Predefined
// tokens win over custom (reserved names can never be shadowed).
func (r *Resolver) resolveTokenName(name string, visited map[string]bool, depth int) Resolution {
	if pre, ok := r.known.Resolve(name); ok {
		return Resolved(pre)
	}
	key := strings.ToLower(name)
	cascade, ok := r.tokens[key]
	if !ok {
		return Invalid(name) // undefined token reference
	}
	if visited[key] {
		return Cycle(name)
	}
	visited[key] = true
	raw := cascade.RawFor(r.os)
	if strings.TrimSpace(raw) == "" {
		delete(visited, key)
		return Unconfigured(name)
	}
	res := r.expand(raw, visited, depth+1)
	delete(visited, key)
	return res
}

// readToken reads a $NAME or ${NAME} token starting at start (the '$'). It returns
// the bare name and the index just past the token, or ok=false when no valid name
// follows.
func readToken(s []rune, start int) (name string, next int, ok bool) {
	i := start + 1
	braced := i < len(s) && s[i] == '{'
	if braced {
		i++
	}
	nameStart := i
	for i < len(s) && (isWord(s[i])) {
		i++
	}
	if i == nameStart {
		return "", start, false // "$" or "${" with no name
	}
	name = string(s[nameStart:i])
	if braced {
		if i >= len(s) || s[i] != '}' {
			return "", start, false // unterminated "${NAME"
		}
		i++ // consume '}'
	}
	return name, i, true
}

func isWord(c rune) bool {
	return c == '_' ||
		(c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}

// sep is the target OS's directory separator.
func (r *Resolver) sep() rune {
	if r.os == OSWindows {
		return '\\'
	}
	return '/'
}

// normalize swaps the foreign separator for the target OS's so a portable
// template written with '/' lands as a native path on Windows.
func (r *Resolver) normalize(path string) string {
	if r.sep() == '\\' {
		return strings.ReplaceAll(path, "/", `\`)
	}
	return strings.ReplaceAll(path, `\`, "/")
}

// isRooted reports whether path is absolute for the target OS. Windows accepts a
// drive root (C:\ or C:/), a UNC prefix (\\), or a leading separator; POSIX
// requires a leading '/'.
func (r *Resolver) isRooted(path string) bool {
	if r.os == OSWindows {
		if strings.HasPrefix(path, `\\`) {
			return true
		}
		if len(path) >= 3 && isAlpha(path[0]) && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
			return true
		}
		return len(path) >= 1 && (path[0] == '\\' || path[0] == '/')
	}
	return strings.HasPrefix(path, "/")
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
