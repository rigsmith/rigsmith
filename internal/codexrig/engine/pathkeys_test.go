package engine

import (
	"reflect"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
)

func TestPathKeysRoundTripAcrossMachines(t *testing.T) {
	// The case clauderig never had to handle: Codex addresses per-project
	// settings BY path, so the path is a table key rather than a value.
	const srcHome = "/Users/one"
	const dstHome = "/Users/two"
	srcFolders := pathmap.MapFolders{"HOME": srcHome}
	dst := pathmap.NewResolver(pathmap.MapFolders{"HOME": dstHome}, pathmap.OSMacOS, nil)

	in := map[string]any{
		"projects": map[string]any{
			srcHome + "/Git/thing": map[string]any{"trust_level": "trusted"},
		},
		"desktop": map[string]any{
			"open-in-target-preferences": map[string]any{
				"perPath": map[string]any{srcHome + "/Git/other": "vscode"},
			},
		},
		// Ordinary keys, which must be left exactly alone. No exception list
		// does that — Portablize simply fails for anything that is not a path
		// under a known folder.
		"features":    map[string]any{"js_repl": false},
		"mcp_servers": map[string]any{"railway": map[string]any{"command": "railway"}},
	}

	portable, n := PortablizeKeys(in, srcFolders, pathmap.OSMacOS)
	if n != 2 {
		t.Fatalf("portablized %d keys, want 2", n)
	}
	pm := portable.(map[string]any)
	if _, ok := pm["projects"].(map[string]any)["$HOME/Git/thing"]; !ok {
		t.Errorf("the project key was not portablized: %+v", pm["projects"])
	}
	if _, ok := pm["features"].(map[string]any)["js_repl"]; !ok {
		t.Errorf("an ordinary key was disturbed: %+v", pm["features"])
	}

	resolved, n := ResolveKeys(portable, dst)
	if n != 2 {
		t.Fatalf("resolved %d keys, want 2", n)
	}
	rm := resolved.(map[string]any)
	if _, ok := rm["projects"].(map[string]any)[dstHome+"/Git/thing"]; !ok {
		t.Errorf("the project key did not land on the second machine: %+v", rm["projects"])
	}
	per := rm["desktop"].(map[string]any)["open-in-target-preferences"].(map[string]any)["perPath"].(map[string]any)
	if per[dstHome+"/Git/other"] != "vscode" {
		t.Errorf("a nested path key did not resolve: %+v", per)
	}
}

func TestACompoundHookKeyKeepsItsAddressingTail(t *testing.T) {
	// Codex's hook trust keys are "<abs path>:<event>:<group>:<handler>". Only
	// the path half may be rewritten; chopping at the first colon would take a
	// Windows drive letter with it.
	folders := pathmap.MapFolders{"HOME": "/Users/one"}
	in := map[string]any{
		"/Users/one/Git/thing/.codex/hooks.json:pre_tool_use:0:0": "x",
	}
	out, n := PortablizeKeys(in, folders, pathmap.OSMacOS)
	if n != 1 {
		t.Fatalf("portablized %d keys, want 1", n)
	}
	if _, ok := out.(map[string]any)["$HOME/Git/thing/.codex/hooks.json:pre_tool_use:0:0"]; !ok {
		t.Errorf("the compound key lost its tail or its path: %+v", out)
	}
}

func TestAKeyThatCannotBeResolvedHereIsKeptVerbatim(t *testing.T) {
	// Dropping it would silently delete the setting it names.
	r := pathmap.NewResolver(pathmap.MapFolders{"HOME": "/Users/two"}, pathmap.OSMacOS, nil)
	in := map[string]any{"$DROPBOX/thing": map[string]any{"trust_level": "trusted"}}
	out, n := ResolveKeys(in, r)
	if n != 0 {
		t.Fatalf("resolved %d keys, want 0 — $DROPBOX is not known here", n)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("the unresolvable key was altered: %+v", out)
	}
}

func TestAnOrdinaryKeyIsNeverMistakenForAPath(t *testing.T) {
	folders := pathmap.MapFolders{"HOME": "/Users/one"}
	in := map[string]any{
		"model": "gpt-6-astra", "features": map[string]any{"js_repl": false},
		"plugins": map[string]any{"documents@openai-primary-runtime": map[string]any{"enabled": true}},
	}
	out, n := PortablizeKeys(in, folders, pathmap.OSMacOS)
	if n != 0 {
		t.Fatalf("portablized %d ordinary keys, want 0", n)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("ordinary keys were altered: %+v", out)
	}
}
