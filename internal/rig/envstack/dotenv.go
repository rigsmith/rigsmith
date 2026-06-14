// Package envstack provides a dependency-free .env reader and layered
// environment merging, ported from the rig .NET implementation
// (DotEnv.cs and EnvStack in Exec.cs).
//
// Supported .env subset (v1): KEY=VALUE, blank lines, full-line # comments,
// optional "export " prefix, single- and double-quoted values (double quotes
// honour \n \t \r \" \\ escapes; single quotes are literal), and inline #
// comments on unquoted values when preceded by whitespace. No ${VAR}
// expansion yet.
package envstack

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

// caseInsensitiveKeys mirrors the C# EnvStack.Comparer: env-var names are
// case-insensitive on Windows, case-sensitive elsewhere.
var caseInsensitiveKeys = runtime.GOOS == "windows"

// set assigns k=v in m, honouring the platform key comparison. On Windows an
// existing key that differs only by case is overwritten in place, preserving
// the first-seen casing (matching a C# Dictionary with OrdinalIgnoreCase).
func set(m map[string]string, k, v string) {
	if caseInsensitiveKeys {
		if _, ok := m[k]; !ok {
			for existing := range m {
				if strings.EqualFold(existing, k) {
					m[existing] = v
					return
				}
			}
		}
	}
	m[k] = v
}

// Parse parses .env text into a map. Later duplicate keys win.
func Parse(content string) map[string]string {
	result := make(map[string]string)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimLeftFunc(line[len("export "):], unicode.IsSpace)
		}

		eq := strings.IndexByte(line, '=')
		if eq <= 0 { // no key, or leading '='
			continue
		}

		key := strings.TrimSpace(line[:eq])
		if !isValidKey(key) {
			continue
		}

		set(result, key, parseValue(strings.TrimSpace(line[eq+1:])))
	}
	return result
}

// Load reads .env then .env.local from dir; .env.local overrides .env.
// Missing files are skipped; any other read error is returned.
func Load(dir string) (map[string]string, error) {
	m := make(map[string]string)
	for _, name := range []string{".env", ".env.local"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for k, v := range Parse(string(data)) {
			set(m, k, v)
		}
	}
	return m, nil
}

func isValidKey(key string) bool {
	if key == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(key)
	if !(unicode.IsLetter(first) || first == '_') {
		return false
	}
	for _, c := range key {
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_') {
			return false
		}
	}
	return true
}

func parseValue(raw string) string {
	if raw == "" {
		return ""
	}

	switch raw[0] {
	case '\'': // single quotes are literal — first closing quote wins
		if end := strings.IndexByte(raw[1:], '\''); end >= 0 {
			return raw[1 : 1+end]
		}
		return raw[1:]
	case '"': // double quotes honour escapes, incl. an escaped \" inside
		if end := closingDoubleQuote(raw); end >= 0 {
			return unescape(raw[1:end])
		}
		return unescape(raw[1:])
	}

	if comment := inlineCommentIndex(raw); comment >= 0 {
		raw = raw[:comment]
	}
	return strings.TrimSpace(raw)
}

// closingDoubleQuote returns the index of the closing double-quote, skipping
// any backslash-escaped char, or -1 if unterminated.
func closingDoubleQuote(s string) int {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++ // skip the escaped char
			continue
		}
		if s[i] == '"' {
			return i
		}
	}
	return -1
}

// inlineCommentIndex returns the index of a '#' preceded by whitespace, or -1.
func inlineCommentIndex(s string) int {
	for i := 1; i < len(s); i++ {
		if s[i] == '#' {
			if prev, _ := utf8.DecodeLastRuneInString(s[:i]); unicode.IsSpace(prev) {
				return i
			}
		}
	}
	return -1
}

func unescape(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default: // unknown escape: keep the char, drop the backslash
				sb.WriteByte(s[i])
			}
		} else {
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}
