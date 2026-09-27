package cmdtest

import (
	"os"
	"path/filepath"
	"testing"
)

// changelog-github's summary overrides and template reach the written
// changelog: the author: line comes out of the text and is thanked, #5 links
// to its issue, and the template's own "- " bullet isn't doubled. The
// workspace isn't a git repository, so nothing is looked up and gh never runs.
func TestVersionChangelogGitHubTemplateAndOverrides(t *testing.T) {
	dir := newWorkspace(t)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "changelog": ["@changesets/changelog-github", { "repo": "acme/widgets", "template": "\n\n- {summary} (thanks {authors}!)" }] }`)
	writeChangeset(t, dir, "fix", "pkg-a", "patch", "author: @octocat\nFixes #5")

	code, out := runChangerig(t, dir, "version")
	assertExitZero(t, code, out)

	b, err := os.ReadFile(filepath.Join(dir, "packages", "pkg-a", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(b), "\n- Fixes [#5](https://github.com/acme/widgets/issues/5) (thanks [@octocat](https://github.com/octocat)!)\n")
	assertNotContains(t, string(b), "author:")
}

// A template token changelog-github doesn't have fails the run before
// anything is written, as it does there, instead of reaching a changelog.
func TestVersionChangelogGitHubRejectsAnUnknownTemplateToken(t *testing.T) {
	dir := newWorkspace(t)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "changelog": ["@changesets/changelog-github", { "repo": "acme/widgets", "template": "{summary} {author}" }] }`)
	writeChangeset(t, dir, "fix", "pkg-a", "patch", "A fix")

	code, out := runChangerig(t, dir, "version")
	assertExitNonZero(t, code, out)
	assertContains(t, out, `unknown changelog template token "{author}"`)
	if _, err := os.Stat(filepath.Join(dir, "packages", "pkg-a", "CHANGELOG.md")); err == nil {
		t.Error("CHANGELOG.md was written despite the bad template")
	}
}
