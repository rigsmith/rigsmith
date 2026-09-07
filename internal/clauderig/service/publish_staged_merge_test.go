package service_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func stagedPublicationFixture(t *testing.T, kind string) (service.ArtifactPublishRequest, *artifactRemote, string, string) {
	t.Helper()
	input, remote := publicationFixture(t, true, false)
	stage := input.Commit.Capture.Sync.StagingDir
	path := "cli/plugins/data/saved.json"
	git(t, stage, "checkout", "-b", "incoming")
	incoming := "{\"value\":\"incoming\"}\n"
	if kind == "losing-secret" {
		incoming = "{\"token\":\"ghp_" + strings.Repeat("z", 40) + "\"}\n"
	}
	put(t, stage, path, incoming)
	git(t, stage, "add", path)
	git(t, stage, "commit", "-m", "incoming")
	other := git(t, stage, "rev-parse", "HEAD")
	git(t, stage, "checkout", "main")
	put(t, stage, path, "{\"value\":\"local\"}\n")
	git(t, stage, "add", path)
	git(t, stage, "commit", "-m", "local")
	original := git(t, stage, "rev-parse", "HEAD")
	if _, err := remoteGit(t.Context(), stage, "merge", "--no-commit", "incoming"); err == nil {
		t.Fatal("expected an add/add conflict")
	}
	if kind != "unresolved" {
		body := "{\"value\":\"manually resolved\"}\r\n"
		if kind == "secret" {
			body = "{\"token\":\"ghp_" + strings.Repeat("z", 40) + "\"}\n"
		}
		put(t, stage, path, body)
		git(t, stage, "add", path)
	}
	// These bytes must never be staged, discarded or published by recovery.
	put(t, stage, path, "{\"value\":\"later unstaged edit\"}\n")
	put(t, stage, "pending.txt", "untracked pending file\n")
	if kind == "corrupt-artifact" {
		paths, err := filepath.Glob(filepath.Join(input.Commit.Commits.Dir, "*.capture"))
		if err != nil || len(paths) != 1 {
			t.Fatalf("artifact fixture: %v %v", paths, err)
		}
		if err := os.WriteFile(paths[0], []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return input, remote, original, other
}

func TestPublishArtifactFinishesStagedMerge(t *testing.T) {
	for _, kind := range []string{"resolved", "unresolved", "secret", "losing-secret", "corrupt-artifact"} {
		t.Run(kind, func(t *testing.T) {
			input, remote, original, incoming := stagedPublicationFixture(t, kind)
			stage := input.Commit.Capture.Sync.StagingDir
			before := map[string][]byte{}
			for _, path := range []string{".git/index", ".git/config", "cli/plugins/data/saved.json", "pending.txt"} {
				b, err := os.ReadFile(filepath.Join(stage, path))
				if err != nil {
					t.Fatal(err)
				}
				before[path] = b
			}
			result, err := (service.Service{}).PublishArtifact(t.Context(), input)
			for path, want := range before {
				got, readErr := os.ReadFile(filepath.Join(stage, path))
				if readErr != nil || !bytes.Equal(got, want) {
					t.Fatalf("changed %s: %v", path, readErr)
				}
			}
			if kind != "resolved" {
				if err == nil || result != (commitartifact.Publication{}) || remote.fetches != 0 || remote.pushes != 0 || git(t, stage, "rev-parse", "HEAD") != original {
					t.Fatalf("accepted unsafe repair: %+v %v", result, err)
				}
				if strings.Contains(kind, "secret") && !errors.Is(err, engine.ErrSecretTripwire) {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(stage, ".git/MERGE_HEAD")); err != nil {
					t.Fatal("lost pending merge", err)
				}
				return
			}
			if err != nil || remote.pushes != 1 || result.RemoteCommit == "" {
				t.Fatalf("publication: %+v %v", result, err)
			}
			head := git(t, stage, "rev-parse", "HEAD")
			if parents := git(t, stage, "show", "-s", "--format=%P", head); parents != original+" "+incoming {
				t.Fatal("lost merge parents", parents)
			}
			git(t, remote.dir, "merge-base", "--is-ancestor", head, result.RemoteCommit)
			if got := git(t, remote.dir, "show", "main:cli/plugins/data/saved.json"); got != `{"value":"manually resolved"}` {
				t.Fatal("wrong saved resolution", got)
			}
			if settled, err := commitartifact.SettledHead(t.Context(), stage); err != nil || settled != head {
				t.Fatalf("unfinished repair: %s %v", settled, err)
			}
			if _, err := (service.Service{}).PublishArtifact(t.Context(), input); err != nil || remote.pushes != 1 || git(t, stage, "rev-parse", "HEAD") != head {
				t.Fatal("replay changed history", err)
			}
		})
	}
}

func TestQueueAdapterStagedMergeRoundTrip(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("set CLAUDERIG_E2E=1; synthetic staged merge recovery")
	}
	for _, kind := range []string{"resolved", "losing-secret", "offline"} {
		t.Run(kind, func(t *testing.T) {
			input, remote, original, _ := stagedPublicationFixture(t, kind)
			req := input.Commit.Capture
			for _, path := range []string{filepath.Join(req.Sync.Machine.Home, ".claude"), req.Store.Dir} {
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
			}
			transport := &queuedTransport{ArtifactTransport: remote}
			offline := errors.Join(commitartifact.ErrTransport, errors.New("synthetic offline"))
			if kind == "offline" {
				transport.beforeFetch = func() error { return offline }
			}
			q, _, adapter := queueAdapterFixture(t, req, input.Commit.Commits, transport)
			seedQueuePhase(t, q, req, queue.Committed)
			result, err := q.RunOne(t.Context(), time.Now(), adapter)
			if kind == "offline" {
				if !errors.Is(err, offline) || result.Acknowledged || result.Phase != queue.Committed || remote.pushes != 0 {
					t.Fatalf("offline recovery: %+v %v", result, err)
				}
				head, headErr := commitartifact.SettledHead(t.Context(), req.Sync.StagingDir)
				if headErr != nil || head == "" || head == original {
					t.Fatalf("lost completed canonical merge: %s %v", head, headErr)
				}
				pending, snapshotErr := q.Snapshot(t.Context())
				if snapshotErr != nil || len(pending) != 1 || pending[0].Phase != queue.Committed {
					t.Fatalf("lost committed batch: %+v %v", pending, snapshotErr)
				}
				transport.beforeFetch = nil
				result, err = q.RunOne(t.Context(), pending[0].NotBefore, adapter)
				if err != nil || git(t, req.Sync.StagingDir, "rev-parse", "HEAD") != head {
					t.Fatal("retry replaced canonical merge", err)
				}
			}
			if kind == "losing-secret" {
				if !errors.Is(err, engine.ErrSecretTripwire) || result.Acknowledged || result.Phase != queue.Committed || remote.pushes != 0 {
					t.Fatalf("unsafe queue recovery: %+v %v", result, err)
				}
				work, err := q.Snapshot(t.Context())
				if err != nil || len(work) != 1 || work[0].Status != queue.Blocked || work[0].FailureCode != "scan-rejected" {
					t.Fatalf("lost blocked batch: %+v %v", work, err)
				}
				return
			}
			if err != nil || !result.Acknowledged || result.Phase != queue.Pushed || remote.pushes != 1 {
				t.Fatalf("queue recovery: %+v %v", result, err)
			}
			if got := git(t, remote.dir, "show", "main:cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(got, "sealed publication bytes") {
				t.Fatal("lost retained capture", got)
			}
			if _, err := q.RunOne(t.Context(), time.Now(), adapter); !errors.Is(err, queue.ErrEmpty) || remote.pushes != 1 {
				t.Fatal("replayed acknowledged batch", err)
			}
		})
	}
}
