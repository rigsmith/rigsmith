package commitartifact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func snapshotCommit(t *testing.T, r gitRepo, parents []string, files map[string]string, sec int64) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range files {
		putPublicationFile(t, root, path, body)
	}
	tree, err := r.writeTree(t.Context(), root, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var identity []string
	for _, value := range r.identity {
		if !strings.HasPrefix(value, "GIT_AUTHOR_DATE=") && !strings.HasPrefix(value, "GIT_COMMITTER_DATE=") {
			identity = append(identity, value)
		}
	}
	r.identity = append(identity, fmt.Sprintf("GIT_AUTHOR_DATE=@%d +0000", sec), fmt.Sprintf("GIT_COMMITTER_DATE=@%d +0000", sec))
	args := []string{"commit-tree", tree}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	return mustRun(t, r, "snapshot\n", args...)
}
func snapshotFiles(t *testing.T, r gitRepo, a, b, path string) *relatedFiles {
	t.Helper()
	mode, ao, err := r.relatedEntry(t.Context(), a, path)
	if err != nil {
		t.Fatal(err)
	}
	_, bo, err := r.relatedEntry(t.Context(), b, path)
	if err != nil {
		t.Fatal(err)
	}
	f := newRelatedFiles(r, a, a, b, nil)
	f.owner = conflictStages{path: path, mode: mode, oids: [3]string{"", ao, bo}}
	return f
}

func TestSnapshotOriginPreservesFileOrdering(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			_, _, r, parent := publicationFixture(t, format)
			makeCommit := func(parents []string, value, other string, sec int64) string {
				return snapshotCommit(t, r, parents, map[string]string{"config": value, "unrelated": other}, sec)
			}
			base := makeCommit([]string{parent}, "base", "base", 100)
			a := makeCommit([]string{base}, "ours", "base", 200)
			b := makeCommit([]string{base}, "theirs", "base", 300)
			// Later unrelated commits and synthetic publication times are not new settings.
			untouched := makeCommit([]string{a}, "ours", "changed", 9000)
			for _, mergeTime := range []int64{1, 10000} {
				merged := makeCommit([]string{untouched, b}, "ours", "changed", mergeTime)
				f := snapshotFiles(t, r, merged, b, "config")
				for side, want := range map[ConflictSide]int64{OurSide: 200, TheirSide: 300} {
					got, err := f.SnapshotTime(t.Context(), side)
					if err != nil || got.Unix() != want {
						t.Fatalf("side %d origin=%v want=%d err=%v", side, got, want, err)
					}
				}
			}
			// Identical independent snapshots retain the latest known source time.
			same := makeCommit([]string{base}, "ours", "other branch", 400)
			merged := makeCommit([]string{a, same}, "ours", "joined", 1)
			f := snapshotFiles(t, r, merged, b, "config")
			got, err := f.SnapshotTime(t.Context(), OurSide)
			if err != nil || got.Unix() != 400 {
				t.Fatalf("identical origins: %v %v", got, err)
			}
		})
	}
}

func TestSnapshotOriginRefusesUnknownAndBounds(t *testing.T) {
	_, _, r, parent := publicationFixture(t, "sha1")
	base := snapshotCommit(t, r, []string{parent}, map[string]string{"config": "base"}, 100)
	a := snapshotCommit(t, r, []string{base}, map[string]string{"config": "ours"}, 200)
	b := snapshotCommit(t, r, []string{base}, map[string]string{"config": "theirs"}, 300)
	novel := snapshotCommit(t, r, []string{a, b}, map[string]string{"config": "combined"}, 400)
	f := snapshotFiles(t, r, novel, b, "config")
	if _, err := f.SnapshotTime(t.Context(), OurSide); !errors.Is(err, ErrConflict) {
		t.Fatal("invented origin for merged bytes", err)
	}
	if _, err := f.Read(t.Context(), TheirSide, "config"); !errors.Is(err, ErrConflict) {
		t.Fatal("ignored origin error", err)
	}
	f = snapshotFiles(t, r, a, b, "config")
	f.originVisits = snapshotVisitLimit
	if _, err := f.SnapshotTime(t.Context(), OurSide); !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("ignored history budget", err)
	}
	f = snapshotFiles(t, r, a, b, "config")
	f.owner.oids[1] = f.owner.oids[2]
	if _, err := f.SnapshotTime(t.Context(), OurSide); !errors.Is(err, ErrConflict) {
		t.Fatal("accepted different owner bytes", err)
	}
	f = snapshotFiles(t, r, a, b, "config")
	if _, err := f.SnapshotTime(t.Context(), BaseSide); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted unsupported side", err)
	}
	f = snapshotFiles(t, r, a, b, "config")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.SnapshotTime(ctx, OurSide); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	// A real timestamp at Unix epoch is valid, unlike time.Time's unknown zero.
	zero := snapshotCommit(t, r, nil, map[string]string{"config": "epoch"}, 0)
	f = snapshotFiles(t, r, zero, b, "config")
	if got, err := f.SnapshotTime(t.Context(), OurSide); err != nil || !got.Equal(time.Unix(0, 0)) {
		t.Fatal("rejected epoch", got, err)
	}
}
