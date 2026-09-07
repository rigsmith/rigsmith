package service_test

import (
	"bytes"
	"errors"
	"fmt"
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

func commitSnapshotAt(t *testing.T, dir, message string, sec int64) {
	t.Helper()
	stamp := fmt.Sprintf("@%d +0000", sec)
	t.Setenv("GIT_AUTHOR_DATE", stamp)
	t.Setenv("GIT_COMMITTER_DATE", stamp)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", message)
}

func snapshotPublicationFixture(t *testing.T, kind string) (service.ArtifactPublishRequest, *artifactRemote, string, string) {
	t.Helper()
	input, remote := publicationFixture(t, true, false)
	stage := input.Commit.Capture.Sync.StagingDir
	path := "cli/plugins/data/state.json"
	if kind == "unsupported" {
		path = "unknown/state.json"
	}
	if kind == "malformed-profile" {
		path = "desktop@a b/profile.json"
	}
	if kind != "add-add" {
		put(t, stage, path, "{\"value\":\"base\"}\n")
	} else {
		put(t, stage, "cli/plugins/data/base.json", "{}\n")
	}
	commitSnapshotAt(t, stage, "shared snapshot base", 100)
	git(t, stage, "push", remote.dir, "HEAD:main")
	incoming := filepath.Join(t.TempDir(), "incoming")
	git(t, filepath.Dir(incoming), "clone", "--branch", "main", remote.dir, incoming)
	ours, theirs := "{\"value\":\"ours\"}\r\n", "{\"value\":\"theirs\"}\n"
	ourTime, theirTime := int64(200), int64(300)
	if kind == "ours" {
		ourTime = 400
	}
	if kind == "tie" {
		theirTime = ourTime
	}
	if kind == "secret" || kind == "losing-remote-secret" {
		theirs = "{\"token\":\"ghp_" + strings.Repeat("z", 40) + "\"}\n"
	}
	if kind == "losing-local-secret" {
		ours = "{\"token\":\"ghp_" + strings.Repeat("z", 40) + "\"}\n"
	}
	if kind == "losing-remote-secret" {
		ourTime = 400
	}
	put(t, stage, path, ours)
	commitSnapshotAt(t, stage, "local snapshot", ourTime)
	put(t, incoming, path, theirs)
	commitSnapshotAt(t, incoming, "incoming snapshot", theirTime)
	git(t, incoming, "push", "origin", "HEAD:main")
	// A much newer local tip must not change this file's older source time.
	put(t, stage, "cli/plugins/data/unrelated.json", "{\"other\":true}\n")
	commitSnapshotAt(t, stage, "unrelated local work", 9000)
	want := theirs
	if kind == "ours" {
		want = ours
	}
	return input, remote, path, want
}

func TestPublishArtifactRecoversSnapshotConflicts(t *testing.T) {
	for _, kind := range []string{"ours", "theirs", "add-add", "tie", "secret", "losing-local-secret", "losing-remote-secret", "unsupported", "malformed-profile"} {
		t.Run(kind, func(t *testing.T) {
			input, remote, path, want := snapshotPublicationFixture(t, kind)
			stage := input.Commit.Capture.Sync.StagingDir
			read := func(p string) []byte {
				b, err := os.ReadFile(filepath.Join(stage, p))
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			before, head := git(t, remote.dir, "rev-parse", "main"), git(t, stage, "rev-parse", "HEAD")
			index, config, body := read(".git/index"), read(".git/config"), read(path)
			result, err := (service.Service{}).PublishArtifact(t.Context(), input)
			if head != git(t, stage, "rev-parse", "HEAD") || !bytes.Equal(index, read(".git/index")) || !bytes.Equal(config, read(".git/config")) || !bytes.Equal(body, read(path)) {
				t.Fatal("changed canonical staging")
			}
			if kind == "tie" || strings.Contains(kind, "secret") || kind == "unsupported" || kind == "malformed-profile" {
				reason := commitartifact.ErrConflict
				if strings.Contains(kind, "secret") {
					reason = engine.ErrSecretTripwire
				}
				if !errors.Is(err, reason) || result != (commitartifact.Publication{}) || remote.pushes != 0 || git(t, remote.dir, "rev-parse", "main") != before {
					t.Fatalf("unsafe snapshot publication: %+v %v", result, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// Read raw bytes through a clone to check CRLF and trailing newlines too.
			clone := filepath.Join(t.TempDir(), "clone")
			git(t, filepath.Dir(clone), "clone", "--branch", "main", remote.dir, clone)
			got, err := os.ReadFile(filepath.Join(clone, path))
			if err != nil || string(got) != want {
				t.Fatalf("wrong snapshot: %q %v", got, err)
			}
			// Both historical sides remain available even though one snapshot was selected.
			git(t, remote.dir, "merge-base", "--is-ancestor", head, result.RemoteCommit)
			git(t, remote.dir, "merge-base", "--is-ancestor", before, result.RemoteCommit)
			if _, err := (service.Service{}).PublishArtifact(t.Context(), input); err != nil || remote.pushes != 1 {
				t.Fatal("replay pushed again", err, remote.pushes)
			}
		})
	}
}

func TestQueueAdapterRetainedSnapshotRoundTrip(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("set CLAUDERIG_E2E=1; synthetic retained snapshot round trip")
	}
	input, remote, path, want := snapshotPublicationFixture(t, "theirs")
	req := input.Commit.Capture
	if err := os.RemoveAll(filepath.Join(req.Sync.Machine.Home, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(req.Store.Dir); err != nil {
		t.Fatal(err)
	}
	q, _, adapter := queueAdapterFixture(t, req, input.Commit.Commits, remote)
	seedQueuePhase(t, q, req, queue.Committed)
	result, err := q.RunOne(t.Context(), time.Now(), adapter)
	if err != nil || !result.Acknowledged || result.Phase != queue.Pushed {
		t.Fatalf("snapshot queue recovery: %+v %v", result, err)
	}
	if got := git(t, remote.dir, "show", "main:"+path); got != strings.TrimSpace(want) {
		t.Fatal("wrong ordered snapshot", got)
	}
	if got := git(t, remote.dir, "show", "main:cli/projects/-workspace-acme/s.jsonl"); !strings.Contains(got, "sealed publication bytes") {
		t.Fatal("lost sealed capture", got)
	}
	pushes := remote.pushes
	if _, err := q.RunOne(t.Context(), time.Now(), adapter); !errors.Is(err, queue.ErrEmpty) || remote.pushes != pushes {
		t.Fatal("acknowledged snapshot replayed", err, remote.pushes)
	}
}

func TestQueueAdapterRetainedSnapshotLosingSecret(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") != "1" {
		t.Skip("set CLAUDERIG_E2E=1; synthetic losing-snapshot tripwire")
	}
	input, remote, _, _ := snapshotPublicationFixture(t, "losing-local-secret")
	req := input.Commit.Capture
	q, _, adapter := queueAdapterFixture(t, req, input.Commit.Commits, remote)
	seedQueuePhase(t, q, req, queue.Committed)
	result, err := q.RunOne(t.Context(), time.Now(), adapter)
	if !errors.Is(err, engine.ErrSecretTripwire) || result.Acknowledged || result.Phase != queue.Committed || remote.pushes != 0 {
		t.Fatalf("secret-bearing history acknowledged: %+v %v", result, err)
	}
	work, err := q.Snapshot(t.Context())
	if err != nil || len(work) != 1 || work[0].Status != queue.Blocked || work[0].FailureCode != "scan-rejected" {
		t.Fatalf("scan refusal not retained: %+v %v", work, err)
	}
}
