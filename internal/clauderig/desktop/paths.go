package desktop

import (
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
)

// PortablizeJSONPaths applies the common JSON path transform plus Desktop's
// embedded permission-reason format. Both transforms modify the parsed document
// in place; callers serialize the result into staging, never over the live file.
func PortablizeJSONPaths(v any, folders map[string]string, srcOS string) (any, int) {
	v, n := pathmap.PortablizeJSONValues(v, folders, srcOS)
	n += rewritePermissionReasons(v, func(p string) (string, bool) { return pathmap.Portablize(p, folders, srcOS) })
	return v, n
}

// ResolveJSONPaths resolves the same path surfaces onto the destination machine.
func ResolveJSONPaths(v any, target *pathmap.Resolver) (any, int) {
	v, n := pathmap.ResolveJSONValues(v, target)
	n += rewritePermissionReasons(v, func(p string) (string, bool) {
		if !strings.HasPrefix(p, "$") {
			return p, false
		}
		result := target.Resolve(p)
		return result.Path, result.IsResolved()
	})
	return v, n
}

// Desktop stores reasons as tool<NUL>reason<NUL>file_path:absolute-path.
// Rewrite only the path argument of that exact shape in alwaysAllowedReasons;
// prose, other argument types and malformed records are deliberately untouched.
func rewritePermissionReasons(v any, rewrite func(string) (string, bool)) int {
	n := 0
	switch node := v.(type) {
	case map[string]any:
		for key, value := range node {
			if key != "alwaysAllowedReasons" {
				n += rewritePermissionReasons(value, rewrite)
				continue
			}
			reasons, ok := value.([]any)
			if !ok {
				continue
			}
			for i, item := range reasons {
				reason, ok := item.(string)
				if !ok {
					continue
				}
				parts := strings.Split(reason, "\x00")
				if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
					continue
				}
				p, ok := strings.CutPrefix(parts[2], "file_path:")
				if !ok {
					continue
				}
				if rewritten, ok := rewrite(p); ok {
					parts[2] = "file_path:" + rewritten
					reasons[i] = strings.Join(parts, "\x00")
					n++
				}
			}
		}
	case []any:
		for _, value := range node {
			n += rewritePermissionReasons(value, rewrite)
		}
	}
	return n
}
