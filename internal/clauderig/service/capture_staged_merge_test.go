package service_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

// Reuse the real merge fixture, but give fresh queued work its own empty stores.
func stagedCaptureFixture(t *testing.T, kind string) (service.ArtifactCaptureRequest, *artifactRemote, string, string) {
	t.Helper()
	input, remote, original, incoming := stagedPublicationFixture(t, kind)
	req := input.Commit.Capture
	req.Store = artifact.Store{Dir: filepath.Join(t.TempDir(), "captures")}
	req.Work.Phase, req.Work.CaptureRef, req.Work.CommitRef = queue.Queued, "", ""
	return req, remote, original, incoming
}

func TestCaptureArtifactFinishesStagedMergeBeforeRetainingSeed(t *testing.T) {
	for _, kind := range []string{"resolved", "unresolved", "secret", "losing-secret", "binding"} {
		t.Run(kind, func(t *testing.T) {
			req, _, original, incoming := stagedCaptureFixture(t, kind)
			stage := req.Sync.StagingDir
			before := map[string][]byte{}
			for _, path := range []string{".git/index", ".git/config", "cli/plugins/data/saved.json", "pending.txt", ".git/HEAD", ".git/MERGE_HEAD", ".git/ORIG_HEAD"} {
				data, err := os.ReadFile(filepath.Join(stage, path))
				if err != nil {
					t.Fatal(err)
				}
				before[path] = data
			}
			if kind == "binding" {
				req.Sync.Config.Remote = "changed-destination"
			}
			ref, err := (service.Service{}).CaptureArtifact(t.Context(), req)
			for path, want := range before {
				if kind == "resolved" && (path == ".git/HEAD" || path == ".git/MERGE_HEAD") {
					continue
				}
				got, readErr := os.ReadFile(filepath.Join(stage, path))
				if readErr != nil || !bytes.Equal(got, want) {
					t.Fatalf("changed %s: %v", path, readErr)
				}
			}
			if kind != "resolved" {
				want := commitartifact.ErrConflict
				if strings.Contains(kind, "secret") {
					want = engine.ErrSecretTripwire
				} else if kind == "binding" {
					want = queue.ErrBinding
				}
				if !errors.Is(err, want) || ref != "" || git(t, stage, "rev-parse", "HEAD") != original {
					t.Fatalf("accepted unsafe recovery: %s %v", ref, err)
				}
				for _, store := range []artifact.Store{req.Store, commitartifact.SeedStore(req.Store)} {
					sealed, globErr := filepath.Glob(filepath.Join(store.Dir, "*.capture"))
					if globErr != nil || len(sealed) != 0 {
						t.Fatalf("retained unsafe seed/capture: %v %v", sealed, globErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			head := git(t, stage, "rev-parse", "HEAD")
			if parents := git(t, stage, "show", "-s", "--format=%P", head); parents != original+" "+incoming {
				t.Fatal("lost merge parents", parents)
			}
			if got := git(t, stage, "show", "HEAD:cli/plugins/data/saved.json"); got != `{"value":"manually resolved"}` {
				t.Fatal("committed unstaged bytes", got)
			}
			if settled, err := commitartifact.SettledHead(t.Context(), stage); err != nil || settled != head {
				t.Fatalf("unfinished capture repair: %s %v", settled, err)
			}
			meta, err := req.Store.Metadata(t.Context(), ref)
			if err != nil || meta.BaseReference != head || meta.SeedReference == "" {
				t.Fatalf("capture did not retain repaired ancestry: %+v %v", meta, err)
			}
			// Commit from the retained seed after canonical history disappears.
			if err := os.RemoveAll(filepath.Join(stage, ".git")); err != nil {
				t.Fatal(err)
			}
			req.Work.Phase, req.Work.CaptureRef = queue.Captured, ref
			input := service.ArtifactCommitRequest{Capture: req, Commits: artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}}
			commitRef, err := (service.Service{}).CommitArtifact(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			info, err := commitartifact.Open(t.Context(), input.Commits, commitRef, filepath.Join(t.TempDir(), "retained"))
			if err != nil || info.Parent != head {
				t.Fatalf("lost repaired seed: %+v %v", info, err)
			}
		})
	}
}

func TestCaptureArtifactRetriesAfterStagedMergeFailure(t *testing.T) {
	for _, kind := range []string{"source", "seed-store"} {
		t.Run(kind, func(t *testing.T) {
			req, _, original, incoming := stagedCaptureFixture(t, "resolved")
			source := filepath.Join(req.Sync.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			seedDir := commitartifact.SeedStore(req.Store).Dir
			if kind == "source" {
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(req.Store.Dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(seedDir, []byte("blocked seed store"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ref, err := (service.Service{}).CaptureArtifact(t.Context(), req)
			if err == nil || ref != "" {
				t.Fatalf("captured despite %s failure: %s %v", kind, ref, err)
			}
			if kind == "source" && !errors.Is(err, service.ErrCaptureSourceUnavailable) {
				t.Fatal(err)
			}
			head, err := commitartifact.SettledHead(t.Context(), req.Sync.StagingDir)
			if err != nil || head == original || head == "" {
				t.Fatalf("lost completed merge: %s %v", head, err)
			}
			if parents := git(t, req.Sync.StagingDir, "show", "-s", "--format=%P", head); parents != original+" "+incoming {
				t.Fatal("lost merge parents", parents)
			}
			if sealed, err := filepath.Glob(filepath.Join(req.Store.Dir, "*.capture")); err != nil || len(sealed) != 0 {
				t.Fatalf("sealed incomplete capture: %v %v", sealed, err)
			}
			if kind == "source" {
				if err := os.WriteFile(source, data, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if got, err := os.ReadFile(seedDir); err != nil || string(got) != "blocked seed store" {
					t.Fatalf("changed blocked seed store: %s %v", got, err)
				}
				if err := os.Remove(seedDir); err != nil {
					t.Fatal(err)
				}
			}
			ref, err = (service.Service{}).CaptureArtifact(t.Context(), req)
			if err != nil || git(t, req.Sync.StagingDir, "rev-parse", "HEAD") != head {
				t.Fatalf("retry replaced recovered merge: %s %v", ref, err)
			}
			meta, err := req.Store.Metadata(t.Context(), ref)
			if err != nil || meta.BaseReference != head || meta.SeedReference == "" {
				t.Fatalf("wrong retry ancestry: %+v %v", meta, err)
			}
			if err := commitartifact.SeedStore(req.Store).Verify(t.Context(), meta.SeedReference); err != nil {
				t.Fatal("retry seed not retained", err)
			}
		})
	}
}

func TestQueueAdapterCaptureStagedMergeRoundTrip(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("set CLAUDERIG_E2E=1; synthetic recovery before fresh capture")
	}
	for _, kind := range []string{"resolved", "unresolved", "losing-secret", "offline"} {
		t.Run(kind, func(t *testing.T) {
			req, remote, original, _ := stagedCaptureFixture(t, kind)
			transport := &queuedTransport{ArtifactTransport: remote}
			offline := errors.Join(commitartifact.ErrTransport, errors.New("synthetic offline"))
			if kind == "offline" {
				transport.beforeFetch = func() error { return offline }
			}
			q, _, adapter := queueAdapterFixture(t, req, artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}, transport)
			result, err := q.RunOne(t.Context(), time.Now(), adapter)
			if kind == "unresolved" || kind == "losing-secret" {
				code, want := "publication-conflict", commitartifact.ErrConflict
				if kind == "losing-secret" {
					code, want = "scan-rejected", engine.ErrSecretTripwire
				}
				if !errors.Is(err, want) || result.Phase != queue.Queued || result.Acknowledged || remote.fetches != 0 || remote.pushes != 0 || git(t, req.Sync.StagingDir, "rev-parse", "HEAD") != original {
					t.Fatalf("unsafe queued recovery: %+v %v", result, err)
				}
				work, err := q.Snapshot(t.Context())
				if err != nil || len(work) != 1 || work[0].Status != queue.Blocked || work[0].FailureCode != code || work[0].CaptureRef != "" || work[0].CommitRef != "" {
					t.Fatalf("lost blocked capture: %+v %v", work, err)
				}
				return
			}
			head := git(t, req.Sync.StagingDir, "rev-parse", "HEAD")
			if kind == "offline" {
				if !errors.Is(err, offline) || result.Acknowledged || result.Phase != queue.Committed || head == original {
					t.Fatalf("offline capture recovery: %+v %v", result, err)
				}
				work, snapshotErr := q.Snapshot(t.Context())
				if snapshotErr != nil || len(work) != 1 || work[0].CaptureRef == "" || work[0].CommitRef == "" {
					t.Fatalf("lost retained capture: %+v %v", work, snapshotErr)
				}
				if err := os.RemoveAll(filepath.Join(req.Sync.Machine.Home, ".claude")); err != nil {
					t.Fatal(err)
				}
				transport.beforeFetch = nil
				result, err = q.RunOne(t.Context(), work[0].NotBefore, adapter)
			}
			if err != nil || !result.Acknowledged || result.Phase != queue.Pushed || remote.pushes != 1 || git(t, req.Sync.StagingDir, "rev-parse", "HEAD") != head {
				t.Fatalf("queue capture recovery: %+v %v", result, err)
			}
			git(t, remote.dir, "merge-base", "--is-ancestor", head, "main")
			if got := git(t, remote.dir, "show", "main:cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(got, "sealed publication bytes") {
				t.Fatal("lost fresh source", got)
			}
			if _, err := q.RunOne(t.Context(), time.Now(), adapter); !errors.Is(err, queue.ErrEmpty) || remote.pushes != 1 {
				t.Fatal("replayed acknowledged capture", err)
			}
		})
	}
}
