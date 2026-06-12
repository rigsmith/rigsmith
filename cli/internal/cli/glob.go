package cli

import "strings"

// excluded reports whether name matches any of the .rig.json `exclude` globs.
func excluded(name string, patterns []string) bool {
	for _, p := range patterns {
		if globMatch(p, name) {
			return true
		}
	}
	return false
}

// globMatch is a minimal '*' glob (matches the planner's ignore semantics):
// supports leading/trailing/middle '*', e.g. "*Bench", "Acme.*", "*.Demo".
func globMatch(pattern, name string) bool {
	if pattern == name {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return false
	}
	parts := strings.Split(pattern, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(name[pos:], part)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			return false
		}
		pos += idx + len(part)
	}
	if last := parts[len(parts)-1]; last != "" {
		return strings.HasSuffix(name, last)
	}
	return true
}
