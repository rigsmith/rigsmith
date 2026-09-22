package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

// nothingToVersion gates the built-in version step: `changerig version` exits
// 1 with nothing pending (as @changesets does), so a release that only
// publishes what is already versioned must skip the step, not fail on it.
func TestNothingToVersion(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	write("package-lock.json", "{}")
	write("packages/pkg-a/package.json", `{ "name": "pkg-a", "version": "1.0.0" }`)
	write(".changeset/config.json", `{ "updateInternalDependencies": "patch" }`)
	t.Chdir(root)

	open := func() *commands.Workspace {
		t.Helper()
		ws, err := commands.Open()
		if err != nil {
			t.Fatal(err)
		}
		return ws
	}

	if !nothingToVersion(context.Background(), open()) {
		t.Error("no changesets: the version step should be skipped")
	}

	write(".changeset/cs.md", "---\n\"pkg-a\": patch\n---\n\na fix\n")
	if nothingToVersion(context.Background(), open()) {
		t.Error("a pending changeset: the version step must run")
	}
}
