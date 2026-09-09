package commitartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

type confirmationTransport struct {
	Transport
	override func(string) string
}

func (t confirmationTransport) Fetch(ctx context.Context, dir, ref string) (string, error) {
	tip, err := t.Transport.Fetch(ctx, dir, ref)
	if err == nil && t.override != nil {
		tip = t.override(tip)
	}
	return tip, err
}

func TestConfirmSnapshotUsesExactRawCommitInFreshRemoteHistory(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			repo, parent := seedRepository(t, format)
			snapshot := newPublicationCommit(t, repo, parent, "captured", "original\r\n\x00bytes")
			newer := newPublicationCommit(t, repo, snapshot, "captured", "newer current bytes")
			mustRun(t, repo, "", "update-ref", "refs/heads/main", newer)
			transport := &testTransport{repo: repo}
			scratch := t.TempDir()
			verified := 0
			req := SnapshotConfirmation{Commit: snapshot, ScratchParent: scratch, Remote: transport, Verify: func(_ context.Context, root string) error {
				verified++
				data, err := os.ReadFile(filepath.Join(root, "captured"))
				if err != nil {
					return err
				}
				if string(data) != "original\r\n\x00bytes" {
					t.Fatal("verified current tip or converted bytes", string(data))
				}
				return nil
			}}
			if err := ConfirmSnapshot(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			if verified != 1 || transport.fetches != 1 || transport.pushes != 0 {
				t.Fatal("confirmation did not read fresh history")
			}
			mustRun(t, repo, "", "update-ref", "refs/heads/main", parent)
			if err := ConfirmSnapshot(t.Context(), req); err == nil {
				t.Fatal("trusted stale observation after remote rewrite")
			}
			if verified != 1 {
				t.Fatal("verified an unreachable snapshot")
			}
			entries, err := os.ReadDir(scratch)
			if err != nil || len(entries) != 0 {
				t.Fatal("leaked private confirmation", err)
			}
		})
	}
}

func TestConfirmSnapshotRejectsFalseRefsPolicyFailureAndOversize(t *testing.T) {
	repo, parent := seedRepository(t, "sha1")
	transport := &testTransport{repo: repo}
	scratch := t.TempDir()
	reject := errors.New("snapshot mismatch")
	req := SnapshotConfirmation{Commit: parent, ScratchParent: scratch, Remote: transport, Verify: func(context.Context, string) error { return reject }}
	if err := ConfirmSnapshot(t.Context(), req); !errors.Is(err, reject) {
		t.Fatal("ignored evidence rejection", err)
	}
	req.Verify = func(context.Context, string) error { t.Fatal("unexpected verification"); return nil }
	req.Remote = confirmationTransport{Transport: transport, override: func(string) string { return "invalid" }}
	if err := ConfirmSnapshot(t.Context(), req); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted false ref", err)
	}
	req.Remote = transport
	req.MaxTreeBytes = 1
	if err := ConfirmSnapshot(t.Context(), req); !errors.Is(err, artifact.ErrTooLarge) {
		t.Fatal("ignored size bound", err)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatal("leaked failed confirmation", err)
	}
}
