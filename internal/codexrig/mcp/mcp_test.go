package mcp

import (
	"os"
	"path/filepath"
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

// An environment value is a string or it is a mistake; a server with an
// integer in [env] is one Codex cannot start, and a portability verdict for
// it would be a verdict about nothing.
func TestANonStringEnvValueIsAnErrorNamingTheServer(t *testing.T) {
	home := t.TempDir()
	cfg := "[mcp_servers.x]\ncommand = \"npx\"\n[mcp_servers.x.env]\nPORT = 7000\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := List(home, pathmap.MapFolders{"HOME": "/Users/x"}, pathmap.OSMacOS)
	if err == nil || !strings.Contains(err.Error(), "PORT") || !strings.Contains(err.Error(), `"x"`) {
		t.Fatalf("err = %v, want one naming the server and the key", err)
	}
}
