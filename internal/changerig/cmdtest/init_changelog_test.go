package cmdtest

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// initRepo is an npm workspace with no .changeset yet, on GitHub when remote
// is set.
func initRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	gitInit(t, dir)
	if remote != "" {
		git(t, dir, "remote", "add", "origin", remote)
	}
	return dir
}

func initConfig(t *testing.T, dir string) map[string]json.RawMessage {
	t.Helper()
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, ".changeset", "config.json"))), &cfg); err != nil {
		t.Fatalf("config.json isn't JSON: %v", err)
	}
	return cfg
}

func TestInitChangelogGitHub(t *testing.T) {
	dir := initRepo(t, "https://github.com/acme/widgets.git")
	code, out := runChangerig(t, dir, "init", "--source", "changesets", "--changelog", "github")
	assertExitZero(t, code, out)
	assertNotContains(t, out, "tip:")
	got := string(initConfig(t, dir)["changelog"])
	if got != `["@changesets/changelog-github", { "repo": "acme/widgets" }]` {
		t.Errorf("changelog = %s", got)
	}
	// The config it wrote renders a changelog.
	writeChangeset(t, dir, "a", "pkg-a", "minor", "A feature")
	code, out = runChangerig(t, dir, "version", "--changelog")
	assertExitZero(t, code, out)
	assertContains(t, out, "A feature")
}

// Scripted (no terminal), init keeps @changesets' plain layout on a GitHub
// repository, and says how to switch.
func TestInitSuggestsChangelogGitHub(t *testing.T) {
	dir := initRepo(t, "git@github.com:acme/widgets.git")
	code, out := runChangerig(t, dir, "init", "--source", "changesets")
	assertExitZero(t, code, out)
	if _, set := initConfig(t, dir)["changelog"]; set {
		t.Error("a scripted init set a changelog")
	}
	assertContains(t, out, "tip: this repository is on GitHub (acme/widgets)")
	assertContains(t, out, `"changelog": ["@changesets/changelog-github", { "repo": "acme/widgets" }]`)
}

func TestInitChangelogDefaultAndNoRemote(t *testing.T) {
	dir := initRepo(t, "https://github.com/acme/widgets.git")
	code, out := runChangerig(t, dir, "init", "--source", "changesets", "--changelog", "default")
	assertExitZero(t, code, out)
	assertNotContains(t, out, "tip:")
	if _, set := initConfig(t, dir)["changelog"]; set {
		t.Error("--changelog default set a changelog")
	}

	bare := initRepo(t, "")
	code, out = runChangerig(t, bare, "init", "--source", "changesets")
	assertExitZero(t, code, out)
	assertNotContains(t, out, "tip:")
	code, out = runChangerig(t, initRepo(t, ""), "init", "--changelog", "github")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "needs a GitHub remote")
	code, out = runChangerig(t, initRepo(t, ""), "init", "--changelog", "fancy")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "want github or default")
}
