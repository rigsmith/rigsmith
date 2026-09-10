package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

func TestArtifactWorkspaceCleanupRevalidatesStagingPolicy(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Roots = nil
	cfg.ChunkTranscripts = nil
	inputs := QueueInputs{Sync: SyncRequest{Config: cfg, Machine: config.Machine{Name: "fixture", Home: root}, StagingDir: filepath.Join(root, "stage")}, Captures: artifact.Store{Dir: filepath.Join(root, "captures")}, Commits: artifact.Store{Dir: filepath.Join(root, "commits")}}
	for _, dir := range []string{inputs.Sync.StagingDir, inputs.Captures.Dir, inputs.Commits.Dir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	scratch := filepath.Join(inputs.Captures.Dir, ".capture-work-keep")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	binding, err := CaptureBinding(inputs.Sync, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Pause at the real preparation/acquisition boundary. A cooperating writer
	// changes the auto-chunking marker under staging ownership before cleanup.
	req, err := workspaceCleanupRequest(binding, inputs)
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := storelock.Acquire(t.Context(), inputs.Sync.StagingDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(filepath.Join(inputs.Sync.StagingDir, transcript.StorageFile), []byte(`{"version":1,"chunkedTranscripts":true}`), 0600)
	release()
	if err != nil {
		t.Fatal(err)
	}
	result, err := commitartifact.CleanupWorkspaces(t.Context(), req)
	if !errors.Is(err, queue.ErrBinding) || result.RemovedWorkspaces != 0 {
		t.Fatal("stale policy authorized cleanup", result, err)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal("removed scratch under stale binding", err)
	}
	// Fresh policy can authorize the same store without recreating work.
	current, err := CaptureBinding(inputs.Sync, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err = (Service{}).CleanupArtifactWorkspaces(t.Context(), current, inputs)
	if err != nil || result.RemovedWorkspaces != 1 {
		t.Fatal(result, err)
	}
}
