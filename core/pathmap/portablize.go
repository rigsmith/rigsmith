package pathmap

import "strings"

// Portablize is the reverse of Resolve: it maps an absolute path back to a
// token-bearing portable template by replacing the longest matching known-folder
// prefix with its $TOKEN. The remainder is emitted with '/' separators so the
// template resolves natively on any target OS.
//
// srcOS controls how the prefix is matched: it sets the separator and the case
// sensitivity (Linux is case-sensitive; macOS/Windows are not — a synced path
// captured as /Users/John still matches a home of /Users/john).
//
// ok is false when no known folder is a prefix of abs (the caller decides the
// fallback — typically keep abs unchanged and translate nothing).
//
//	Portablize(`/Users/john/Git/x`, {"HOME":"/Users/john"}, OSMacOS) → "$HOME/Git/x", true
//	Portablize(`C:\Users\John\Git\x`, {"HOME":`C:\Users\John`}, OSWindows) → "$HOME/Git/x", true
func Portablize(abs string, folders map[string]string, srcOS string) (template string, ok bool) {
	sep := byte('/')
	if srcOS == OSWindows {
		sep = '\\'
	}
	fold := srcOS != OSLinux

	// Collapse a leading run of POSIX separators so artifacts like a permission
	// rule's "//Users/john/…/**" still match the home prefix (keeping one slash).
	if sep == '/' {
		i := 0
		for i < len(abs) && abs[i] == '/' {
			i++
		}
		if i > 1 {
			abs = abs[i-1:]
		}
	}

	bestName, bestBase := "", ""
	for name, base := range folders {
		base = trimTrailingSep(base)
		if base == "" {
			continue
		}
		if !prefixAtBoundary(abs, base, sep, fold) {
			continue
		}
		if len(base) > len(bestBase) {
			bestName, bestBase = name, base
		}
	}
	if bestName == "" {
		return "", false
	}

	remainder := abs[len(bestBase):] // "" or starts with sep
	remainder = strings.ReplaceAll(remainder, string(sep), "/")
	return "$" + bestName + remainder, true
}

// prefixAtBoundary reports whether base is a path-prefix of s ending on a segment
// boundary: s equals base, or s continues with a separator right after base.
func prefixAtBoundary(s, base string, sep byte, fold bool) bool {
	if len(s) < len(base) {
		return false
	}
	head := s[:len(base)]
	if fold {
		if !strings.EqualFold(head, base) {
			return false
		}
	} else if head != base {
		return false
	}
	return len(s) == len(base) || s[len(base)] == sep
}

func trimTrailingSep(p string) string {
	if len(p) > 1 {
		return strings.TrimRight(p, `/\`)
	}
	return p
}
