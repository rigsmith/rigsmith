package service_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

const queuedMergePath = "cli/projects/-p/append.jsonl"
const queuedMergeBytes = "{\"uuid\":\"base\"}\n{\"uuid\":\"ours\"}\n{\"uuid\":\"theirs\"}\n"

func TestUnsealedArtifactMergeAllowsManualResolution(t *testing.T) {
	for _, phase := range []string{"capture", "publication"} {
		for _, committed := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "-staged", true: "-committed"}[committed], func(t *testing.T) {
				input, _, _, _ := stagedPublicationFixture(t, "unresolved")
				req := input.Commit.Capture
				if phase == "capture" {
					req.Store = artifact.Store{Dir: filepath.Join(t.TempDir(), "captures")}
					req.Work.Phase, req.Work.CaptureRef, req.Work.CommitRef = queue.Queued, "", ""
				}
				run := func() error {
					if phase == "capture" {
						_, err := (service.Service{}).CaptureArtifact(t.Context(), req)
						return err
					}
					_, err := (service.Service{}).PublishArtifact(t.Context(), input)
					return err
				}
				if err := run(); !errors.Is(err, commitartifact.ErrConflict) {
					t.Fatal("expected initial repair refusal", err)
				}
				stage := req.Sync.StagingDir
				git(t, stage, "add", "cli/plugins/data/saved.json")
				if committed {
					git(t, stage, "commit", "-m", "manual recovery")
				}
				if err := run(); err != nil {
					t.Fatal("empty repair directory blocked manual resolution", err)
				}
			})
		}
	}
}

func unresolvedQueueFixture(t *testing.T) (service.ArtifactPublishRequest, *artifactRemote, string, string) {
	t.Helper()
	input, remote := publicationFixture(t, true, false)
	stage := input.Commit.Capture.Sync.StagingDir
	base := "{\"uuid\":\"base\"}\n"
	put(t, stage, queuedMergePath, base)
	git(t, stage, "add", queuedMergePath)
	git(t, stage, "commit", "-m", "append base")
	git(t, stage, "checkout", "-b", "incoming")
	put(t, stage, queuedMergePath, base+"{\"uuid\":\"theirs\"}\n")
	git(t, stage, "add", queuedMergePath)
	git(t, stage, "commit", "-m", "incoming append")
	incoming := git(t, stage, "rev-parse", "HEAD")
	git(t, stage, "checkout", "main")
	put(t, stage, queuedMergePath, base+"{\"uuid\":\"ours\"}\n")
	git(t, stage, "add", queuedMergePath)
	git(t, stage, "commit", "-m", "local append")
	original := git(t, stage, "rev-parse", "HEAD")
	if _, err := remoteGit(t.Context(), stage, "merge", "--no-commit", "incoming"); err == nil {
		t.Fatal("expected unresolved append conflict")
	}
	put(t, stage, "pending.txt", "pending outside affected files\n")
	return input, remote, original, incoming
}

func TestQueueAdapterRecoversUnresolvedMergeAcrossOfflineRetry(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("set CLAUDERIG_E2E=1; synthetic unresolved queue recovery")
	}
	for _, phase := range []string{"capture", "publication"} {
		t.Run(phase, func(t *testing.T) {
			input, remote, original, incoming := unresolvedQueueFixture(t)
			req := input.Commit.Capture
			if phase == "capture" {
				req.Store = artifact.Store{Dir: filepath.Join(t.TempDir(), "captures")}
				req.Work.Phase, req.Work.CaptureRef, req.Work.CommitRef = queue.Queued, "", ""
			}
			transport := &queuedTransport{ArtifactTransport: remote}
			q, dir, adapter := queueAdapterFixture(t, req, input.Commit.Commits, transport)
			if phase == "publication" {
				w, err := q.Worker(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				b, err := w.Next(t.Context(), time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if err := w.Progress(t.Context(), b.ID, queue.Captured, req.Work.CaptureRef); err != nil {
					t.Fatal(err)
				}
				if err := w.Progress(t.Context(), b.ID, queue.Committed, req.Work.CommitRef); err != nil {
					t.Fatal(err)
				}
				w.Close()
			}
			offline := errors.Join(commitartifact.ErrTransport, errors.New("synthetic offline"))
			transport.beforeFetch = func() error { return offline }
			result, err := q.RunOne(t.Context(), time.Now(), adapter)
			if !errors.Is(err, offline) || result.Phase != queue.Committed || result.Acknowledged {
				t.Fatalf("expected retained offline batch: %+v %v", result, err)
			}
			saved, err := q.Snapshot(t.Context())
			if err != nil || len(saved) != 1 {
				t.Fatal("missing retained batch", saved, err)
			}
			wantCapture, _ := artifactBytes(t, req, saved[0].CaptureRef)
			stage := req.Sync.StagingDir
			head, err := commitartifact.SettledHead(t.Context(), stage)
			if err != nil || head == original {
				t.Fatal("merge not recovered", head, err)
			}
			if git(t, stage, "show", "-s", "--format=%P", head) != original+" "+incoming {
				t.Fatal("lost original merge parents")
			}
			merged, err := os.ReadFile(filepath.Join(stage, queuedMergePath))
			if err != nil || string(merged) != queuedMergeBytes {
				t.Fatalf("lost append bytes: %q %v", merged, err)
			}
			index, err := os.ReadFile(filepath.Join(stage, ".git/index"))
			if err != nil {
				t.Fatal(err)
			}
			// A new event and absent capture sources cannot redirect saved work.
			request := req.Work.Events[0].Request
			request.EventID = "later-merge-event"
			if _, err := q.Enqueue(t.Context(), request, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(req.Sync.Machine.Home, ".claude")); err != nil {
				t.Fatal(err)
			}
			q, err = queue.Open(t.Context(), dir, req.Binding)
			if err != nil {
				t.Fatal(err)
			}
			transport.beforeFetch = nil
			result, err = q.RunOne(t.Context(), time.Now().Add(time.Hour), adapter)
			if err != nil || !result.Acknowledged || remote.pushes != 1 {
				t.Fatalf("restart publication: %+v %v", result, err)
			}
			git(t, remote.dir, "merge-base", "--is-ancestor", head, "main")
			show := exec.CommandContext(t.Context(), "git", "show", "main:cli/projects/-workspace-acme/s.jsonl")
			show.Dir = remote.dir
			remoteCapture, err := show.Output()
			if err != nil || !bytes.Equal(remoteCapture, []byte(wantCapture)) {
				t.Fatalf("remote changed retained capture bytes: %v", err)
			}
			gotIndex, err := os.ReadFile(filepath.Join(stage, ".git/index"))
			if err != nil || !bytes.Equal(gotIndex, index) || git(t, stage, "rev-parse", "HEAD") != head {
				t.Fatal("replay changed completed canonical merge", err)
			}
			remaining, err := q.Snapshot(t.Context())
			if err != nil || len(remaining) != 1 || remaining[0].Events[0].Request.EventID != "later-merge-event" {
				t.Fatalf("acknowledged newer work: %+v %v", remaining, err)
			}
		})
	}
}

func TestCaptureArtifactUnresolvedRecoveryBeforeSourceFailure(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "replay", true: "changed-head"}[change], func(t *testing.T) {
			input, _, _, _ := unresolvedQueueFixture(t)
			req := input.Commit.Capture
			req.Store = artifact.Store{Dir: filepath.Join(t.TempDir(), "captures")}
			req.Work.Phase, req.Work.CaptureRef, req.Work.CommitRef = queue.Queued, "", ""
			source := filepath.Join(req.Sync.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			if _, err := (service.Service{}).CaptureArtifact(t.Context(), req); !errors.Is(err, service.ErrCaptureSourceUnavailable) {
				t.Fatal("expected source failure after completion", err)
			}
			head := git(t, req.Sync.StagingDir, "rev-parse", "HEAD")
			if change {
				git(t, req.Sync.StagingDir, "commit", "--allow-empty", "-m", "later unrelated commit")
			}
			if err := os.WriteFile(source, data, 0600); err != nil {
				t.Fatal(err)
			}
			ref, err := (service.Service{}).CaptureArtifact(t.Context(), req)
			if change {
				if err == nil || ref != "" {
					t.Fatal("accepted unrelated HEAD as saved completion")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			meta, err := req.Store.Metadata(t.Context(), ref)
			if err != nil || meta.BaseReference != head || !strings.Contains(meta.SeedReference, ":") {
				t.Fatal("lost completed repair seed", meta, err)
			}
		})
	}
}
