package commitartifact

import (
	"context"
	"errors"
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
			resolve := func(ctx context.Context, path string, base, ours, theirs []byte) ([]byte, error) {
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
			if _, err := repo.mergeWithPolicy(t.Context(), a, b, "declined", func(context.Context, string, []byte, []byte, []byte) ([]byte, error) { return nil, ErrConflict }); !errors.Is(err, ErrConflict) {
				t.Fatal("accepted declined resolution", err)
			}
		})
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
	_, err := repo.mergeWithPolicy(ctx, a, b, "cancel", func(context.Context, string, []byte, []byte, []byte) ([]byte, error) {
		cancel()
		return []byte("result"), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	_, err = repo.mergeWithPolicy(t.Context(), a, b, "large output", func(context.Context, string, []byte, []byte, []byte) ([]byte, error) {
		return make([]byte, conflictByteLimit+1), nil
	})
	if !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("unbounded output", err)
	}
	large := newPublicationCommit(t, repo, parent, "meta.json", strings.Repeat("z", conflictByteLimit+1))
	called := false
	_, err = repo.mergeWithPolicy(t.Context(), a, large, "large input", func(context.Context, string, []byte, []byte, []byte) ([]byte, error) { called = true; return nil, nil })
	if !errors.Is(err, artifact.ErrTooLarge) || called {
		t.Fatal("unbounded input", err, called)
	}
	// Real delete/edit conflicts cannot be treated as a two-sided content merge.
	edited := newPublicationCommit(t, repo, a, "meta.json", "edited")
	deleted := newPublicationCommit(t, repo, a, "other", "other")
	_, err = repo.mergeWithPolicy(t.Context(), edited, deleted, "delete edit", func(context.Context, string, []byte, []byte, []byte) ([]byte, error) { called = true; return nil, nil })
	if !errors.Is(err, ErrConflict) || called {
		t.Fatal("resolved structural conflict", err, called)
	}
}
