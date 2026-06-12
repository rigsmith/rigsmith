// Ported from net-changesets Shared/SinceChangesTests.cs.
package since

import (
	"path/filepath"
	"testing"

	"github.com/rigsmith/core/plugin"
)

func TestChangedProjectNamesReturnsProjectsOwningChangedFile(t *testing.T) {
	root := filepath.FromSlash("/repo")
	pkgs := []plugin.Package{
		{Name: "pkg-a", Dir: filepath.FromSlash("packages/pkg-a")},
		{Name: "pkg-b", Dir: filepath.FromSlash("packages/pkg-b")},
	}
	changed := []string{
		filepath.FromSlash("/repo/packages/pkg-a/src/main.ts"),
		filepath.FromSlash("/repo/README.md"), // owned by no package
	}
	got := ChangedProjectNames(changed, pkgs, root)
	if len(got) != 1 || got[0] != "pkg-a" {
		t.Errorf("ChangedProjectNames = %v, want [pkg-a]", got)
	}

	// A file in a sibling dir sharing a name prefix is NOT under the package.
	got = ChangedProjectNames([]string{filepath.FromSlash("/repo/packages/pkg-a-extras/x.ts")}, pkgs, root)
	if len(got) != 0 {
		t.Errorf("prefix sibling matched: %v", got)
	}
}

func TestAnyChangesetAddedDetectsChangesetFilesNotReadme(t *testing.T) {
	dir := filepath.FromSlash("/repo/.changeset")
	if AnyChangesetAdded([]string{filepath.FromSlash("/repo/.changeset/README.md")}, dir) {
		t.Error("README.md must not count as a changeset")
	}
	if AnyChangesetAdded([]string{filepath.FromSlash("/repo/.changeset/config.json")}, dir) {
		t.Error("config.json must not count as a changeset")
	}
	if AnyChangesetAdded([]string{filepath.FromSlash("/repo/src/notes.md")}, dir) {
		t.Error("an .md outside the changeset dir must not count")
	}
	if !AnyChangesetAdded([]string{filepath.FromSlash("/repo/.changeset/brave-pandas-smile.md")}, dir) {
		t.Error("a changeset .md must count")
	}
}

func TestChangedChangesetIDs(t *testing.T) {
	dir := filepath.FromSlash("/repo/.changeset")
	ids := ChangedChangesetIDs([]string{
		filepath.FromSlash("/repo/.changeset/brave-pandas-smile.md"),
		filepath.FromSlash("/repo/.changeset/two-foxes-run.md"),
		filepath.FromSlash("/repo/.changeset/README.md"),
		filepath.FromSlash("/repo/.changeset/config.json"),
		filepath.FromSlash("/repo/src/x.ts"),
	}, dir)
	want := map[string]bool{"brave-pandas-smile": true, "two-foxes-run": true}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want 2 entries", ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected id %q", id)
		}
	}
}
