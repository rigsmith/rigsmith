package commitartifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupHistoryAcceptsRelatedTipsAndObservesRewrites(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			repo, seed := seedRepository(t, format)
			local := newPublicationCommit(t, repo, seed, "local", "local bytes")
			remote := newPublicationCommit(t, repo, seed, "remote", "remote bytes")
			transport := &testTransport{repo: repo}
			scratch := t.TempDir()
			req := StartupHistoryRequest{LocalDir: repo.dir, LocalCommit: local, ScratchParent: scratch, Remote: transport}
			for _, tc := range []struct{ tip, base string }{{local, local}, {seed, seed}, {remote, seed}, {newPublicationCommit(t, repo, local, "remote", "later"), local}} {
				mustRun(t, repo, "", "update-ref", "refs/heads/main", tc.tip)
				before := mustRun(t, repo, "", "show-ref")
				got, err := CheckStartupHistory(t.Context(), req)
				if err != nil || got != (StartupHistory{local, tc.tip, tc.base}) {
					t.Fatal(got, err)
				}
				if after := mustRun(t, repo, "", "show-ref"); after != before {
					t.Fatal("startup changed canonical refs")
				}
			}
			// A fresh observation must reject a remote rewritten to unrelated history.
			tree := mustRun(t, repo, "", "rev-parse", seed+"^{tree}")
			unrelated := mustRun(t, repo, "new root\n", "commit-tree", tree)
			mustRun(t, repo, "", "update-ref", "refs/heads/main", unrelated)
			if got, err := CheckStartupHistory(t.Context(), req); !errors.Is(err, ErrSharedHistory) || got != (StartupHistory{}) {
				t.Fatal("accepted unrelated history", got, err)
			}
			if transport.fetches != 5 || transport.pushes != 0 {
				t.Fatal("startup must freshly fetch without pushing", transport)
			}
			entries, err := os.ReadDir(scratch)
			if err != nil || len(entries) != 0 {
				t.Fatal("leaked startup scratch", entries, err)
			}
		})
	}
}

func TestStartupHistoryRefusesMissingUntrustedAndIncompleteHistory(t *testing.T) {
	for _, mode := range []string{"empty-local", "missing-local", "empty-remote", "offline", "false-ref", "shallow-local", "shallow-remote", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			repo, seed := seedRepository(t, "sha1")
			transport := &testTransport{repo: repo}
			req := StartupHistoryRequest{LocalDir: repo.dir, LocalCommit: seed, ScratchParent: t.TempDir(), Remote: transport}
			ctx := t.Context()
			var want error
			switch mode {
			case "empty-local":
				req.LocalCommit, want = "", ErrSharedHistory
			case "missing-local":
				req.LocalCommit = strings.Repeat("f", 40)
			case "empty-remote":
				mustRun(t, repo, "", "update-ref", "-d", "refs/heads/main")
				want = ErrSharedHistory
			case "offline":
				transport.fetchErrorAt = 1
			case "false-ref":
				req.Remote = confirmationTransport{Transport: transport, override: func(string) string { return strings.Repeat("a", 40) }}
				want = ErrInvalid
			case "shallow-local":
				if err := os.WriteFile(filepath.Join(repo.dir, "shallow"), []byte(seed+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				want = ErrInvalid
			case "shallow-remote":
				req.Remote = startupShallowTransport{Transport: transport}
				want = ErrInvalid
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			if got, err := CheckStartupHistory(ctx, req); err == nil || (want != nil && !errors.Is(err, want)) || got != (StartupHistory{}) {
				t.Fatal("invalid startup accepted", got, err)
			}
			if transport.pushes != 0 {
				t.Fatal("startup pushed")
			}
			if (mode == "empty-local" || mode == "missing-local" || mode == "shallow-local" || mode == "canceled") && transport.fetches != 0 {
				t.Fatal("invalid local input reached remote")
			}
			entries, err := os.ReadDir(req.ScratchParent)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed startup leaked scratch", entries, err)
			}
		})
	}
}

type startupShallowTransport struct{ Transport }

func (s startupShallowTransport) Fetch(ctx context.Context, dir, ref string) (string, error) {
	tip, err := s.Transport.Fetch(ctx, dir, ref)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "shallow"), []byte(tip+"\n"), 0600)
	}
	return tip, err
}

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
