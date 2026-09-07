package commitartifact

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func TestRelatedFilesImmutableSides(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			_, _, r, parent := publicationFixture(t, format)
			commit := func(parent, body, part string) string {
				root := t.TempDir()
				putPublicationFile(t, root, "index", body)
				putPublicationFile(t, root, "parts/shared", part)
				tree, err := r.writeTree(t.Context(), root, root, nil)
				if err != nil {
					t.Fatal(err)
				}
				return mustRun(t, r, "fixture\n", "commit-tree", tree, "-p", parent)
			}
			base := commit(parent, "base", "base part")
			a, b := commit(base, "ours", "our part"), commit(base, "theirs", "their part")
			// Only index conflicts: related side values may legitimately differ, but
			// cannot be used to overwrite another conflict's output.
			calls := 0
			sha, err := r.mergeWithPolicy(t.Context(), a, b, "resolve", func(ctx context.Context, path string, base, ours, theirs []byte, f RelatedFiles) ([]byte, error) {
				calls++
				for side, want := range []string{"base part", "our part", "their part"} {
					got, err := f.Read(ctx, ConflictSide(side), "parts/shared")
					if err != nil || string(got) != want {
						t.Fatalf("side %d: %q %v", side, got, err)
					}
				}
				if err := f.Add(ctx, "parts/new", []byte("new\r\n")); err != nil {
					return nil, err
				}
				return []byte("resolved"), nil
			})
			if err != nil || calls != 2 {
				t.Fatalf("merge: %s %d %v", sha, calls, err)
			}
			got, err := r.run(t.Context(), nil, "show", sha+":parts/new")
			if err != nil || got != "new\r\n" {
				t.Fatalf("raw companion: %q %v", got, err)
			}
		})
	}
}

func TestRelatedFilesRefuseUnsafeOperations(t *testing.T) {
	_, _, r, parent := publicationFixture(t, "sha1")
	a := newPublicationCommit(t, r, parent, "index", "ours")
	b := newPublicationCommit(t, r, parent, "index", "theirs")
	for _, kind := range []string{"replace", "conflict", "ancestor", "descendant", "traversal", "case-conflict", "missing", "invalid-side", "large", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			_, err := r.mergeWithPolicy(t.Context(), a, b, "unsafe", func(ctx context.Context, _ string, _, _, _ []byte, f RelatedFiles) ([]byte, error) {
				switch kind {
				case "replace":
					_ = f.Add(ctx, "history.txt", []byte("replacement"))
				case "conflict":
					_ = f.Add(ctx, "index", nil)
				case "ancestor":
					_ = f.Add(ctx, "directory", nil)
					_ = f.Add(ctx, "directory/file", nil)
				case "descendant":
					_ = f.Add(ctx, "directory/file", nil)
					_ = f.Add(ctx, "directory", nil)
				case "traversal":
					_, _ = f.Read(ctx, OurSide, "../outside")
				case "case-conflict":
					_ = f.Add(ctx, "INDEX", nil)
				case "missing":
					_, _ = f.Read(ctx, OurSide, "missing")
				case "invalid-side":
					_, _ = f.Read(ctx, ConflictSide(9), "index")
				case "large":
					_ = f.Add(ctx, "big", []byte(strings.Repeat("x", relatedByteLimit+1)))
				case "cancel":
					canceled, cancel := context.WithCancel(ctx)
					cancel()
					_ = f.Add(canceled, "new", nil)
				}
				// Even deliberately ignored errors must prevent a merge commit.
				return []byte("resolved"), nil
			})
			if err == nil {
				t.Fatal("unsafe operation ignored")
			}
			if kind == "large" && !errors.Is(err, artifact.ErrTooLarge) {
				t.Fatal(err)
			}
		})
	}
}

func TestRelatedFilesReadBoundsAndModes(t *testing.T) {
	_, _, r, parent := publicationFixture(t, "sha1")
	large := newPublicationCommit(t, r, parent, "large", strings.Repeat("x", relatedByteLimit+1))
	f := newRelatedFiles(r, large, large, large, nil)
	f.calls = 256
	if _, err := f.Read(t.Context(), OurSide, "large"); !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("ignored operation budget", err)
	}
	f = newRelatedFiles(r, large, large, large, nil)
	if _, err := f.Read(t.Context(), OurSide, "large"); !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("unbounded side read", err)
	}
	small := newPublicationCommit(t, r, parent, "small", "123456789")
	f = newRelatedFiles(r, small, small, small, nil)
	f.budget = 8
	if _, err := f.Read(t.Context(), OurSide, "small"); !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("ignored remaining budget", err)
	}
	oid := mustRun(t, r, "outside", "hash-object", "-w", "--stdin")
	tree := mustRun(t, r, "120000 blob "+oid+"\tlink\n", "mktree")
	f = newRelatedFiles(r, tree, tree, tree, nil)
	if _, err := f.Read(t.Context(), OurSide, "link"); !errors.Is(err, ErrConflict) {
		t.Fatal("read symbolic link", err)
	}
	f = newRelatedFiles(r, tree, tree, tree, nil)
	if err := f.Add(t.Context(), "link/child", nil); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced symbolic-link ancestor", err)
	}
}

func TestRelatedFilesRejectVirtualBase(t *testing.T) {
	_, _, r, parent := publicationFixture(t, "sha1")
	a := newPublicationCommit(t, r, parent, "index", "shared")
	b := newPublicationCommit(t, r, parent, "index", "shared")
	// Give the otherwise identical sibling an independent identity.
	tree := mustRun(t, r, "", "rev-parse", b+"^{tree}")
	b = mustRun(t, r, "other sibling\n", "commit-tree", tree, "-p", parent)
	makeMerge := func(body string) string {
		tip := newPublicationCommit(t, r, a, "index", body)
		tree := mustRun(t, r, "", "rev-parse", tip+"^{tree}")
		return mustRun(t, r, "criss-cross\n", "commit-tree", tree, "-p", a, "-p", b)
	}
	ours, theirs := makeMerge("ours"), makeMerge("theirs")
	_, err := r.mergeWithPolicy(t.Context(), ours, theirs, "resolve", func(ctx context.Context, _ string, _, _, _ []byte, f RelatedFiles) ([]byte, error) {
		_, err := f.Read(ctx, BaseSide, "history.txt")
		return []byte("resolved"), err
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("accepted ambiguous companion base", err)
	}
}
