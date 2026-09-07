package mergepolicy

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
)

type snapshotFixture struct {
	times [3]time.Time
	calls int
	err   error
}

func (f *snapshotFixture) Read(context.Context, commitartifact.ConflictSide, string) ([]byte, error) {
	return nil, errors.New("unexpected related read")
}
func (f *snapshotFixture) Add(context.Context, string, []byte) error {
	return errors.New("unexpected related addition")
}
func (f *snapshotFixture) SnapshotTime(ctx context.Context, side commitartifact.ConflictSide) (time.Time, error) {
	f.calls++
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	return f.times[side], f.err
}

func TestRetainedSnapshotSelection(t *testing.T) {
	for _, kind := range []string{"ours", "theirs", "equal", "unknown", "lookup-failed", "binary", "identical", "missing", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := &snapshotFixture{times: [3]time.Time{{}, time.Unix(200, 0), time.Unix(300, 0)}}
			ours, theirs := []byte("our bytes\r\n"), []byte("their bytes\n")
			switch kind {
			case "ours":
				f.times[1] = time.Unix(400, 0)
			case "equal":
				f.times[1] = f.times[2]
			case "unknown":
				f.times[1] = time.Time{}
			case "lookup-failed":
				f.err = errors.New("lookup failed")
			case "binary":
				theirs = []byte{0, 255, 13, 10}
			case "identical":
				theirs = bytes.Clone(ours)
			case "missing":
				ours = nil
			}
			ctx := t.Context()
			if kind == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := ResolveRetainedFiles(ctx, "cli/plugins/data/state.bin", nil, ours, theirs, f)
			switch kind {
			case "equal", "unknown", "lookup-failed", "missing", "cancel":
				if err == nil {
					t.Fatal("accepted ambiguous snapshot")
				}
				return
			}
			want := theirs
			if kind == "ours" {
				want = ours
			}
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("wrong selection: %q %v", got, err)
			}
			got[0] ^= 1
			if bytes.Equal(got, want) {
				t.Fatal("returned mutable input alias")
			}
			if kind == "identical" && f.calls != 0 {
				t.Fatal("queried irrelevant ordering")
			}
		})
	}
}

func TestRetainedSnapshotDoesNotFallbackFromOtherPolicies(t *testing.T) {
	for _, path := range []string{"cli/projects/p/s.jsonl", "cli/projects/p/memory/MEMORY.md", "cli/projects/p/memory/state.json", "cli/history.jsonl", "cli/plugins/cache/state.json", "custom/settings.json", ".gitattributes", "cli/skills/example/.gitattributes", "clauderig-storage.json", "clauderig-manifest.json", "cli/projects/p/s.jsonl.chunks/part.part"} {
		t.Run(path, func(t *testing.T) {
			f := &snapshotFixture{times: [3]time.Time{{}, time.Unix(200, 0), time.Unix(300, 0)}}
			_, err := ResolveRetainedFiles(t.Context(), path, []byte("base\n"), []byte("edited ours\n"), []byte("edited theirs\n"), f)
			if err == nil || f.calls != 0 {
				t.Fatalf("fallback for protected path: %v calls=%d", err, f.calls)
			}
		})
	}
}
