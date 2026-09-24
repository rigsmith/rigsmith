package gitutil

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFileAtRevsAndParents(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	first := git(t, dir, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(dir, "sub", "f.json"), "{\"a\": 1}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "add f")
	second := git(t, dir, "rev-parse", "HEAD")

	// Relative to a subdirectory, as a workspace below the repository root
	// reads it.
	files, err := FileAtRevs(ctx, filepath.Join(dir, "sub"), []string{first, second}, "f.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[first]; ok {
		t.Error("a file absent at a commit was read")
	}
	if string(files[second]) != "{\"a\": 1}\n" {
		t.Errorf("content at %s = %q", second, files[second])
	}

	if _, err := FileAtRevs(ctx, dir, []string{"0123456789012345678901234567890123456789"}, "sub/f.json"); err == nil {
		t.Error("a commit that doesn't exist read as a missing file")
	}

	parents, err := Parents(ctx, dir, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(parents[first]) != 0 || len(parents[second]) != 1 || parents[second][0] != first {
		t.Errorf("parents = %v", parents)
	}
}
