package commitartifact

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

type countGitOutput struct{ bytes int64 }

func (c *countGitOutput) Write(p []byte) (int, error) { c.bytes += int64(len(p)); return len(p), nil }

func TestConflictedMergeOutputIsBoundedBeforeTreeParsing(t *testing.T) {
	repo, parent := seedRepository(t, "sha1")
	var sides []string
	for _, body := range []string{"local\n", "remote\n"} {
		oid := mustRun(t, repo, body, "hash-object", "-w", "--stdin")
		var treeInput strings.Builder
		for i := range 1600 {
			fmt.Fprintf(&treeInput, "100644 blob %s\t%04d-%s%c", oid, i, strings.Repeat("path", 50), 0)
		}
		tree := mustRun(t, repo, treeInput.String(), "mktree", "-z")
		sides = append(sides, mustRun(t, repo, "conflicting side\n", "commit-tree", tree, "-p", parent))
	}
	// Count, rather than retain, diagnostics to prove the fixture exceeds the
	// bound regardless of Git's message wording on each supported platform.
	count := &countGitOutput{}
	if err := repo.runTo(t.Context(), nil, count, "merge-tree", "--write-tree", sides[0], sides[1]); err == nil {
		t.Fatal("fixture did not conflict")
	}
	if count.bytes <= gitOutputLimit {
		t.Fatalf("fixture output %d did not exceed %d", count.bytes, gitOutputLimit)
	}
	sha, err := repo.merge(t.Context(), sides[0], sides[1], "merge")
	if sha != "" || !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatalf("unbounded/partially parsed conflict result: %q %v", sha, err)
	}
}

func TestCapturedGitOutputIsBoundedOnSuccessToo(t *testing.T) {
	repo, _ := seedRepository(t, "sha1")
	oid := mustRun(t, repo, strings.Repeat("x", int(gitOutputLimit)+1), "hash-object", "-w", "--stdin")
	out, err := repo.run(t.Context(), nil, "cat-file", "blob", oid)
	if out != "" || !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatalf("oversized output returned %d bytes: %v", len(out), err)
	}
}

func TestQuietIntegrityChecksStillRejectDamagedDanglingObjects(t *testing.T) {
	repo, _ := seedRepository(t, "sha1")
	oid := mustRun(t, repo, "unreferenced retry object", "hash-object", "-w", "--stdin")
	notices := mustRun(t, repo, "", "fsck", "--strict", "--no-reflogs")
	if !strings.Contains(notices, "dangling blob "+oid) {
		t.Fatal("fixture did not produce dangling diagnostics")
	}
	if err := repo.checkObjects(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo.dir, "objects", oid[:2], oid[2:])
	// Git writes loose objects read-only. Make just this fixture writable before
	// damaging it, including on Windows.
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("damaged object"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.checkObjects(t.Context()); err == nil {
		t.Fatal("suppressed integrity failure with dangling notices")
	}
}
