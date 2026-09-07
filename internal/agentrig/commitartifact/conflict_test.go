package commitartifact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func TestMergeWithPolicyRawBlobs(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			_, _, repo, parent := publicationFixture(t, format)
			a := newPublicationCommit(t, repo, parent, "meta.json", "ours")
			b := newPublicationCommit(t, repo, parent, "meta.json", "theirs")
			calls := 0
			resolve := func(ctx context.Context, path string, base, ours, theirs []byte, _ RelatedFiles) ([]byte, error) {
				calls++
				if path != "meta.json" || base != nil || string(ours) != "ours" || string(theirs) != "theirs" {
					t.Fatalf("wrong sides: %q %q %q %q", path, base, ours, theirs)
				}
				return []byte("both\r\n"), nil
			}
			sha, err := repo.mergeWithPolicy(t.Context(), a, b, "resolved", resolve)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("resolver not used", calls)
			}
			got, err := repo.run(t.Context(), nil, "show", sha+":meta.json")
			if err != nil || got != "both\r\n" {
				t.Fatalf("raw output: %q %v", got, err)
			}
			again, err := repo.mergeWithPolicy(t.Context(), a, b, "resolved", resolve)
			if err != nil || again != sha {
				t.Fatal("nondeterministic recovery", again, err)
			}
			parents := mustRun(t, repo, "", "show", "-s", "--format=%P", sha)
			if parents != a+" "+b {
				t.Fatal("lost parents", parents)
			}
			if _, err := repo.mergeWithPolicy(t.Context(), a, b, "declined", func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
				return nil, ErrConflict
			}); !errors.Is(err, ErrConflict) {
				t.Fatal("accepted declined resolution", err)
			}
		})
	}
}

func TestRetainedMergeIgnoresAttributeDrivers(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, policy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/policy=%v", format, policy), func(t *testing.T) {
				_, _, repo, parent := publicationFixture(t, format)
				commit := func(parent, body string) string {
					root := t.TempDir()
					putPublicationFile(t, root, ".gitattributes", "[attr]automatic merge=union\n* automatic\n")
					putPublicationFile(t, root, "nested/.gitattributes", "* merge=union\n")
					putPublicationFile(t, root, "nested/meta.json", body)
					tree, err := repo.writeTree(t.Context(), root, root, nil)
					if err != nil {
						t.Fatal(err)
					}
					return mustRun(t, repo, "attribute fixture\n", "commit-tree", tree, "-p", parent)
				}
				base := commit(parent, "base\n")
				a, b := commit(base, "ours\n"), commit(base, "theirs\n")
				// Select the attribute source explicitly so this regression does
				// not depend on the bare repository default. The override must win.
				mustRun(t, repo, "", "read-tree", a)
				mustRun(t, repo, "", "config", "attr.tree", a)
				// Demonstrate that Git alone reports the hostile union as clean.
				unionTree := mustRun(t, repo, "", "merge-tree", "--write-tree", a, b)
				if got := mustRun(t, repo, "", "show", unionTree+":nested/meta.json"); got != "ours\ntheirs" {
					t.Fatalf("fixture did not activate union driver: %q", got)
				}
				var resolve ResolveConflict
				calls := 0
				if policy {
					resolve = func(_ context.Context, path string, base, ours, theirs []byte, _ RelatedFiles) ([]byte, error) {
						calls++
						if path != "nested/meta.json" || string(base) != "base\n" || string(ours) != "ours\n" || string(theirs) != "theirs\n" {
							t.Fatalf("wrong conflict: %s %q %q %q", path, base, ours, theirs)
						}
						return nil, ErrConflict
					}
				}
				if _, err := repo.mergeWithPolicy(t.Context(), a, b, "decline union", resolve); !errors.Is(err, ErrConflict) {
					t.Fatalf("attribute driver bypassed conflict refusal: %v", err)
				}
				if policy && calls != 1 {
					t.Fatal("resolver bypassed", calls)
				}
				// Ordinary non-overlapping text edits still merge automatically.
				base = commit(parent, "one\ntwo\nthree\nfour\nfive\n")
				a = commit(base, "ours\ntwo\nthree\nfour\nfive\n")
				b = commit(base, "one\ntwo\nthree\nfour\ntheirs\n")
				sha, err := repo.mergeWithPolicy(t.Context(), a, b, "clean text", resolve)
				if err != nil {
					t.Fatal(err)
				}
				if got := mustRun(t, repo, "", "show", sha+":nested/meta.json"); got != "ours\ntwo\nthree\nfour\ntheirs" {
					t.Fatalf("lost non-overlapping edits: %q", got)
				}
			})
		}
	}
}

func TestParseConflictsRejectsUnsupportedRecords(t *testing.T) {
	oid := strings.Repeat("a", 40)
	row := func(mode, stage, path string) string { return mode + " " + oid + " " + stage + "\t" + path + "\x00" }
	for _, records := range []string{
		"", row("100644", "2", "file"), row("120000", "2", "file") + row("120000", "3", "file"),
		row("100644", "2", "file") + row("100755", "3", "file"),
		row("100644", "2", "../file") + row("100644", "3", "../file"),
		row("100644", "2", "file") + row("100644", "2", "file") + row("100644", "3", "file"),
		row("100644", "2", "file") + row("100644", "3", "file") + "\x00message\x00",
	} {
		if _, _, err := parseConflicts(oid + "\x00" + records); err == nil {
			t.Fatal("accepted unsupported conflict", records)
		}
	}
}

func TestMergeWithPolicyBoundsAndStructuralRefusal(t *testing.T) {
	_, _, repo, parent := publicationFixture(t, "sha1")
	a := newPublicationCommit(t, repo, parent, "meta.json", "ours")
	b := newPublicationCommit(t, repo, parent, "meta.json", "theirs")
	ctx, cancel := context.WithCancel(t.Context())
	_, err := repo.mergeWithPolicy(ctx, a, b, "cancel", func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
		cancel()
		return []byte("result"), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	_, err = repo.mergeWithPolicy(t.Context(), a, b, "large output", func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
		return make([]byte, conflictByteLimit+1), nil
	})
	if !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("unbounded output", err)
	}
	large := newPublicationCommit(t, repo, parent, "meta.json", strings.Repeat("z", conflictByteLimit+1))
	called := false
	_, err = repo.mergeWithPolicy(t.Context(), a, large, "large input", func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
		called = true
		return nil, nil
	})
	if !errors.Is(err, artifact.ErrTooLarge) || called {
		t.Fatal("unbounded input", err, called)
	}
	// Real delete/edit conflicts cannot be treated as a two-sided content merge.
	edited := newPublicationCommit(t, repo, a, "meta.json", "edited")
	deleted := newPublicationCommit(t, repo, a, "other", "other")
	_, err = repo.mergeWithPolicy(t.Context(), edited, deleted, "delete edit", func(context.Context, string, []byte, []byte, []byte, RelatedFiles) ([]byte, error) {
		called = true
		return nil, nil
	})
	if !errors.Is(err, ErrConflict) || called {
		t.Fatal("resolved structural conflict", err, called)
	}
}
