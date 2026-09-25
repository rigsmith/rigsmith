package cmdtest

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The reference changelogen plugin, run as the engine runs it: a request on
// stdin, the entry on stdout. Needs node; skipped without it.
func TestChangelogenExamplePlugin(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node isn't installed")
	}
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join(root, "examples", "plugins", "changeset-changelog-changelogen"), "render")
	cmd.Stdin = strings.NewReader(`{
		"apiVersion": 1,
		"package": { "name": "app", "displayName": "app", "currentVersion": "1.0.0", "newVersion": "1.1.0" },
		"options": { "repo": "acme/widgets" },
		"changes": [
			{ "bump": "minor", "type": "feat", "scope": "cli", "summary": "feat:\n\nAdd a --json flag\nwith a second line", "commit": "abcdef1234567", "pr": 42 },
			{ "bump": "patch", "type": "fix", "summary": "\nfix: Keep line endings" },
			{ "bump": "patch", "summary": "Updated dependencies\n  - lib@2.0.0", "dependencies": true }
		],
		"dependencyUpdates": [ { "name": "lib", "displayName": "lib", "newVersion": "2.0.0" } ],
		"contributors": [ { "name": "Ada Lovelace", "login": "ada" } ]
	}`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("plugin failed: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"## 1.1.0\n\n### 🚀 Enhancements\n\n- **cli:** Add a --json flag ([#42](https://github.com/acme/widgets/pull/42))\n  with a second line",
		"### 🩹 Fixes\n\n- Keep line endings",
		"### 🌊 Dependencies\n\n- lib@2.0.0",
		"### ❤️ Contributors\n\n- Ada Lovelace ([@ada](https://github.com/ada))",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Updated dependencies") {
		t.Errorf("the flagged dependency change was rendered too:\n%s", got)
	}
}
