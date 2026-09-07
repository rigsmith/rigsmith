package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func artifactCaptureFixture(t *testing.T, text string) service.ArtifactCaptureRequest {
	t.Helper()
	syncReq, root := syncFixture(t, text)
	identity := service.Identity{AccountUUID: "11111111-1111-4111-8111-111111111111", Email: "fixture@example.com"}
	binding, err := service.CaptureBinding(syncReq, nil)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := service.CaptureProvenance(identity)
	if err != nil {
		t.Fatal(err)
	}
	q, err := queue.Create(t.Context(), filepath.Join(root, "queue"), binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.Enqueue(t.Context(), queue.Request{EventID: "event-a", SessionID: "s", ProvenanceID: provenance, Flush: queue.Flush{Mode: queue.Normal}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	w, err := q.Worker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	b, err := w.Next(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return service.ArtifactCaptureRequest{Store: artifact.Store{Dir: filepath.Join(root, "captures")}, Binding: binding, Work: b, Sync: syncReq, Identity: identity}
}
func artifactBytes(t *testing.T, req service.ArtifactCaptureRequest, ref string) (string, string) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "tree")
	if err := req.Store.Extract(t.Context(), ref, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "cli", "projects", "-workspace-acme", "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data), dest
}

func TestCaptureArtifactPinsBytesAndOnlyRequestedAttribution(t *testing.T) {
	req := artifactCaptureFixture(t, "original queued capture")
	source := filepath.Join(req.Sync.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")
	sourceBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// Another source session is visible to the same walk but has no provenance in
	// this batch. It must not inherit the queued event's account.
	other := strings.ReplaceAll(string(sourceBytes), `"s"`, `"other"`)
	put(t, req.Sync.Machine.Home, ".claude/projects/-workspace-acme/other.jsonl", other)
	svc := service.Service{ReadIdentity: func() (service.Identity, error) { t.Fatal("read worker login"); return service.Identity{}, nil }}
	ref, err := svc.CaptureArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(req.Sync.StagingDir, "cli")); !os.IsNotExist(err) {
		t.Fatal("changed canonical staging", err)
	}
	data, dest := artifactBytes(t, req, ref)
	if !strings.Contains(data, "original queued capture") {
		t.Fatal("wrong bytes")
	}
	entries := ledger.LoadAll(dest)
	if entries["s"].Account != req.Identity.AccountUUID || entries["other"].Account != "" {
		t.Fatalf("wrong attribution: %+v", entries)
	}
	if _, err = os.Stat(filepath.Join(dest, devices.FileName)); err == nil {
		t.Fatal("delayed capture created a device registry")
	}
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	again, err := svc.CaptureArtifact(t.Context(), req)
	if err != nil || again != ref {
		t.Fatalf("retry recaptured deleted source: %s %v", again, err)
	}
	data, _ = artifactBytes(t, req, again)
	if !strings.Contains(data, "original queued capture") {
		t.Fatal("retry changed bytes")
	}
}

func TestCaptureArtifactPreservesEmptyDirectoryAliases(t *testing.T) {
	for _, excludedOnly := range []bool{false, true} {
		name := "empty"
		if excludedOnly {
			name = "excluded-only"
		}
		t.Run(name, func(t *testing.T) {
			req := artifactCaptureFixture(t, "shared memory")
			live := filepath.Join(req.Sync.Machine.Home, ".claude")
			const target = "projects/-workspace-acme/memory"
			const alias = "projects/-workspace-acme-wt/memory"
			if err := os.MkdirAll(filepath.Join(live, filepath.FromSlash(target)), 0700); err != nil {
				t.Fatal(err)
			}
			if excludedOnly {
				put(t, live, target+"/node_modules/dependency/index.js", "excluded dependency")
			}
			put(t, live, "projects/-workspace-acme-wt/other.jsonl",
				`{"type":"user","sessionId":"other","cwd":"/workspace/acme-wt","message":{"role":"user","content":"fixture"}}`+"\n")
			if err := os.Symlink(filepath.Join(live, filepath.FromSlash(target)), filepath.Join(live, filepath.FromSlash(alias))); err != nil {
				t.Skipf("symlink unsupported: %v", err)
			}
			svc := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
			// Capture live inputs separately so seeded metadata cannot hide an alias
			// lost while freezing the queued capture's source tree.
			native := req.Sync
			native.StagingDir = filepath.Join(t.TempDir(), "native")
			if _, err := svc.Capture(t.Context(), native); err != nil {
				t.Fatal(err)
			}
			nativeManifest, err := manifest.Load(native.StagingDir)
			if err != nil || nativeManifest.Links[alias] != target {
				t.Fatalf("live capture did not preserve alias: %+v, %v", nativeManifest, err)
			}
			ref, err := svc.CaptureArtifact(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			_, dest := artifactBytes(t, req, ref)
			sealedManifest, err := manifest.Load(dest)
			if err != nil || sealedManifest.Links[alias] != nativeManifest.Links[alias] {
				t.Fatalf("sealed capture lost alias: %+v, %v", sealedManifest, err)
			}
			if _, err := os.Stat(filepath.Join(dest, "cli", filepath.FromSlash(target), "node_modules")); !os.IsNotExist(err) {
				t.Fatalf("sealed capture included excluded files: %v", err)
			}
		})
	}
}

func TestCaptureArtifactRejectsChangedBindingProvenanceAndMissingSource(t *testing.T) {
	for _, mode := range []string{"config", "provenance", "missing", "oversize", "overlap", "secret"} {
		t.Run(mode, func(t *testing.T) {
			text := "fixture"
			if mode == "secret" {
				text = "sk-ant-api03-" + strings.Repeat("z", 60)
			}
			req := artifactCaptureFixture(t, text)
			switch mode {
			case "config":
				req.Sync.Config.Remote = "different-remote"
			case "provenance":
				req.Identity.AccountUUID = "22222222-2222-4222-8222-222222222222"
			case "missing":
				if err := os.Remove(filepath.Join(req.Sync.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				req.Sync.Config.Retention.MaxFileBytes = 1
				var err error
				req.Binding, err = service.CaptureBinding(req.Sync, nil)
				if err != nil {
					t.Fatal(err)
				}
			case "overlap":
				req.Store.Dir = filepath.Join(req.Sync.StagingDir, "captures")
			}
			_, err := (service.Service{}).CaptureArtifact(t.Context(), req)
			if err == nil {
				t.Fatal("invalid capture succeeded")
			}
			if mode == "config" || mode == "provenance" {
				if !errors.Is(err, queue.ErrBinding) {
					t.Fatal(err)
				}
			}
			published, _ := filepath.Glob(filepath.Join(req.Store.Dir, "*.capture"))
			if len(published) != 0 {
				t.Fatal("failed capture was sealed", published)
			}
		})
	}
}

func TestCaptureArtifactProtectsOldRequestedSourceAndRetainsSeedReference(t *testing.T) {
	req := artifactCaptureFixture(t, "aged source")
	svc := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
	// Seed canonical history using normal sync, then make its requested live
	// source old enough that ordinary retention would exclude it.
	if _, err := svc.Sync(t.Context(), req.Sync); err != nil {
		t.Fatal(err)
	}
	seed := git(t, req.Sync.StagingDir, "rev-parse", "HEAD")
	source := filepath.Join(req.Sync.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")
	old := time.Now().AddDate(-2, 0, 0)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	ref, err := svc.CaptureArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := req.Store.Metadata(t.Context(), ref)
	if err != nil || meta.BaseReference != seed {
		t.Fatalf("seed %+v %v", meta, err)
	}
	data, _ := artifactBytes(t, req, ref)
	if !strings.Contains(data, "aged source") {
		t.Fatal("requested source was pruned")
	}
	if got := git(t, req.Sync.StagingDir, "rev-parse", "HEAD"); got != seed {
		t.Fatal("capture moved canonical HEAD")
	}
	if dirty := git(t, req.Sync.StagingDir, "status", "--porcelain"); dirty != "" {
		t.Fatal("capture changed canonical tree", dirty)
	}
	if req.Sync.Config.Retention.HistoryDays == 0 {
		t.Fatal("modified caller retention configuration")
	}
}

func TestCaptureArtifactFrozenSourceSurvivesDeletionDuringScan(t *testing.T) {
	req := artifactCaptureFixture(t, "frozen before scan")
	source := filepath.Join(req.Sync.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")
	svc := service.Service{Observe: func(e service.Event) {
		if _, ok := e.(service.SyncStarted); ok {
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
		}
	}}
	ref, err := svc.CaptureArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := artifactBytes(t, req, ref)
	if !strings.Contains(data, "frozen before scan") {
		t.Fatal("lost frozen source")
	}
}

func TestCaptureArtifactRedactsBeforeSealing(t *testing.T) {
	req := artifactCaptureFixture(t, "sk-ant-api03-"+strings.Repeat("z", 60))
	req.Sync.Config.RedactTranscripts = true
	var err error
	req.Binding, err = service.CaptureBinding(req.Sync, nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := (service.Service{}).CaptureArtifact(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := artifactBytes(t, req, ref)
	if strings.Contains(data, strings.Repeat("z", 60)) {
		t.Fatal("sealed raw token")
	}
	files, err := os.ReadDir(req.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".capture") {
		t.Fatal("source workspace retained", files)
	}
}

func TestCaptureArtifactPreservesChunkedTranscripts(t *testing.T) {
	req := artifactCaptureFixture(t, strings.Repeat("ordinary words ", transcript.ChunkSize/4))
	ref, err := (service.Service{}).CaptureArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "tree")
	if err = req.Store.Extract(t.Context(), ref, dest); err != nil {
		t.Fatal(err)
	}
	f, err := transcript.Open(filepath.Join(dest, "cli", "projects", "-workspace-acme", "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = os.Stat(filepath.Join(dest, "cli", "projects", "-workspace-acme", "s.jsonl"+transcript.Suffix)); err != nil {
		t.Fatal("chunk parts missing", err)
	}
}

func TestCaptureBindingPinsResolvedAutoChunking(t *testing.T) {
	req := artifactCaptureFixture(t, "fixture")
	req.Sync.Config.ChunkTranscripts = nil
	before, err := service.CaptureBinding(req.Sync, nil)
	if err != nil {
		t.Fatal(err)
	}
	put(t, req.Sync.StagingDir, transcript.StorageFile, `{"version":1,"chunkedTranscripts":true}`)
	after, err := service.CaptureBinding(req.Sync, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("auto chunking was not part of resolved binding")
	}
	req.Binding = before
	if _, err = (service.Service{}).CaptureArtifact(t.Context(), req); !errors.Is(err, queue.ErrBinding) {
		t.Fatal("accepted changed storage mode", err)
	}
}

func TestCaptureCannotSealBeforeSeedRetentionSucceeds(t *testing.T) {
	for _, mode := range []string{"capacity", "blocked-store"} {
		t.Run(mode, func(t *testing.T) {
			req := artifactCaptureFixture(t, "seed must be durable first")
			svc := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
			if _, err := svc.Sync(t.Context(), req.Sync); err != nil {
				t.Fatal(err)
			}
			head := git(t, req.Sync.StagingDir, "rev-parse", "HEAD")
			if mode == "capacity" {
				req.Store.MaxBytes = 512 // Too small for the complete seed bundle.
			} else {
				if err := os.MkdirAll(req.Store.Dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(commitartifact.SeedStore(req.Store).Dir, []byte("blocked"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ref, err := svc.CaptureArtifact(t.Context(), req)
			if err == nil || ref != "" {
				t.Fatalf("capture acknowledged failed seed retention: %s %v", ref, err)
			}
			if mode == "capacity" && !errors.Is(err, artifact.ErrTooLarge) {
				t.Fatal(err)
			}
			if paths, _ := filepath.Glob(filepath.Join(req.Store.Dir, "*.capture")); len(paths) != 0 {
				t.Fatal("capture sealed before seed", paths)
			}
			if got := git(t, req.Sync.StagingDir, "rev-parse", "HEAD"); got != head {
				t.Fatal("failed seed retention changed canonical HEAD")
			}
		})
	}
}

func TestCaptureArtifactRejectsUnsettledStagingBeforeRetainingSeed(t *testing.T) {
	markers := []string{"MERGE_HEAD", "MERGE_AUTOSTASH", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer"}
	for _, marker := range append(markers, "unmerged-index") {
		t.Run(marker, func(t *testing.T) {
			req := artifactCaptureFixture(t, "pending capture")
			svc := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
			if _, err := svc.Sync(t.Context(), req.Sync); err != nil {
				t.Fatal(err)
			}
			stage := req.Sync.StagingDir
			head := git(t, stage, "rev-parse", "HEAD")
			paths := []string{".git/index", ".git/config", "pending.txt"}
			if marker == "unmerged-index" {
				// Populate all three conflict stages without an operation marker,
				// so only the unmerged-index guard can reject this capture.
				var entries strings.Builder
				for i, content := range []string{"base", "ours", "theirs"} {
					put(t, stage, "conflicted.txt", content)
					blob := git(t, stage, "hash-object", "-w", "conflicted.txt")
					fmt.Fprintf(&entries, "100644 %s %d\tconflicted.txt\n", blob, i+1)
				}
				cmd := exec.Command("git", "update-index", "--index-info")
				cmd.Dir = stage
				cmd.Stdin = strings.NewReader(entries.String())
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("create unmerged index: %v\n%s", err, out)
				}
				if got := git(t, stage, "ls-files", "--unmerged"); got != strings.TrimSpace(entries.String()) {
					t.Fatalf("unexpected conflict stages: %s", got)
				}
				for _, absent := range markers {
					if _, err := os.Stat(filepath.Join(stage, ".git", absent)); !os.IsNotExist(err) {
						t.Fatalf("unexpected operation marker %s: %v", absent, err)
					}
				}
				paths = append(paths, "conflicted.txt")
			} else {
				put(t, stage, ".git/"+marker, head+"\n")
				paths = append(paths, ".git/"+marker)
			}
			put(t, stage, "pending.txt", "pending staged bytes")
			git(t, stage, "add", "pending.txt")
			put(t, stage, "pending.txt", "later unstaged bytes")
			before := map[string]string{}
			for _, path := range paths {
				data, err := os.ReadFile(filepath.Join(stage, path))
				if err != nil {
					t.Fatal(err)
				}
				before[path] = string(data)
			}
			ref, err := svc.CaptureArtifact(t.Context(), req)
			if !errors.Is(err, commitartifact.ErrConflict) || ref != "" {
				t.Fatalf("captured unfinished operation: %s %v", ref, err)
			}
			for _, store := range []artifact.Store{req.Store, commitartifact.SeedStore(req.Store)} {
				sealed, err := filepath.Glob(filepath.Join(store.Dir, "*.capture"))
				if err != nil || len(sealed) != 0 {
					t.Fatalf("retained bytes before refusing %s: %v %v", marker, sealed, err)
				}
			}
			for path, want := range before {
				got, err := os.ReadFile(filepath.Join(stage, path))
				if err != nil || string(got) != want {
					t.Fatalf("changed canonical %s: %v", path, err)
				}
			}
			if got := git(t, stage, "rev-parse", "HEAD"); got != head {
				t.Fatal("capture moved HEAD")
			}
		})
	}
}
