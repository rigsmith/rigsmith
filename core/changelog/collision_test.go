package changelog

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// collisionRepo is a real repository whose history holds two commits sharing
// their first 7 hash characters, each adding its own changeset, beside a base
// commit whose 7 characters are unique.
type collisionRepo struct {
	dir           string
	base          string // full SHA of the base commit
	first, second string // full SHAs of the colliding pair
}

// newCollisionRepo builds the repository. The pair are siblings (same parent,
// one adding .changeset/first.md and the other .changeset/second.md), found by
// a birthday search over their object hashes and written with hash-object; a
// merge puts both in HEAD's history. core.abbrev is set to 12 so that git's
// own default abbreviation is longer than 7 everywhere: only a lookup pinned at
// 7 still gives 7 for the base.
func newCollisionRepo(t *testing.T) collisionRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// Repository-location variables (set inside a git hook, say) would point
	// both the fixture's git and the resolver at another repository.
	for _, v := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES"} {
		if _, set := os.LookupEnv(v); set {
			t.Setenv(v, "")
			os.Unsetenv(v)
		}
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// Each changeset's content is its own: similar contents would let
	// --follow's rename detection take one for a copy of another.
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, ".changeset", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// SHA-1 explicitly: the search below hashes as SHA-1, whatever the
	// machine's default object format.
	run("", "init", "-q", "-b", "main", "--object-format=sha1")
	if f := run("", "rev-parse", "--show-object-format"); f != "sha1" {
		t.Fatalf("fixture: object format %q, want sha1", f)
	}
	run("", "config", "core.abbrev", "12")
	write("base.md", "The base of the history.\n")
	run("", "add", "-A")
	run("", "commit", "-q", "-m", "base")
	base := run("", "rev-parse", "HEAD")

	// The two sides' trees: the base plus one changeset each, then both.
	write("first.md", "One side of the collision.\n")
	run("", "add", "-A")
	firstTree := run("", "write-tree")
	run("", "rm", "-q", "-f", ".changeset/first.md")
	write("second.md", "Zebras, quite unrelated.\n")
	run("", "add", "-A")
	secondTree := run("", "write-tree")
	write("first.md", "One side of the collision.\n")
	run("", "add", "-A")
	bothTree := run("", "write-tree")

	body := func(tree string, n int) string {
		return fmt.Sprintf("tree %s\nparent %s\nauthor T <t@example.com> 1700000000 +0000\ncommitter T <t@example.com> 1700000000 +0000\n\nfix: change %d\n", tree, base, n)
	}
	hash := func(content string) string {
		sum := sha1.Sum([]byte(fmt.Sprintf("commit %d\x00%s", len(content), content)))
		return hex.EncodeToString(sum[:])
	}
	// A collision between a first-side and a second-side candidate: each
	// candidate n alternates sides, and a prefix is only a hit against the
	// other side.
	type candidate struct {
		n    int
		tree string
	}
	seen := map[string]candidate{}
	var one, two candidate
	for n := 0; ; n++ {
		if n > 1<<21 {
			t.Fatal("no 7-character collision in 2^21 tries")
		}
		c := candidate{n, firstTree}
		if n%2 == 1 {
			c.tree = secondTree
		}
		prefix := hash(body(c.tree, c.n))[:7]
		if m, ok := seen[prefix]; ok && m.tree != c.tree {
			one, two = m, c
			break
		}
		seen[prefix] = c
	}
	if one.tree != firstTree {
		one, two = two, one
	}
	first := run(body(one.tree, one.n), "hash-object", "-t", "commit", "-w", "--stdin")
	second := run(body(two.tree, two.n), "hash-object", "-t", "commit", "-w", "--stdin")
	if first[:7] != second[:7] || first == second {
		t.Fatalf("fixture: %s and %s don't share 7 characters", first, second)
	}
	merge := run("", "commit-tree", bothTree, "-p", first, "-p", second, "-m", "Merge the siblings")
	run("", "update-ref", "HEAD", merge)
	run("", "reset", "-q", "--hard")

	// The base's 7 characters name nothing else, and git's own default
	// abbreviation (core.abbrev) is longer: the preconditions for "stays at 7".
	if got := run("", "rev-parse", "--short", base); len(got) <= 7 {
		t.Fatalf("fixture: git's default abbreviation %q is not longer than 7; core.abbrev didn't take", got)
	}
	if got := run("", "rev-parse", "--disambiguate="+base[:7]); got != base {
		t.Fatalf("fixture: %s is ambiguous in the repository:\n%s", base[:7], got)
	}
	return collisionRepo{dir: dir, base: base, first: first, second: second}
}

// Two changesets added by commits sharing 7 characters: each keeps its full
// SHA, and the display forms are git's unique abbreviations, so the two
// changelog lines differ. The base commit, unambiguous, stays at 7 even though
// core.abbrev asks for 12.
func TestResolveTellsCommitsSharingSevenCharactersApart(t *testing.T) {
	repo := newCollisionRepo(t)

	got := Resolve([]string{"first", "second", "base"}, Setting{Kind: KindGit}, repo.dir, execRun)

	for id, sha := range map[string]string{"first": repo.first, "second": repo.second, "base": repo.base} {
		if got[id].Commit != sha {
			t.Errorf("%s: Commit = %q, want the full SHA %q", id, got[id].Commit, sha)
		}
	}
	assertDistinctAbbreviations(t, got["first"], got["second"])
	if b := got["base"]; b.Short != repo.base[:7] {
		t.Errorf("base: Short = %q, want the 7 characters %q", b.Short, repo.base[:7])
	}
}

// The commit-mode resolver, handed the SHAs, abbreviates them the same way.
func TestResolveFromCommitsTellsCommitsSharingSevenCharactersApart(t *testing.T) {
	repo := newCollisionRepo(t)

	got := ResolveFromCommits(map[string]string{"first": repo.first, "second": repo.second, "base": repo.base}, Setting{Kind: KindGit}, repo.dir, execRun)

	for id, sha := range map[string]string{"first": repo.first, "second": repo.second, "base": repo.base} {
		if got[id].Commit != sha {
			t.Errorf("%s: Commit = %q, want the full SHA %q", id, got[id].Commit, sha)
		}
	}
	assertDistinctAbbreviations(t, got["first"], got["second"])
	if b := got["base"]; b.Short != repo.base[:7] {
		t.Errorf("base: Short = %q, want the 7 characters %q", b.Short, repo.base[:7])
	}
}

func assertDistinctAbbreviations(t *testing.T, a, b CommitInfo) {
	t.Helper()
	for _, info := range []CommitInfo{a, b} {
		if len(info.Short) <= 7 || !strings.HasPrefix(info.Commit, info.Short) {
			t.Errorf("%s: Short = %q, want a prefix of it longer than 7", info.Commit, info.Short)
		}
	}
	lineA := RenderLine("A change", Setting{Kind: KindGit}, &a)
	lineB := RenderLine("A change", Setting{Kind: KindGit}, &b)
	if lineA == lineB {
		t.Errorf("both commits render %q", lineA)
	}
	linkA := RenderLine("A change", Setting{Kind: KindGitHub, Repo: "acme/widgets"}, &a)
	if want := "[`" + a.Short + "`](https://github.com/acme/widgets/commit/" + a.Commit + ") - A change"; linkA != want {
		t.Errorf("github line = %q, want %q", linkA, want)
	}
}
