package mergepolicy

import (
	"bytes"
	"context"
	"github.com/rigsmith/rigsmith/internal/agentrig/backupgit"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/rolloutstore"
)

const relRollout = "cli/sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl"

// turns builds n identical records after a header, then `extra` — the turns
// this side alone appended. Two sides with the same n and different extras
// have genuinely diverged; two whose extras nest have not.
func turns(n int, extra ...string) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"session_meta","payload":{"session_id":"01a0722a","cwd":"/x"}}` + "\n")
	filler := strings.Repeat("y", 900)
	for i := 0; i < n; i++ {
		b.WriteString(`{"type":"response_item","payload":{"text":"` + filler + `"}}` + "\n")
	}
	for _, e := range extra {
		b.WriteString(`{"type":"response_item","payload":{"text":"` + e + `"}}` + "\n")
	}
	return []byte(b.String())
}

func writeChunked(t *testing.T, dir string, body []byte) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(relRollout))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rolloutstore.Write(p, bytes.NewReader(body), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, ctx context.Context, repo *gitrepo.Repo, msg string) {
	t.Helper()
	// The attributes the product writes before it commits anything: without
	// them a runner with core.autocrlf refuses the LF rollout outright.
	if err := backupgit.Ensure(repo.Dir, "CodexRig"); err != nil {
		t.Fatal(err)
	}
	if err := repo.StageAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, msg); err != nil {
		t.Fatal(err)
	}
}

// two machines synced the same session; one has more of it. Set up so the
// conflict is on the chunk INDEX, which is never a byte-prefix of the other
// side even when one conversation is exactly the other plus a turn.
func divergedRepo(t *testing.T, oursExtra, theirsExtra []string) (*gitrepo.Repo, []byte, []byte) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	repo, err := gitrepo.Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	writeChunked(t, dir, turns(10000)) // ~9 MB: past the threshold
	commitAll(t, ctx, repo, "base")
	if err := repo.Checkout(ctx, "theirs", true); err != nil {
		t.Fatal(err)
	}
	theirs := turns(10000, theirsExtra...)
	writeChunked(t, dir, theirs)
	commitAll(t, ctx, repo, "theirs")
	if err := repo.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	ours := turns(10000, oursExtra...)
	writeChunked(t, dir, ours)
	commitAll(t, ctx, repo, "ours")
	conflicted, err := repo.MergeRef(ctx, "theirs")
	if err != nil {
		t.Fatal(err)
	}
	if !conflicted {
		t.Fatal("fixture no longer conflicts, so nothing here is being resolved")
	}
	return repo, ours, theirs
}

func TestAChunkedRolloutThatOneSideExtendedAutoMerges(t *testing.T) {
	ctx := context.Background()
	repo, _, theirs := divergedRepo(t, []string{"a"}, []string{"a", "b", "c"}) // theirs is ours plus two turns
	rep, err := Resolve(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unresolved) != 0 {
		t.Fatalf("left for a human: %v — the indexes were compared as bytes, not as conversations", rep.Unresolved)
	}
	// And what was kept is the LONGER conversation, rebuilt from its parts.
	raw, err := os.ReadFile(filepath.Join(repo.Dir, filepath.FromSlash(relRollout)))
	if err != nil {
		t.Fatal(err)
	}
	if !rolloutstore.IsIndex(raw) {
		t.Fatal("the resolution wrote plain bytes over a chunked rollout")
	}
	f, err := rolloutstore.Open(filepath.Join(repo.Dir, filepath.FromSlash(relRollout)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got bytes.Buffer
	if _, err := got.ReadFrom(f); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), theirs) {
		t.Errorf("kept %d bytes, want the longer side's %d", got.Len(), len(theirs))
	}
}

// Two conversations that genuinely diverged still go to a person: unioning tool
// histories produces a transcript that never happened.
func TestChunkedRolloutsThatDivergedStayUnresolved(t *testing.T) {
	ctx := context.Background()
	repo, _, _ := divergedRepo(t, []string{"a", "b"}, []string{"a", "x"}) // neither is a prefix of the other
	rep, err := Resolve(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	// The index AND its parts are left alone: a rollout that is a human's
	// problem does not get its parts silently pruned underneath it.
	var index bool
	for _, u := range rep.Unresolved {
		if u == relRollout {
			index = true
		}
	}
	if !index {
		t.Fatalf("unresolved = %v, want the rollout left for a human", rep.Unresolved)
	}
	for _, r := range rep.Resolved {
		if r.Policy == PolicyAppend || r.Policy == PolicyDrop {
			t.Errorf("a diverged rollout had %s applied to %s", r.Policy, r.Path)
		}
	}
}

// When the longer side is a PLAIN file, no index references any part, so every
// conflicted part belongs to the side that lost. Leaving them unresolved made
// Reconcile abort a valid append-only merge.
func TestAPlainWinnerDropsTheLosersParts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo, err := gitrepo.Init(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	writeChunked(t, dir, turns(10000))
	commitAll(t, ctx, repo, "base")
	if err := repo.Checkout(ctx, "theirs", true); err != nil {
		t.Fatal(err)
	}
	writeChunked(t, dir, turns(10000, "a"))
	commitAll(t, ctx, repo, "theirs")
	if err := repo.Checkout(ctx, "main", false); err != nil {
		t.Fatal(err)
	}
	// Ours has more of the session and is stored whole (a machine with
	// chunking off), sidecar removed as Convert would.
	p := filepath.Join(dir, filepath.FromSlash(relRollout))
	if err := os.RemoveAll(p + rolloutstore.Suffix); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, turns(10000, "a", "b"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, ctx, repo, "ours plain")
	if conflicted, err := repo.MergeRef(ctx, "theirs"); err != nil || !conflicted {
		t.Fatalf("fixture: conflicted=%v err=%v", conflicted, err)
	}
	rep, err := Resolve(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unresolved) != 0 {
		t.Fatalf("left for a human: %v", rep.Unresolved)
	}
	if _, err := os.Lstat(p + rolloutstore.Suffix); !os.IsNotExist(err) {
		t.Error("the loser's parts survived beside a plain winner")
	}
}
