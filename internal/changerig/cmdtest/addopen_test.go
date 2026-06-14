package cmdtest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `add --open` launches $EDITOR on the created changeset. A fake editor records
// the path it was handed so we can assert it's the new .changeset/*.md file.
func TestAddOpenLaunchesEditorOnTheChangeset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake-editor marker script is POSIX sh")
	}
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	runChangerig(t, dir, "init")

	marker := filepath.Join(dir, "editor-marker.txt")
	script := filepath.Join(dir, "fake-editor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \""+marker+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", script)

	code, out := runChangerig(t, dir, "add", "-m", "fix something", "-p", "pkg-a", "--open")
	assertExitZero(t, code, out)

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("editor was not invoked (no marker): %v\n%s", err, out)
	}
	path := strings.TrimSpace(string(got))
	if !strings.Contains(path, ".changeset"+string(os.PathSeparator)) || !strings.HasSuffix(path, ".md") {
		t.Errorf("editor opened %q, want the created .changeset/*.md file", path)
	}
}
