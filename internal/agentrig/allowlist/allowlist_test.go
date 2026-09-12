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

// The any-depth prune is documented as winning over anything the rules say
// about what lives INSIDE the banned tree — and only that case needs it. With
// the rule sets this package ships, a banned directory is also excluded by
// decide(), so the prune loop could be deleted with every allowlist test green.
// The case that separates them is an include pointing into the banned tree.
func TestDescend_AnyDepthPruneBeatsAnIncludeInsideIt(t *testing.T) {
	l := List{Rules: []Rule{
		Inc("projects"),
		Exc(AnyDepth + "node_modules"),
		Inc("projects/p/node_modules/left-pad"), // longer, and would otherwise win
	}}
	if l.Descend("projects/p/node_modules") {
		t.Error("descended a banned tree because an include named something inside it")
	}
	// decide() is deliberately NOT consulted here: by longest-match-wins the
	// deeper include does outrank the ban, so Match on that path is true. The
	// prune is what keeps it from ever being reached, which is exactly why the
	// prune cannot be left to fall through to decide().
	if l.decide("projects/p/node_modules/left-pad/index.js") != Include {
		t.Fatal("fixture no longer poses the problem: decide already excludes this path, so descend could reach the right answer for the wrong reason")
	}
}

// resolveInRoot promises a target that lives INSIDE the root, and is the only
// thing that decides it. Walk's own `decide(target) == Include` happens to
// reject an escaping target too, which is why a Walk-level test for this cannot
// fail when the containment check is removed — it rides on the other gate. So
// the contract is asserted where it is actually made.
func TestResolveInRoot_RefusesATargetOutsideTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "projects", "-main"), 0o755); err != nil {
		t.Fatal(err)
	}
	escaping := filepath.Join(root, "projects", "-main", "memory")
	if err := os.Symlink(filepath.Join(outside, "memory"), escaping); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if got, ok := resolveInRoot(root, escaping); ok {
		t.Errorf("resolveInRoot accepted a target outside the root: %q", got)
	}

	// The control: an ordinary in-root target still resolves, so the refusal
	// above is about containment and not about symlinks in general.
	inside := filepath.Join(root, "projects", "-main", "shared")
	if err := os.MkdirAll(filepath.Join(root, "projects", "-other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "projects", "-other"), inside); err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveInRoot(root, inside); !ok || got != "projects/-other" {
		t.Errorf("resolveInRoot(in-root) = %q %v, want projects/-other true", got, ok)
	}

	// The boundary: a real directory whose NAME starts with "..". Rel returns
	// it unchanged, so a bare HasPrefix(rel, "..") reads it as an escape and
	// silently drops a link that belongs in the backup.
	if err := os.MkdirAll(filepath.Join(root, "..shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	dotted := filepath.Join(root, "projects", "-main", "dotted")
	if err := os.Symlink(filepath.Join(root, "..shared"), dotted); err != nil {
		t.Fatal(err)
	}
	if got, ok := resolveInRoot(root, dotted); !ok || got != "..shared" {
		t.Errorf("resolveInRoot(..shared) = %q %v, want ..shared true", got, ok)
	}
}
