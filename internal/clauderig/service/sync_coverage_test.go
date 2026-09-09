package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

var coverageIdentity = service.Identity{AccountUUID: "11111111-1111-4111-8111-111111111111", Email: "fixture@example.com"}

func coverageFixture(t *testing.T) (service.SyncRequest, *queue.Queue, queue.Request, service.Service) {
	t.Helper()
	req, root := syncFixture(t, "coverage bytes")
	remote := filepath.Join(root, "remote.git")
	git(t, root, "init", "--bare", remote)
	req.Config.Remote = remote
	q, r := coverageQueue(t, req, filepath.Join(root, "queue"))
	return req, q, r, service.Service{ReadIdentity: func() (service.Identity, error) { return coverageIdentity, nil }}
}

func coverageQueue(t *testing.T, req service.SyncRequest, dir string) (*queue.Queue, queue.Request) {
	t.Helper()
	binding, err := service.CaptureBinding(req, engine.LocalProfileNames())
	if err != nil {
		t.Fatal(err)
	}
	q, err := queue.Create(t.Context(), dir, binding)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := service.CaptureProvenance(coverageIdentity)
	if err != nil {
		t.Fatal(err)
	}
	r := queue.Request{EventID: "event", SessionID: "s", ProvenanceID: provenance, Flush: queue.Flush{Mode: queue.Normal}}
	return q, r
}
func enqueueCoverage(t *testing.T, q *queue.Queue, r queue.Request) queue.Event {
	t.Helper()
	e, err := q.Enqueue(t.Context(), r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func pendingCoverage(t *testing.T, q *queue.Queue, n int) []queue.Work {
	t.Helper()
	batches, err := q.Snapshot(t.Context())
	if err != nil || len(batches) != n {
		t.Fatalf("pending: %+v %v, want %d", batches, err, n)
	}
	for _, b := range batches {
		if b.Phase != queue.Queued || b.Attempts != 0 {
			t.Fatalf("manual sync changed saved phase: %+v", b)
		}
	}
	return batches
}

func TestSyncWithCoveragePreservesLaterGenerationAndIdentityTiming(t *testing.T) {
	req, q, r, svc := coverageFixture(t)
	original := enqueueCoverage(t, q, r)
	reads, decodes := 0, 0
	svc.ReadIdentity = func() (service.Identity, error) { reads++; return coverageIdentity, nil }
	req.ResolveFlush = func() service.FlushIntent {
		decodes++
		if reads != 1 {
			t.Fatal("identity must precede flush decoding")
		}
		return service.FlushIntent{Mode: service.FlushSelected, Paths: []string{filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")}}
	}
	var late queue.Event
	svc.Observe = func(e service.Event) {
		if _, ok := e.(service.Captured); ok {
			if w, err := q.Worker(t.Context()); err == nil {
				w.Close()
				t.Fatal("worker ownership released before publication")
			}
			if _, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0); err == nil {
				release()
				t.Fatal("staging ownership released before publication")
			}
			r.EventID = "late"
			late = enqueueCoverage(t, q, r)

		}
		if _, ok := e.(service.Published); ok {
			if err := os.Remove(filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || !reflect.DeepEqual(result.Acknowledged, []uint64{original.Generation}) {
		t.Fatalf("coverage sync: %+v %v", result, err)
	}
	if reads != 1 || decodes != 1 || !result.Sync.Publication.Pushed || result.Sync.Publication.SnapshotCommit == "" {
		t.Fatalf("timing/publication: %+v reads=%d decodes=%d", result, reads, decodes)
	}
	if got := pendingCoverage(t, q, 1); got[0].ID != late.BatchID {
		t.Fatal("later event acknowledged", got)
	}
	if body := git(t, req.Config.Remote, "show", "main:cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(body, "coverage bytes") {
		t.Fatal(body)
	}
	if got := enqueueCoverage(t, q, queue.Request{EventID: "event", SessionID: "s", ProvenanceID: r.ProvenanceID, Flush: queue.Flush{Mode: queue.Normal}}); got.Generation != original.Generation {
		t.Fatal("lost receipt")
	}
}

func TestSyncWithCoverageReadsSameMetadataBytesFresh(t *testing.T) {
	req, q, r, svc := coverageFixture(t)
	if _, err := svc.Sync(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// Model Windows' reduced stat cache on every platform. Fresh capture must
	// also force Git to read bytes when size/mtime match its cached index entry.
	git(t, req.StagingDir, "config", "core.trustctime", "false")
	git(t, req.StagingDir, "config", "core.checkStat", "minimal")
	src := filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(string(data), "coverage bytes", "replaced bytes"))
	if int64(len(data)) != info.Size() {
		t.Fatal("fixture size changed")
	}
	if err := os.WriteFile(src, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(src, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	enqueueCoverage(t, q, r)
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 1 {
		t.Fatalf("sync: %+v %v", result, err)
	}
	if body := git(t, req.Config.Remote, "show", "main:cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(body, "replaced bytes") {
		t.Fatal("acknowledged stale mtime match", body)
	}
}

func TestSyncWithCoverageDeferralAndSelectedSubagentFlush(t *testing.T) {
	req, _, _, svc := coverageFixture(t)
	native := false
	req.Config.ChunkTranscripts = &native
	req.Config.Retention.LargeFileBytes = 1024
	child := filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s/subagents/agent-a.jsonl")
	put(t, filepath.Dir(child), filepath.Base(child), strings.Repeat("{\"type\":\"progress\"}\n", 100))
	if _, err := svc.Sync(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	q, r := coverageQueue(t, req, filepath.Join(t.TempDir(), "queue"))
	r.Flush = queue.Flush{Mode: queue.Selected, Paths: []string{filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")}}
	enqueueCoverage(t, q, r)
	f, err := os.OpenFile(child, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("{\"type\":\"progress\",\"new\":true}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 0 || result.Sync.Capture.Roots[0].Deferred != 1 {
		t.Fatalf("deferred: %+v %v", result, err)
	}
	pendingCoverage(t, q, 1)
	req.Flush = service.FlushIntent{Mode: service.FlushSelected, Paths: r.Flush.Paths}
	result, err = svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 1 {
		t.Fatalf("selected child flush: %+v %v", result, err)
	}
	pendingCoverage(t, q, 0)
}

func TestSyncWithCoverageLeavesUnavailableRequestsPending(t *testing.T) {
	for _, mode := range []string{"missing", "ambiguous", "retention", "oversize", "missing-selected", "other-provenance", "identity-error"} {
		t.Run(mode, func(t *testing.T) {
			req, _, _, svc := coverageFixture(t)
			src := filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")
			switch mode {
			case "missing":
				if err := os.Remove(src); err != nil {
					t.Fatal(err)
				}
			case "ambiguous":
				put(t, req.Machine.Home, ".claude/projects/-other/s.jsonl", "{\"type\":\"progress\"}\n")
			case "retention":
				old := time.Now().AddDate(0, 0, -100)
				if err := os.Chtimes(src, old, old); err != nil {
					t.Fatal(err)
				}
				req.Config.Retention.HistoryDays = 1
			case "oversize":
				if _, err := svc.Sync(t.Context(), req); err != nil {
					t.Fatal(err)
				}
				native := false
				req.Config.ChunkTranscripts = &native
				req.Config.Retention.MaxFileBytes = 100
			case "identity-error":
				svc.ReadIdentity = func() (service.Identity, error) { return service.Identity{}, errors.New("unavailable identity") }
			}
			q, r := coverageQueue(t, req, filepath.Join(t.TempDir(), "queue"))
			if mode == "missing-selected" {
				r.Flush = queue.Flush{Mode: queue.Selected, Paths: []string{filepath.Join(req.Machine.Home, "missing.jsonl")}}
			}
			if mode == "other-provenance" {
				r.ProvenanceID = "different-account"
			}
			enqueueCoverage(t, q, r)
			result, err := svc.SyncWithCoverage(t.Context(), req, q)
			if err != nil || len(result.Acknowledged) != 0 {
				t.Fatalf("unavailable: %+v %v", result, err)
			}
			pendingCoverage(t, q, 1)
		})
	}
}

func TestSyncWithCoverageDoesNotAcknowledgeLocalDryOrFailedSync(t *testing.T) {
	for _, mode := range []string{"local", "dry", "push-failure", "scan-failure", "cancel-after-push"} {
		t.Run(mode, func(t *testing.T) {
			req, _, _, svc := coverageFixture(t)
			if mode == "local" {
				req.Config.Remote = ""
			}
			q, r := coverageQueue(t, req, filepath.Join(t.TempDir(), "queue"))
			enqueueCoverage(t, q, r)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "dry":
				req.DryRun = true
			case "push-failure":
				if err := os.RemoveAll(req.Config.Remote); err != nil {
					t.Fatal(err)
				}
			case "scan-failure":
				put(t, req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl", "-----BEGIN PRIVATE KEY-----\nfixture\n-----END PRIVATE KEY-----\n")
			case "cancel-after-push":
				svc.Observe = func(e service.Event) {
					if _, ok := e.(service.Published); ok {
						cancel()
					}
				}
			}
			result, err := svc.SyncWithCoverage(ctx, req, q)
			if mode != "local" && mode != "dry" && err == nil {
				t.Fatal("expected failed sync/confirmation")
			}
			if (mode == "local" || mode == "dry") && err != nil {
				t.Fatal(err)
			}
			if len(result.Acknowledged) != 0 {
				t.Fatal("acknowledged incomplete publication", result)
			}
			pendingCoverage(t, q, 1)
			w, err := q.Worker(t.Context())
			if err != nil {
				t.Fatal("leaked worker", err)
			}
			w.Close()
			_, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0)
			if err != nil {
				t.Fatal("leaked staging", err)
			}
			release()
		})
	}
}

func TestSyncWithCoverageChunkedAndRedactedBytes(t *testing.T) {
	req, _, _, svc := coverageFixture(t)
	chunked := true
	req.Config.ChunkTranscripts = &chunked
	req.Config.RedactTranscripts = true
	src := filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")
	f, err := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	// Small records exercise streaming redaction and physical chunk validation.
	line := "{\"type\":\"progress\",\"text\":\"" + strings.Repeat("hello ", 200) + "Authorization: Bearer fixture-token-0123456789abcdefghijklmnopqrstuvwxyz\"}\n"
	for i := 0; i < 3*transcript.ChunkSize/len(line); i++ {
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	q, r := coverageQueue(t, req, filepath.Join(t.TempDir(), "queue"))
	enqueueCoverage(t, q, r)
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 1 {
		t.Fatalf("chunked/redacted: %+v %v", result, err)
	}
	stored, err := os.ReadFile(filepath.Join(req.StagingDir, "cli/projects/-workspace-acme/s.jsonl"))
	if err != nil || !transcript.IsIndex(stored) {
		t.Fatal("fixture did not retain chunked output", err)
	}
	logical, err := transcript.ReadFile(filepath.Join(req.StagingDir, "cli/projects/-workspace-acme/s.jsonl"))
	if err != nil || strings.Contains(string(logical), "fixture-token-0123456789abcdefghijklmnopqrstuvwxyz") {
		t.Fatal("fixture did not redact token", err)
	}
	pendingCoverage(t, q, 0)
}

func TestSyncWithCoverageRefusesChangedBindingAndOverlappingQueue(t *testing.T) {
	for _, mode := range []string{"binding", "source-overlap", "staging-overlap", "merge-tool", "unsupported-remote"} {
		t.Run(mode, func(t *testing.T) {
			req, q, r, svc := coverageFixture(t)
			switch mode {
			case "binding":
				req.Config.Retention.HistoryDays++
			case "source-overlap":
				q, r = coverageQueue(t, req, filepath.Join(req.Machine.Home, ".claude", "queue"))
			case "staging-overlap":
				q, r = coverageQueue(t, req, filepath.Join(req.StagingDir, "queue"))
			case "merge-tool":
				req.AllowMergeTool = true
			case "unsupported-remote":
				req.Config.Remote = "ssh://git@example.com/acme/backup.git"
				svc.ReadIdentity = func() (service.Identity, error) {
					t.Fatal("capture started before transport validation")
					return service.Identity{}, nil
				}
			}
			enqueueCoverage(t, q, r)
			result, err := svc.SyncWithCoverage(t.Context(), req, q)
			if err == nil || result.Sync.Publication.Pushed || len(result.Acknowledged) > 0 {
				t.Fatalf("unsafe input accepted: %+v %v", result, err)
			}
			if mode == "binding" && !errors.Is(err, queue.ErrBinding) {
				t.Fatal(err)
			}
			pendingCoverage(t, q, 1)
		})
	}
}

func TestSyncWithCoverageRequiresAllFlushAndWholeBatchEvidence(t *testing.T) {
	req, _, _, svc := coverageFixture(t)
	native := false
	req.Config.ChunkTranscripts = &native
	req.Config.Retention.LargeFileBytes = 1024
	other := filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/other.jsonl")
	put(t, filepath.Dir(other), filepath.Base(other), strings.Repeat("{\"type\":\"progress\"}\n", 100))
	if _, err := svc.Sync(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	q, r := coverageQueue(t, req, filepath.Join(t.TempDir(), "queue"))
	enqueueCoverage(t, q, r)
	r.EventID = "all"
	r.Flush = queue.Flush{Mode: queue.All}
	enqueueCoverage(t, q, r)
	f, err := os.OpenFile(other, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("{\"type\":\"progress\",\"new\":true}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	req.Flush = service.FlushIntent{Mode: service.FlushSelected, Paths: []string{filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")}}
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 0 {
		t.Fatalf("partial all-flush was acknowledged: %+v %v", result, err)
	}
	batches := pendingCoverage(t, q, 1)
	if len(batches[0].Events) != 2 {
		t.Fatal("lost partially covered batch")
	}
	req.Flush = service.FlushIntent{Mode: service.FlushAll}
	result, err = svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || !reflect.DeepEqual(result.Acknowledged, []uint64{1, 2}) {
		t.Fatalf("all-flush: %+v %v", result, err)
	}
}

func TestSyncWithCoverageRequiresFreshRemoteAncestry(t *testing.T) {
	req, q, r, svc := coverageFixture(t)
	enqueueCoverage(t, q, r)
	svc.Observe = func(e service.Event) {
		if _, ok := e.(service.Published); ok {
			// Simulate a remote rewrite after push success. Cached tracking refs and
			// Pushed=true must not authorize queue acknowledgement.
			git(t, req.Config.Remote, "update-ref", "-d", "refs/heads/main")
		}
	}
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err == nil || !result.Sync.Publication.Pushed || len(result.Acknowledged) != 0 {
		t.Fatalf("unconfirmed push: %+v %v", result, err)
	}
	pendingCoverage(t, q, 1)
	svc.Observe = nil
	result, err = svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 1 {
		t.Fatalf("confirmed retry: %+v %v", result, err)
	}
}

func TestSyncWithCoverageNewSubagentAfterCaptureStaysPending(t *testing.T) {
	req, q, r, svc := coverageFixture(t)
	enqueueCoverage(t, q, r)
	svc.Observe = func(e service.Event) {
		if _, ok := e.(service.Captured); ok {
			put(t, req.Machine.Home, ".claude/projects/-workspace-acme/s/subagents/agent-new.jsonl", "{\"type\":\"progress\"}\n")
		}
	}
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 0 {
		t.Fatalf("omitted new group member: %+v %v", result, err)
	}
	pendingCoverage(t, q, 1)
	svc.Observe = nil
	result, err = svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 1 {
		t.Fatalf("group retry: %+v %v", result, err)
	}
}

func TestSyncWithCoverageConfirmsSnapshotBeforeReconciliation(t *testing.T) {
	req, _, _, svc := coverageFixture(t)
	if _, err := svc.Sync(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(t.TempDir(), "peer")
	git(t, filepath.Dir(peer), "clone", "--branch", "main", req.Config.Remote, peer)
	put(t, peer, "cli/plugins/data/peer.txt", "peer snapshot\n")
	git(t, peer, "add", "-A")
	git(t, peer, "commit", "-m", "peer update")
	q, r := coverageQueue(t, req, filepath.Join(t.TempDir(), "queue"))
	enqueueCoverage(t, q, r)
	put(t, req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl", "{\"type\":\"user\",\"sessionId\":\"s\",\"uuid\":\"new-message\",\"cwd\":\"/workspace/acme\",\"message\":{\"role\":\"user\",\"content\":\"local update\"}}\n")
	svc.Observe = func(e service.Event) {
		if _, ok := e.(service.Captured); ok {
			git(t, peer, "push", "origin", "main")
		}
	}
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 1 {
		t.Fatalf("reconciled snapshot: %+v %v", result, err)
	}
	head := git(t, req.StagingDir, "rev-parse", "HEAD")
	if result.Sync.Publication.SnapshotCommit == head {
		t.Fatal("fixture did not reconcile after snapshot commit")
	}
	git(t, req.StagingDir, "merge-base", "--is-ancestor", result.Sync.Publication.SnapshotCommit, head)
	pendingCoverage(t, q, 0)
}

func TestSyncWithCoverageRequiresNativeParentShape(t *testing.T) {
	for _, parentPresent := range []bool{false, true} {
		t.Run(fmt.Sprint(parentPresent), func(t *testing.T) {
			req, q, r, svc := coverageFixture(t)
			if _, err := svc.Sync(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			// Keep the permanent ledger row while adding a nested name collision.
			if !parentPresent {
				if err := os.Remove(filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")); err != nil {
					t.Fatal(err)
				}
			}
			put(t, req.Machine.Home, ".claude/projects/-workspace-acme/other/subagents/s.jsonl", "{\"type\":\"progress\"}\n")
			enqueueCoverage(t, q, r)
			result, err := svc.SyncWithCoverage(t.Context(), req, q)
			want := 0
			if parentPresent {
				want = 1
			}
			if err != nil || len(result.Acknowledged) != want {
				t.Fatalf("parent=%v: %+v %v", parentPresent, result, err)
			}
			pendingCoverage(t, q, 1-want)
		})
	}
}

func TestSyncWithCoverageSymlinkedTranscript(t *testing.T) {
	for _, mode := range []string{queue.Normal, queue.Selected} {
		t.Run(string(mode), func(t *testing.T) {
			req, q, r, svc := coverageFixture(t)
			src := filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")
			target := filepath.Join(t.TempDir(), "source.jsonl")
			if err := os.Rename(src, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, src); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			r.Flush.Mode = mode
			if mode == queue.Selected {
				r.Flush.Paths = []string{src}
			}
			enqueueCoverage(t, q, r)
			result, err := svc.SyncWithCoverage(t.Context(), req, q)
			if err != nil || len(result.Acknowledged) != 1 {
				t.Fatalf("symlinked transcript: %+v %v", result, err)
			}
			pendingCoverage(t, q, 0)
		})
	}
}

func TestSyncWithCoverageDoesNotShareEvidenceBetweenFileAliases(t *testing.T) {
	req, _, _, svc := coverageFixture(t)
	native := false
	req.Config.ChunkTranscripts = &native
	req.Config.Retention.LargeFileBytes = 1024
	parent := filepath.Join(req.Machine.Home, ".claude/projects/-workspace-acme/s.jsonl")
	body, err := os.ReadFile(parent)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte(strings.Repeat("{\"type\":\"progress\"}\n", 100))...)
	if err := os.WriteFile(parent, body, 0600); err != nil {
		t.Fatal(err)
	}
	const aliasRel = "projects/-workspace-acme/s/subagents/agent-a.jsonl"
	alias := filepath.Join(req.Machine.Home, ".claude", filepath.FromSlash(aliasRel))
	if err := os.MkdirAll(filepath.Dir(alias), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := svc.Sync(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	// Only the alias has a stale, slightly shorter staged snapshot. Its large
	// source is deferred while the parent is freshly read from the same target.
	put(t, req.StagingDir, "cli/"+aliasRel, string(body[:len(body)-len("{\"type\":\"progress\"}\n")]))
	q, r := coverageQueue(t, req, filepath.Join(t.TempDir(), "queue"))
	enqueueCoverage(t, q, r)
	result, err := svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 0 || result.Sync.Capture.Roots[0].Deferred != 1 {
		t.Fatalf("alias borrowed evidence: %+v %v", result, err)
	}
	pendingCoverage(t, q, 1)
	req.Flush.Mode = service.FlushAll
	result, err = svc.SyncWithCoverage(t.Context(), req, q)
	if err != nil || len(result.Acknowledged) != 1 {
		t.Fatalf("alias flush: %+v %v", result, err)
	}
}

func TestSyncWithCoverageSupervisedCanonicalGitIsGated(t *testing.T) {
	ctx := process.WithSupervisor(t.Context(), "unused")
	req, q, request, svc := coverageFixture(t)
	enqueueCoverage(t, q, request)
	svc.ReadIdentity = func() (service.Identity, error) {
		t.Fatal("gated sync reached capture")
		return service.Identity{}, nil
	}
	if _, err := svc.SyncWithCoverage(ctx, req, q); !errors.Is(err, service.ErrSupervisedSyncUnavailable) {
		t.Fatal("canonical coverage bypassed supervision", err)
	}
	if _, err := svc.Sync(ctx, req); !errors.Is(err, service.ErrSupervisedSyncUnavailable) {
		t.Fatal("canonical sync bypassed supervision", err)
	}
	pendingCoverage(t, q, 1)
}
