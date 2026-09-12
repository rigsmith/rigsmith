package allowlist

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The engine's own tests, stated in terms of rules rather than of any vendor's
// layout. The Claude rule set has its own suite next to it in
// internal/clauderig/allowlist, which exercises these same mechanics against a
// real list — both are wanted: this one pins the semantics, that one pins the
// policy.

func TestDefaultDeny(t *testing.T) {
	l := List{}
	if l.Match("anything") {
		t.Error("an empty rule set must include nothing — default-deny is the safety property")
	}
}

func TestLongestMatchWins(t *testing.T) {
	l := List{Rules: []Rule{
		Inc("skills"),
		Exc("skills/private"),
		Inc("skills/private/ok.md"),
	}}
	cases := map[string]bool{
		"skills/a.md":              true,
		"skills/private/x.md":      false,
		"skills/private/ok.md":     true,
		"skills/private/deep/y.md": false,
	}
	for rel, want := range cases {
		if got := l.Match(rel); got != want {
			t.Errorf("Match(%q) = %v, want %v — the most specific rule must win at any depth", rel, got, want)
		}
	}
}

func TestAnyDepthExcludeOutranksALongInclude(t *testing.T) {
	// The case the scoring rule exists for: a short "**/node_modules" has to
	// beat a much longer include it sits inside, or a plugin tree drags a
	// dependency graph into the backup.
	l := List{Rules: []Rule{
		Inc("plugins/marketplaces/some/very/long/path"),
		Exc(AnyDepth + "node_modules"),
	}}
	if l.Match("plugins/marketplaces/some/very/long/path/node_modules/pkg/index.js") {
		t.Error("an any-depth exclude must outrank a longer include that contains it")
	}
	if !l.Match("plugins/marketplaces/some/very/long/path/manifest.json") {
		t.Error("the include must still cover everything the exclude does not carve out")
	}
}

func TestGlobDoesNotCrossASeparator(t *testing.T) {
	l := List{Rules: []Rule{Inc("projects/*/notes.md")}}
	if !l.Match("projects/a/notes.md") {
		t.Error("a glob segment must match one segment")
	}
	if l.Match("projects/a/b/notes.md") {
		t.Error("'*' must not cross a '/' — that is what keeps a rule scoped to one level")
	}
}

func TestWalkPrunesAndSorts(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep/a.txt")
	write("keep/b.txt")
	write("keep/node_modules/huge/index.js")
	write("drop/c.txt")

	files, links, err := Walk(root, List{Rules: []Rule{Inc("keep"), Exc(AnyDepth + "node_modules")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("no symlinks were created, so none should be reported; got %v", links)
	}
	want := []string{"keep/a.txt", "keep/b.txt"}
	if !slices.Equal(files, want) {
		t.Errorf("Walk = %v, want %v (sorted, pruned)", files, want)
	}
}

func TestWalkReportsADirectorySymlinkAsALinkNotAFile(t *testing.T) {
	// The load-bearing case: emitting a directory link as a file makes every
	// consumer read a directory as one, which can only ever fail.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	files, links, err := Walk(root, List{Rules: []Rule{Inc("real"), Inc("link")}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(files, "link") {
		t.Error("a directory symlink must never be returned as a file")
	}
	if len(links) != 1 || links[0].Rel != "link" || links[0].Target != "real" {
		t.Errorf("links = %v, want one link/real entry", links)
	}
}
