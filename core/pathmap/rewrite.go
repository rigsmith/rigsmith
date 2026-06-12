package pathmap

import "strings"

// PortablizeJSONValues walks a parsed JSON value and replaces every string that
// is an absolute path under a known folder with its portable template ($HOME/…),
// leaving all other strings untouched. It is the sync-side transform for files
// whose path fields must travel portably (Desktop session metadata's cwd /
// originCwd / planPath / added directories, settings.json's additionalDirectories
// / marketplace paths, …). Returns the rewritten value and the count changed.
//
// Value-based (not field-name-based) so it is robust to fields we haven't
// enumerated; non-path strings and system paths (/tmp) outside any known folder
// pass through unchanged.
func PortablizeJSONValues(v any, folders map[string]string, srcOS string) (any, int) {
	n := 0
	var walk func(any) any
	walk = func(node any) any {
		switch t := node.(type) {
		case map[string]any:
			for k, val := range t {
				t[k] = walk(val)
			}
			return t
		case []any:
			for i, val := range t {
				t[i] = walk(val)
			}
			return t
		case string:
			if tmpl, ok := Portablize(t, folders, srcOS); ok {
				n++
				return tmpl
			}
			return t
		default:
			return node
		}
	}
	return walk(v), n
}

// ResolveJSONValues is the restore-side inverse: it resolves every string that is
// a portable template (starts with '$') into the target machine's native path,
// leaving everything else untouched. Only '$'-prefixed strings are touched, so
// ordinary values never change.
func ResolveJSONValues(v any, target *Resolver) (any, int) {
	n := 0
	var walk func(any) any
	walk = func(node any) any {
		switch t := node.(type) {
		case map[string]any:
			for k, val := range t {
				t[k] = walk(val)
			}
			return t
		case []any:
			for i, val := range t {
				t[i] = walk(val)
			}
			return t
		case string:
			if strings.HasPrefix(t, "$") {
				if res := target.Resolve(t); res.IsResolved() {
					n++
					return res.Path
				}
			}
			return t
		default:
			return node
		}
	}
	return walk(v), n
}
