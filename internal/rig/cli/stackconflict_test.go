package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// conflictedStackspace builds a stackspace mid-merge in the shape #261
// reported: the fetched branch shares a full stackspace commit as an ancestor,
// deletes everything outside tweed/, and changes tweed/ too. HEAD has changed
// both since the base, so mermaider/ is UD and tweed/ is UU.
func conflictedStackspace(t *testing.T, touchPrefix bool) (*gitrepo.Repo, string) {
	t.Helper()
	root := t.TempDir()
	mustGitStack(t, root, "init", "-q", "-b", "main")
	mustGitStack(t, root, "config", "user.email", "t@t")
	mustGitStack(t, root, "config", "user.name", "t")
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tweed/src/a.cs", "base\n")
	write("mermaider/LICENSE.txt", "base\n")
	write("mermaider/src/x.cs", "base\n")
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "base stackspace commit")

	// "theirs": what a filtered history would look like if it had grown from
	// that base — tweed/ only, with tweed changed.
	mustGitStack(t, root, "checkout", "-qb", "fetched")
	mustGitStack(t, root, "rm", "-rq", "mermaider")
	write("tweed/src/a.cs", "theirs\n")
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "fetched")

	// ours: both prefixes changed since the base.
	mustGitStack(t, root, "checkout", "-q", "main")
	write("mermaider/LICENSE.txt", "ours\n")
	write("mermaider/src/x.cs", "ours\n")
	if touchPrefix {
		write("tweed/src/a.cs", "ours\n")
	}
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "ours")

	// Start the merge; it is expected to stop.
	c := gitCmd(root, "merge", "--no-edit", "-m", "stack: pull tweed @ abc", "fetched")
	_ = c.Run()
	repo, err := gitrepo.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return repo, root
}

func TestStackSettleConflicts(t *testing.T) {
	t.Run("conflicts outside the prefix are settled as ours and the merge is committed", func(t *testing.T) {
		repo, root := conflictedStackspace(t, false)
		// The ancestor is named by its commit, found before the merge is
		// committed — afterwards `merge-base HEAD MERGE_HEAD` would answer
		// with the fetched tip, which is not the commit that explains
		// anything.
		base := strings.TrimSpace(mustGitStack(t, root, "merge-base", "main", "fetched"))
		var out bytes.Buffer
		if err := stackSettleConflicts(context.Background(), &out, repo, "tweed"); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		for _, want := range []string{"2 conflict(s) outside tweed/", "mermaider/ (2)", "has stackspace commit " + short(base) + " as an ancestor"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output missing %q:\n%s", want, out.String())
			}
		}
		for _, f := range []string{"mermaider/LICENSE.txt", "mermaider/src/x.cs"} {
			got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
			// Trimmed: a Windows checkout with autocrlf hands the file back CRLF.
			if err != nil || strings.TrimSpace(string(got)) != "ours" {
				t.Errorf("%s = %q, %v; want ours", f, got, err)
			}
		}
		if got := strings.TrimSpace(mustGitStack(t, root, "show", "HEAD:tweed/src/a.cs")); got != "theirs" {
			t.Errorf("tweed/src/a.cs = %q, want upstream's change taken", got)
		}
		if st := strings.TrimSpace(mustGitStack(t, root, "status", "--porcelain")); st != "" {
			t.Errorf("merge not committed:\n%s", st)
		}
		if parents := strings.Fields(mustGitStack(t, root, "log", "-1", "--format=%P")); len(parents) != 2 {
			t.Errorf("HEAD has %d parents, want a merge commit", len(parents))
		}
	})

	t.Run("conflicts inside the prefix are named, and the merge is left open", func(t *testing.T) {
		repo, root := conflictedStackspace(t, true)
		var out bytes.Buffer
		err := stackSettleConflicts(context.Background(), &out, repo, "tweed")
		if err == nil {
			t.Fatal("expected the prefix's own conflict to be reported")
		}
		for _, want := range []string{"under tweed/ (1 file(s))", "tweed/src/a.cs", "merge --abort"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error missing %q: %v", want, err)
			}
		}
		if strings.Contains(err.Error(), "mermaider") {
			t.Errorf("outside-prefix paths reported as the user's problem: %v", err)
		}
		if left, _ := repo.UnmergedPaths(context.Background()); len(left) != 1 || left[0] != "tweed/src/a.cs" {
			t.Errorf("unmerged after settling = %v, want only the prefix's own file", left)
		}
		if _, err := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); err != nil {
			t.Error("merge was not left open for the user")
		}
	})
}

func TestStackConflictDirs(t *testing.T) {
	got := stackConflictDirs([]string{"mermaider/a", "mermaider/b/c", "live/x", "README.md"})
	if got != "README.md (1), live/ (1), mermaider/ (2)" {
		t.Fatalf("got %q", got)
	}
}

// gitCmd is a git invocation in dir whose failure the caller expects.
func gitCmd(dir string, args ...string) *exec.Cmd {
	c := exec.Command("git", args...)
	c.Dir = dir
	return c
}
