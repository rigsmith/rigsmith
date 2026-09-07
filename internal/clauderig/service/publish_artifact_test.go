package service_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

type artifactRemote struct {
	dir, branch     string
	fetches, pushes int
	pushErr         error
	beforeFetch     func()
}

func (r *artifactRemote) Destination() (string, string) { return r.dir, r.branch }
func remoteGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	return strings.TrimSpace(string(b)), err
}
func (r *artifactRemote) Fetch(ctx context.Context, dir, ref string) (string, error) {
	r.fetches++
	if r.beforeFetch != nil {
		r.beforeFetch()
	}
	sha, err := remoteGit(ctx, r.dir, "for-each-ref", "--format=%(objectname)", "refs/heads/main")
	if err != nil || sha == "" {
		return sha, err
	}
	_, err = remoteGit(ctx, dir, "fetch", "--no-tags", "--", r.dir, "refs/heads/main:"+ref)
	return sha, err
}
func (r *artifactRemote) Push(ctx context.Context, dir, commit string) error {
	r.pushes++
	if _, err := remoteGit(ctx, dir, "push", "--", r.dir, commit+":refs/heads/main"); err != nil {
		return err
	}
	return r.pushErr
}
func publicationFixture(t *testing.T, seeded, auto bool) (service.ArtifactPublishRequest, *artifactRemote) {
	t.Helper()
	req := artifactCaptureFixture(t, "sealed publication bytes")
	if seeded {
		svc := service.Service{ReadIdentity: func() (service.Identity, error) { return req.Identity, nil }}
		if _, err := svc.Sync(t.Context(), req.Sync); err != nil {
			t.Fatal(err)
		}
	}
	remote := &artifactRemote{dir: filepath.Join(t.TempDir(), "remote.git"), branch: "main"}
	git(t, filepath.Dir(remote.dir), "init", "--bare", remote.dir)
	req.Sync.Config.Remote = remote.dir
	if auto {
		req.Sync.Config.ChunkTranscripts = nil
		put(t, req.Sync.StagingDir, "clauderig-storage.json", `{"version":1,"chunkedTranscripts":true}`)
	}
	var err error
	req.Binding, err = service.CaptureBinding(req.Sync, req.Profiles)
	if err != nil {
		t.Fatal(err)
	}
	req.Work.CaptureRef, err = (service.Service{}).CaptureArtifact(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Work.Phase = queue.Captured
	input := service.ArtifactCommitRequest{Capture: req, Commits: artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}}
	input.Capture.Work.CommitRef, err = (service.Service{}).CommitArtifact(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Capture.Work.Phase = queue.Committed
	return service.ArtifactPublishRequest{Commit: input, Remote: remote}, remote
}

func TestPublishArtifactPreservesCanonicalWorkAndConfirmsReplay(t *testing.T) {
	input, remote := publicationFixture(t, true, false)
	stage := input.Commit.Capture.Sync.StagingDir
	put(t, stage, "newer.txt", "newer committed work")
	git(t, stage, "add", "newer.txt")
	git(t, stage, "commit", "-m", "newer")
	head := git(t, stage, "rev-parse", "HEAD")
	put(t, stage, "pending.txt", "pending index")
	git(t, stage, "add", "pending.txt")
	put(t, stage, "pending.txt", "pending worktree")
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(stage, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	index, config, work := read(".git/index"), read(".git/config"), read("pending.txt")
	remote.pushErr = errors.New("lost push response")
	remote.beforeFetch = func() {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		_, release, err := storelock.Acquire(ctx, stage, time.Second)
		if err == nil {
			release()
			t.Fatal("staging lease not held during transport")
		}
	}
	svc := service.Service{ReadIdentity: func() (service.Identity, error) { t.Fatal("read worker login"); return service.Identity{}, nil }}
	result, err := svc.PublishArtifact(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.CaptureCommit == "" || result.RemoteCommit == "" || remote.pushes != 1 {
		t.Fatalf("unconfirmed: %+v %+v", result, remote)
	}
	if got := git(t, remote.dir, "show", "main:newer.txt"); got != "newer committed work" {
		t.Fatal(got)
	}
	if got := git(t, remote.dir, "ls-tree", "--name-only", "main"); strings.Contains(got, "pending.txt") {
		t.Fatal("published pending changes")
	}
	if read(".git/index") != index || read(".git/config") != config || read("pending.txt") != work || git(t, stage, "rev-parse", "HEAD") != head {
		t.Fatal("canonical state changed")
	}
	// Retained publication survives source/capture/staging removal and a lost queue marker.
	remote.beforeFetch = nil
	for _, dir := range []string{stage, input.Commit.Capture.Store.Dir, filepath.Join(input.Commit.Capture.Sync.Machine.Home, ".claude")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	again, err := svc.PublishArtifact(t.Context(), input)
	if err != nil || again != result || remote.pushes != 1 {
		t.Fatalf("replay: %+v %v pushes=%d", again, err, remote.pushes)
	}
}

func TestPublishArtifactAutoRecoveryWithoutLiveInputs(t *testing.T) {
	input, remote := publicationFixture(t, false, true)
	for _, dir := range []string{input.Commit.Capture.Sync.StagingDir, input.Commit.Capture.Store.Dir, filepath.Join(input.Commit.Capture.Sync.Machine.Home, ".claude")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (service.Service{}).PublishArtifact(t.Context(), input); err != nil || remote.pushes != 1 {
		t.Fatalf("auto recovery: %v", err)
	}
}

func TestPublishArtifactRejectsInvalidWorkBeforeTransport(t *testing.T) {
	for _, mode := range []string{"binding", "provenance", "phase", "capture-key", "missing-commit", "other-commit", "other-batch", "corrupt-commit", "overlap", "remote", "branch", "no-remote", "merge"} {
		t.Run(mode, func(t *testing.T) {
			input, remote := publicationFixture(t, true, false)
			switch mode {
			case "binding":
				input.Commit.Capture.Sync.Config.Remote = "changed"
			case "provenance":
				input.Commit.Capture.Identity = service.Identity{}
			case "phase":
				input.Commit.Capture.Work.Phase = queue.Captured
			case "capture-key":
				input.Commit.Capture.Work.CaptureRef = artifact.Key([]byte("other")) + ":" + strings.Repeat("0", 64)
			case "missing-commit":
				if err := os.RemoveAll(input.Commit.Commits.Dir); err != nil {
					t.Fatal(err)
				}
			case "other-commit":
				input.Commit.Capture.Work.CommitRef = artifact.Key([]byte("other")) + ":" + strings.Repeat("0", 64)
			case "other-batch":
				other, _ := publicationFixture(t, false, false)
				input.Commit.Commits = other.Commit.Commits
				input.Commit.Capture.Work.CommitRef = other.Commit.Capture.Work.CommitRef
			case "corrupt-commit":
				paths, err := filepath.Glob(filepath.Join(input.Commit.Commits.Dir, "*.capture"))
				if err != nil || len(paths) != 1 {
					t.Fatalf("artifact fixture: %v %v", paths, err)
				}
				if err := os.WriteFile(paths[0], []byte("corrupt artifact"), 0600); err != nil {
					t.Fatal(err)
				}
			case "overlap":
				input.Commit.Commits.Dir = input.Commit.Capture.Store.Dir
			case "remote":
				remote.dir = t.TempDir()
			case "branch":
				remote.branch = "other"
			case "no-remote":
				input.Remote = nil
			case "merge":
				put(t, input.Commit.Capture.Sync.StagingDir, ".git/MERGE_HEAD", git(t, input.Commit.Capture.Sync.StagingDir, "rev-parse", "HEAD")+"\n")
			}
			result, err := (service.Service{}).PublishArtifact(t.Context(), input)
			if err == nil || result != (commitartifact.Publication{}) || remote.fetches != 0 || remote.pushes != 0 {
				t.Fatalf("accepted invalid work: %+v %v %+v", result, err, remote)
			}
		})
	}
}

func TestPublishArtifactRejectsUnsafeLocalAndRemoteTrees(t *testing.T) {
	for _, mode := range []string{"local-attributes", "local-secret", "remote-attributes", "remote-secret"} {
		t.Run(mode, func(t *testing.T) {
			input, remote := publicationFixture(t, true, false)
			svc := service.Service{}
			if strings.HasPrefix(mode, "remote") {
				if _, err := svc.PublishArtifact(t.Context(), input); err != nil {
					t.Fatal(err)
				}
			}
			stage := input.Commit.Capture.Sync.StagingDir
			if strings.HasPrefix(mode, "remote") {
				stage = filepath.Join(t.TempDir(), "clone")
				git(t, filepath.Dir(stage), "clone", "--branch", "main", remote.dir, stage)
				git(t, stage, "config", "user.name", "fixture")
				git(t, stage, "config", "user.email", "fixture@example.com")
			}
			if strings.HasSuffix(mode, "attributes") {
				put(t, stage, "unsafe/.gitattributes", "* text\n")
				put(t, stage, "unsafe/file.txt", "bytes")
			} else {
				put(t, stage, "unsafe.txt", "token=ghp_"+strings.Repeat("a", 36)+"\n")
			}
			git(t, stage, "add", ".")
			git(t, stage, "commit", "-m", "unsafe fixture")
			if strings.HasPrefix(mode, "remote") {
				git(t, stage, "push", "origin", "HEAD:main")
			}
			pushes := remote.pushes
			result, err := svc.PublishArtifact(t.Context(), input)
			if err == nil || result != (commitartifact.Publication{}) || remote.pushes != pushes {
				t.Fatalf("unsafe tree published/acknowledged: %+v %v", result, err)
			}
		})
	}
}

func TestPublishArtifactSHA256(t *testing.T) {
	t.Setenv("GIT_DEFAULT_HASH", "sha256")
	input, remote := publicationFixture(t, true, false)
	result, err := (service.Service{}).PublishArtifact(t.Context(), input)
	if err != nil || len(result.CaptureCommit) != 64 || len(result.RemoteCommit) != 64 || remote.pushes != 1 {
		t.Fatalf("SHA-256: %+v %v", result, err)
	}
}

func TestPublishArtifactCancellationReleasesStaging(t *testing.T) {
	input, remote := publicationFixture(t, true, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	remote.beforeFetch = cancel
	result, err := (service.Service{}).PublishArtifact(ctx, input)
	if !errors.Is(err, context.Canceled) || result != (commitartifact.Publication{}) || remote.pushes != 0 {
		t.Fatalf("cancellation: %+v %v", result, err)
	}
	_, release, err := storelock.Acquire(t.Context(), input.Commit.Capture.Sync.StagingDir, time.Second)
	if err != nil {
		t.Fatal("staging lease retained", err)
	}
	release()
	work, err := filepath.Glob(filepath.Join(input.Commit.Commits.Dir, ".publication-*"))
	if err != nil || len(work) != 0 {
		t.Fatalf("private workspace retained: %v %v", work, err)
	}
}

func TestPublishArtifactRejectsForeignPolicyForSameCapture(t *testing.T) {
	for _, field := range []string{"policy", "name", "email", "time", "message"} {
		t.Run(field, func(t *testing.T) {
			input, remote := publicationFixture(t, false, false)
			capture := input.Commit.Capture
			request := commitartifact.Request{
				Captures: capture.Store, Commits: input.Commit.Commits, CaptureRef: capture.Work.CaptureRef,
				PolicyID:   "claude-retained-commit-v1",
				Message:    adapter.PublicationPlan(capture.Sync.Machine.Name, capture.Sync.Config.Retention).SnapshotMessage,
				AuthorName: "clauderig", AuthorEmail: "clauderig@localhost",
				Time:    capture.Work.Events[len(capture.Work.Events)-1].EnqueuedAt,
				Prepare: backupgit.EnsureContext, Audit: engine.CheckPublishContext,
			}
			original, err := commitartifact.Build(t.Context(), request)
			if err != nil || original != capture.Work.CommitRef {
				t.Fatalf("fixture policy drift: %s %v", original, err)
			}
			switch field {
			case "policy":
				request.PolicyID = "foreign-policy"
			case "name":
				request.AuthorName = "foreign author"
			case "email":
				request.AuthorEmail = "foreign@example.com"
			case "time":
				request.Time = request.Time.Add(time.Second)
			case "message":
				request.Message = "foreign commit"
			}
			foreign, err := commitartifact.Build(t.Context(), request)
			if err != nil || foreign == original {
				t.Fatalf("foreign fixture: %s %v", foreign, err)
			}
			info, err := commitartifact.Open(t.Context(), input.Commit.Commits, foreign, filepath.Join(t.TempDir(), "foreign"))
			if err != nil || info.CaptureRef != capture.Work.CaptureRef {
				t.Fatalf("invalid fixture: %+v %v", info, err)
			}
			input.Commit.Capture.Work.CommitRef = foreign
			result, err := (service.Service{}).PublishArtifact(t.Context(), input)
			if !errors.Is(err, queue.ErrBinding) || result != (commitartifact.Publication{}) || remote.fetches != 0 || remote.pushes != 0 {
				t.Fatalf("foreign %s accepted: %+v %v fetches=%d pushes=%d", field, result, err, remote.fetches, remote.pushes)
			}
		})
	}
}

func TestPublishArtifactWithGitTransport(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			input, remote := publicationFixture(t, true, false)
			constructor := commitartifact.NewGitTransport
			if configured {
				constructor = commitartifact.NewConfiguredGitTransport
			}
			transport, err := constructor(commitartifact.GitTransportOptions{Remote: remote.dir, Branch: remote.branch})
			if err != nil {
				t.Fatal(err)
			}
			input.Remote = transport
			result, err := (service.Service{}).PublishArtifact(t.Context(), input)
			if err != nil || result.RemoteCommit == "" {
				t.Fatalf("bound transport: %+v %v", result, err)
			}
			if got := git(t, remote.dir, "show", "main:cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(got, "sealed publication bytes") {
				t.Fatal("native captured bytes missing")
			}
		})
	}
}

func TestPublishArtifactResolvesMetadataAndAuditsResult(t *testing.T) {
	for _, kind := range []string{"union", "unknown-field", "secret"} {
		t.Run(kind, func(t *testing.T) {
			input, remote := publicationFixture(t, true, false)
			stage := input.Commit.Capture.Sync.StagingDir
			writeJSON := func(root, path string, value any) {
				b, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				put(t, root, path, string(b)+"\n")
			}
			now := time.Now().UTC()
			retired := devices.Device{Name: "retired", LastSync: now.Add(-time.Hour)}
			writeJSON(stage, devices.FileName, devices.Registry{Schema: 1, Devices: map[string]devices.Device{"retired": retired}})
			removedLink := "projects/worktree/memory"
			oldTarget := "projects/main/memory"
			var baseManifest manifest.Manifest
			if err := json.Unmarshal([]byte(git(t, stage, "show", "HEAD:"+manifest.FileName)), &baseManifest); err != nil {
				t.Fatal(err)
			}
			baseManifest.Links = map[string]string{removedLink: oldTarget}
			writeJSON(stage, manifest.FileName, baseManifest)
			git(t, stage, "add", devices.FileName, manifest.FileName)
			git(t, stage, "commit", "-m", "base metadata")
			git(t, stage, "push", remote.dir, "HEAD:refs/heads/main")
			incoming := filepath.Join(t.TempDir(), "incoming")
			git(t, filepath.Dir(incoming), "clone", "--branch", "main", remote.dir, incoming)
			oursPath := "/ours"
			if kind == "secret" {
				oursPath = "ghp_" + strings.Repeat("z", 40)
			}
			localManifest := manifest.Manifest{Schema: 1, SourceOS: "linux", Projects: map[string]manifest.Project{"ours": {Cwd: oursPath}, "shared": {Cwd: "/ours/shared"}}}
			remoteManifest := manifest.Manifest{Schema: 1, SourceOS: "windows", Projects: map[string]manifest.Project{"theirs": {Cwd: "/theirs"}, "shared": {Cwd: "/theirs/shared"}}}

			writeJSON(stage, manifest.FileName, localManifest)
			remoteManifest.Links = map[string]string{removedLink: oldTarget}
			writeJSON(incoming, manifest.FileName, remoteManifest)
			if kind == "unknown-field" {
				put(t, incoming, manifest.FileName, "{\"schema\":1,\"sourceOS\":\"windows\",\"projects\":{},\"future\":true}\n")
			}
			writeJSON(stage, devices.FileName, devices.Registry{Schema: 1, Devices: map[string]devices.Device{
				"ours": {Name: "ours"}, "shared": {Name: "shared", LastSync: now, Account: &devices.Account{Email: "fixture@example.com"}},
			}})
			writeJSON(incoming, devices.FileName, devices.Registry{Schema: 1, Devices: map[string]devices.Device{
				"retired": retired, "theirs": {Name: "theirs"}, "shared": {Name: "shared", LastSync: now.Add(time.Hour)},
			}})
			git(t, stage, "add", ".")
			git(t, stage, "commit", "-m", "local metadata")
			git(t, incoming, "add", ".")
			git(t, incoming, "commit", "-m", "remote metadata")
			git(t, incoming, "push", "origin", "HEAD:main")
			before := git(t, remote.dir, "rev-parse", "main")
			localHead := git(t, stage, "rev-parse", "HEAD")
			canonical := func(path string) []byte {
				b, err := os.ReadFile(filepath.Join(stage, path))
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			index, cfg, content := canonical(".git/index"), canonical(".git/config"), canonical(manifest.FileName)
			result, err := (service.Service{}).PublishArtifact(t.Context(), input)
			if kind != "union" {
				want := commitartifact.ErrConflict
				if kind == "secret" {
					want = engine.ErrSecretTripwire
				}
				if !errors.Is(err, want) || result != (commitartifact.Publication{}) || remote.pushes != 0 || git(t, remote.dir, "rev-parse", "main") != before {
					t.Fatalf("unsafe merge published: %+v %v pushes=%d", result, err, remote.pushes)
				}
				return
			}
			if err != nil || result.RemoteCommit == "" {
				t.Fatalf("metadata publication: %+v %v", result, err)
			}
			var merged manifest.Manifest
			if err := json.Unmarshal([]byte(git(t, remote.dir, "show", "main:"+manifest.FileName)), &merged); err != nil {
				t.Fatal(err)
			}
			if _, restored := merged.Links[removedLink]; restored {
				t.Fatal("restored removed link", merged.Links)
			}
			if len(merged.Projects) != 3 || merged.Projects["shared"].Cwd != "/ours/shared" || merged.Projects["theirs"].Cwd != "/theirs" {
				t.Fatalf("lost projects: %+v", merged)
			}
			var registry devices.Registry
			if err := json.Unmarshal([]byte(git(t, remote.dir, "show", "main:"+devices.FileName)), &registry); err != nil {
				t.Fatal(err)
			}
			if registry.Has("retired") || len(registry.Devices) != 3 || registry.Devices["shared"].Account == nil || registry.Devices["shared"].Account.Email != "fixture@example.com" || !registry.Devices["shared"].LastSync.Equal(now.Add(time.Hour)) {
				t.Fatalf("lost device provenance: %+v", registry)
			}
			if localHead != git(t, stage, "rev-parse", "HEAD") || !bytes.Equal(index, canonical(".git/index")) || !bytes.Equal(cfg, canonical(".git/config")) || !bytes.Equal(content, canonical(manifest.FileName)) {
				t.Fatal("changed canonical staging")
			}
			if _, err := (service.Service{}).PublishArtifact(t.Context(), input); err != nil || remote.pushes != 1 {
				t.Fatal("replay pushed again", err, remote.pushes)
			}
		})
	}
}
