package pathmap

import (
	"encoding/json"
	"testing"
)

func TestPortablizeAndResolveJSONValues_RoundTrip(t *testing.T) {
	// A Desktop-session-shaped doc captured on macOS.
	src := []byte(`{
		"cwd": "/Users/john/Git/rigsmith",
		"originCwd": "/Users/john/Git",
		"planPath": "/Users/john/.claude/plans/x.md",
		"model": "claude-fable-5",
		"directories": ["/Users/john/Git/x", "/tmp"],
		"completedTurns": 2
	}`)
	var v any
	if err := json.Unmarshal(src, &v); err != nil {
		t.Fatal(err)
	}

	// sync side: portablize against john's macOS home
	pv, n := PortablizeJSONValues(v, map[string]string{"HOME": "/Users/john"}, OSMacOS)
	if n != 4 { // cwd, originCwd, planPath, directories[0] — NOT /tmp or non-paths
		t.Fatalf("portablized %d values, want 4", n)
	}

	// restore side: resolve onto a Windows machine
	win := NewResolver(MapFolders{"HOME": `C:\Users\Jane`}, OSWindows, nil)
	rv, m := ResolveJSONValues(pv, win)
	if m != 4 {
		t.Fatalf("resolved %d values, want 4", m)
	}

	out := rv.(map[string]any)
	if out["cwd"] != `C:\Users\Jane\Git\rigsmith` {
		t.Errorf("cwd = %v", out["cwd"])
	}
	if out["planPath"] != `C:\Users\Jane\.claude\plans\x.md` {
		t.Errorf("planPath = %v", out["planPath"])
	}
	dirs := out["directories"].([]any)
	if dirs[0] != `C:\Users\Jane\Git\x` || dirs[1] != "/tmp" {
		t.Errorf("directories = %v (/tmp must be untouched)", dirs)
	}
	// non-path values untouched
	if out["model"] != "claude-fable-5" || out["completedTurns"].(float64) != 2 {
		t.Errorf("non-path values changed: %v", out)
	}
}

func TestResolveJSONValues_IgnoresNonTemplates(t *testing.T) {
	v := map[string]any{"a": "high", "b": "/already/abs", "c": "$HOME/x"}
	r := NewResolver(MapFolders{"HOME": "/Users/john"}, OSMacOS, nil)
	out, n := ResolveJSONValues(v, r)
	if n != 1 {
		t.Fatalf("resolved %d, want 1 (only the $-template)", n)
	}
	m := out.(map[string]any)
	if m["a"] != "high" || m["b"] != "/already/abs" || m["c"] != "/Users/john/x" {
		t.Errorf("got %v", m)
	}
}
