package engine

import (
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
)

// Codex writes absolute paths as TABLE KEYS, not only as values:
//
//	[projects."/Users/someone/Git/thing"]
//	[hooks.state."/Users/someone/Git/thing/.codex/hooks.json:pre_tool_use:0:0"]
//	[desktop.open-in-target-preferences.perPath]
//	  "/Users/someone/Git/thing" = "vscode"
//
// Claude Code does not do this, so clauderig's path rewriting — which walks
// VALUES — has no equivalent, and porting it unchanged leaves every one of those
// keys spelled in the source machine's home. The symptom is quiet and bad: a
// restored config trusts a project directory that does not exist on the target
// machine, and does not trust the one that does.
//
// So the tree walk here rewrites keys as well as values. The test for "is this a
// path" is Portablize itself: it only succeeds for something under a known
// folder, so an ordinary key like "features" or "railway" is left alone by
// construction rather than by a list of exceptions.

// PortablizeKeys rewrites absolute-path map keys into portable templates,
// returning the new tree and how many keys changed.
func PortablizeKeys(v any, folders pathmap.MapFolders, srcOS string) (any, int) {
	n := 0
	out := mapKeys(v, func(k string) string {
		if tmpl, ok := portablizeKey(k, folders, srcOS); ok {
			n++
			return tmpl
		}
		return k
	})
	return out, n
}

// ResolveKeys rewrites portable-template map keys back into this machine's
// paths. A template that does not resolve here is left exactly as it is: a key
// this machine cannot place is better kept verbatim than dropped, since dropping
// it silently deletes the setting it names.
func ResolveKeys(v any, r *pathmap.Resolver) (any, int) {
	n := 0
	out := mapKeys(v, func(k string) string {
		if resolved, ok := resolveKey(k, r); ok {
			n++
			return resolved
		}
		return k
	})
	return out, n
}

// portablizeKey handles both a bare path and Codex's compound hook key, whose
// path is followed by ":<event>:<group>:<handler>". Splitting on the LAST colon
// run rather than the first matters on Windows, where a drive letter puts a
// colon in the path itself.
func portablizeKey(k string, folders pathmap.MapFolders, srcOS string) (string, bool) {
	path, suffix := splitCompoundKey(k)
	tmpl, ok := pathmap.Portablize(path, folders, srcOS)
	if !ok {
		return "", false
	}
	return tmpl + suffix, true
}

func resolveKey(k string, r *pathmap.Resolver) (string, bool) {
	if !strings.HasPrefix(k, "$") && !strings.HasPrefix(k, "~") {
		return "", false
	}
	path, suffix := splitCompoundKey(k)
	res := r.Resolve(path)
	if !res.IsResolved() {
		return "", false
	}
	return res.Path + suffix, true
}

// splitCompoundKey separates a leading path from a trailing ":"-delimited tail.
// The tail is recognised only when it looks like Codex's hook addressing —
// three colon-separated fields where the last two are numbers — so an ordinary
// path containing a colon is not chopped.
func splitCompoundKey(k string) (path, suffix string) {
	parts := strings.Split(k, ":")
	if len(parts) < 4 {
		return k, ""
	}
	tail := parts[len(parts)-3:]
	if !allDigits(tail[1]) || !allDigits(tail[2]) {
		return k, ""
	}
	cut := len(k) - (len(tail[0]) + len(tail[1]) + len(tail[2]) + 3)
	return k[:cut], k[cut:]
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// mapKeys returns a copy of the tree with every map key passed through fn.
//
// A rename that collides with a key already present keeps the EXISTING value:
// two keys mapping to one is a config that already had a duplicate meaning, and
// silently letting the later one win would make the result depend on Go's map
// iteration order.
func mapKeys(v any, fn func(string) string) any {
	switch n := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			nk := fn(k)
			child := mapKeys(val, fn)
			if _, taken := out[nk]; taken {
				continue
			}
			out[nk] = child
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, val := range n {
			out[i] = mapKeys(val, fn)
		}
		return out
	default:
		return v
	}
}
