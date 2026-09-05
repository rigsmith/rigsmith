package desktop

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
)

const permissionReasonPrefix = "Read\x00Path is outside allowed working directories\x00file_path:"

func TestPermissionReasonPathsRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, sourceOS, sourceHome, sourcePath, targetOS, targetHome, targetPath string }{
		{"mac-to-windows", pathmap.OSMacOS, "/Users/you", "/Users/you/My Project/notes.txt", pathmap.OSWindows, `C:\Users\Other`, `C:\Users\Other\My Project\notes.txt`},
		{"windows-to-linux", pathmap.OSWindows, `C:\Users\You`, `C:\Users\You\My Project\notes.txt`, pathmap.OSLinux, "/home/other", "/home/other/My Project/notes.txt"},
		{"linux-to-mac", pathmap.OSLinux, "/home/you", "/home/you/My Project/notes.txt", pathmap.OSMacOS, "/Users/other", "/Users/other/My Project/notes.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := permissionReasonPrefix + tc.sourcePath
			v := map[string]any{"cwd": tc.sourceHome, "alwaysAllowedReasons": []any{original}, "description": original}
			portable, n := PortablizeJSONPaths(v, map[string]string{"HOME": tc.sourceHome}, tc.sourceOS)
			if n != 2 {
				t.Fatalf("rewrote %d values, want cwd and reason path", n)
			}
			if got := v["alwaysAllowedReasons"].([]any)[0]; got != permissionReasonPrefix+"$HOME/My Project/notes.txt" {
				t.Fatalf("portable reason = %q", got)
			}
			if v["description"] != original {
				t.Fatal("rewrote ordinary prose")
			}
			resolved, n := ResolveJSONPaths(portable, pathmap.NewResolver(pathmap.MapFolders{"HOME": tc.targetHome}, tc.targetOS, nil))
			if n != 2 {
				t.Fatalf("resolved %d values, want 2", n)
			}
			if got := resolved.(map[string]any)["alwaysAllowedReasons"].([]any)[0]; got != permissionReasonPrefix+tc.targetPath {
				t.Fatalf("resolved reason = %q", got)
			}
			// The reason/tool delimiters and Windows drive colon must also survive a
			// reverse trip; this is a format adapter, not a substring replacement.
			back, _ := PortablizeJSONPaths(resolved, map[string]string{"HOME": tc.targetHome}, tc.targetOS)
			back, _ = ResolveJSONPaths(back, pathmap.NewResolver(pathmap.MapFolders{"HOME": tc.sourceHome}, tc.sourceOS, nil))
			if got := back.(map[string]any)["alwaysAllowedReasons"].([]any)[0]; got != original {
				t.Fatalf("reverse roundtrip = %q", got)
			}
		})
	}
}

func TestPermissionReasonsOnlyRewriteKnownPathFields(t *testing.T) {
	values := []any{
		"Read without delimiters file_path:/Users/you/file",
		"Read\x00reason\x00command:cat /Users/you/file",
		"Read\x00reason\x00file_path:/Users/you/file\x00extra",
		"\x00reason\x00file_path:/Users/you/file",
		permissionReasonPrefix + "/Users/younger/file",
		permissionReasonPrefix + "/tmp/file",
		permissionReasonPrefix + "$UNCONFIGURED/file",
		42, nil,
	}
	want := append([]any(nil), values...)
	v := map[string]any{"alwaysAllowedReasons": values}
	portable, n := PortablizeJSONPaths(v, map[string]string{"HOME": "/Users/you"}, pathmap.OSMacOS)
	if n != 0 {
		t.Fatalf("changed %d unsupported values", n)
	}
	resolved, n := ResolveJSONPaths(portable, pathmap.NewResolver(pathmap.MapFolders{"HOME": "/Users/other"}, pathmap.OSMacOS, nil))
	if n != 0 || !reflect.DeepEqual(resolved.(map[string]any)["alwaysAllowedReasons"], want) {
		t.Fatal("changed unsupported/unconfigured reasons")
	}
	nested := []any{map[string]any{"alwaysAllowedReasons": []any{permissionReasonPrefix + "//Users/you/project/**"}}}
	portable, n = PortablizeJSONPaths(nested, map[string]string{"HOME": "/Users/you"}, pathmap.OSMacOS)
	if n != 1 || !strings.HasSuffix(portable.([]any)[0].(map[string]any)["alwaysAllowedReasons"].([]any)[0].(string), "$HOME/project/**") {
		t.Fatal("nested permission glob not rewritten")
	}
}
