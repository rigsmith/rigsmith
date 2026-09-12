package mcp

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
)

// A field name alone told a script that something needed attention and not
// what: "args" for a server with five arguments.
func TestLocalPathsNameTheValueNotJustTheField(t *testing.T) {
	s := Server{Command: "/opt/homebrew/bin/x", Args: []string{"--root", "/srv/data", "ok"}}
	p := judge(s, pathmap.MapFolders{"HOME": "/Users/x"}, pathmap.OSMacOS)
	want := []string{"args[1]=/srv/data", "command=/opt/homebrew/bin/x"}
	if strings.Join(p.LocalPaths, "|") != strings.Join(want, "|") {
		t.Errorf("LocalPaths = %v, want %v", p.LocalPaths, want)
	}
}
