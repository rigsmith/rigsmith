package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// clobberFetchHead puts a git shim first on PATH that runs the real git and
// then, after every fetch, overwrites FETCH_HEAD with sha — the footprint a
// second rig process's fetch leaves in the same repository between one
// process's `git fetch` and whatever it runs next.
func clobberFetchHead(t *testing.T, sha string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shim is a shell script")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"\"" + realGit + "\" \"$@\"\nrc=$?\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = fetch ]; then printf '%s\\t\\tbranch other\\n' \"" + sha + "\" > \"$(\"" + realGit + "\" rev-parse --git-dir)/FETCH_HEAD\"; break; fi\n" +
		"done\nexit $rc\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// mainAndOther gives the remote two branches: main, and other one commit
// ahead of it. Returns their tips.
func mainAndOther(t *testing.T, ctx context.Context, a *Repo) (mainTip, otherTip string) {
	t.Helper()
	mainTip, err := a.Head(ctx)
	must(t, err)
	if _, err := runGit(ctx, a.Dir, "checkout", "-q", "-b", "other"); err != nil {
		t.Fatal(err)
	}
	write(t, a.Dir, "other.txt", "other\n")
	if _, err := a.Commit(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	must(t, a.Push(ctx, "origin", "other"))
	otherTip, err = a.Head(ctx)
	must(t, err)
	if _, err := runGit(ctx, a.Dir, "checkout", "-q", "main"); err != nil {
		t.Fatal(err)
	}
	return mainTip, otherTip
}

func TestFetchRef_NamesThisFetchNotTheLast(t *testing.T) {
	ctx, a, b := twoClones(t)
	mainTip, otherTip := mainAndOther(t, ctx, a)
	clobberFetchHead(t, otherTip)

	got, err := b.FetchRef(ctx, "origin", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != mainTip {
		t.Fatalf("FetchRef returned %s; wanted main's tip %s (other's is %s)", got, mainTip, otherTip)
	}
	if fh, _ := os.ReadFile(filepath.Join(b.Dir, ".git", "FETCH_HEAD")); !strings.HasPrefix(string(fh), otherTip) {
		t.Fatalf("shim did not clobber FETCH_HEAD: %q", fh)
	}
	if refs, _ := runGit(ctx, b.Dir, "for-each-ref", "refs/rig/"); strings.TrimSpace(refs) != "" {
		t.Fatalf("private fetch ref left behind:\n%s", refs)
	}
}

func TestPull_FastForwardsWhatItFetched(t *testing.T) {
	ctx, a, b := twoClones(t)
	write(t, a.Dir, "f.txt", "advanced\n")
	if _, err := a.Commit(ctx, "advance main"); err != nil {
		t.Fatal(err)
	}
	must(t, a.Push(ctx, "origin", "main"))
	mainTip, otherTip := mainAndOther(t, ctx, a)
	clobberFetchHead(t, otherTip)

	if err := b.Pull(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	head, err := b.Head(ctx)
	must(t, err)
	if head != mainTip {
		t.Fatalf("Pull left HEAD at %s; wanted main's tip %s (other's is %s)", head, mainTip, otherTip)
	}
}

func TestFetchRef_ErrorNamesTheBranchNotThePrivateRef(t *testing.T) {
	ctx := context.Background()
	r, _ := Init(ctx, t.TempDir())
	_, err := r.FetchRef(ctx, filepath.Join(t.TempDir(), "missing.git"), "main", nil)
	if err == nil {
		t.Fatal("expected the fetch of a missing remote to fail")
	}
	if !strings.Contains(err.Error(), " main: ") || strings.Contains(err.Error(), "refs/rig/") {
		t.Fatalf("error should read as `git fetch <remote> main: …`, got: %v", err)
	}
}

func TestFetchRef_DropsThePrivateRefWhenTheCallerHasCancelled(t *testing.T) {
	ctx, a, b := twoClones(t)
	// A context that reads as cancelled by the time the deferred cleanup runs
	// but lets the fetch itself through: git gets a fresh process, so what
	// matters is only whether the delete honours the caller's cancellation.
	cctx, cancel := context.WithCancel(ctx)
	tip, err := a.Head(ctx)
	must(t, err)
	go func() {
		// Cancel as soon as the fetch has landed anything, which is before the
		// rev-parse and the deferred delete; a cancelled ctx afterwards is the
		// case under test, an earlier one just fails the fetch.
		for {
			if refs, _ := runGit(ctx, b.Dir, "for-each-ref", "refs/rig/"); strings.TrimSpace(refs) != "" {
				cancel()
				return
			}
			if cctx.Err() != nil {
				return
			}
		}
	}()
	got, ferr := b.FetchRef(cctx, "origin", "main", nil)
	cancel()
	if ferr == nil && got != tip {
		t.Fatalf("FetchRef returned %s, wanted %s", got, tip)
	}
	if refs, _ := runGit(ctx, b.Dir, "for-each-ref", "refs/rig/"); strings.TrimSpace(refs) != "" {
		t.Fatalf("private fetch ref left behind after cancellation:\n%s", refs)
	}
}
