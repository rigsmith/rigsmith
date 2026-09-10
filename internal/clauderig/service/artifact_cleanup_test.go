package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestArtifactWorkspaceCleanupValidatesBindingAndSourceSeparation(t *testing.T) {
	for _, mode := range []string{"clean", "binding", "source-overlap", "staging-overlap", "busy"} {
		t.Run(mode, func(t *testing.T) {
			req := artifactCaptureFixture(t, "workspace cleanup fixture")
			inputs := service.QueueInputs{Sync: req.Sync, Profiles: req.Profiles, Captures: req.Store, Commits: artifact.Store{Dir: filepath.Join(t.TempDir(), "commits")}}
			for _, dir := range []string{req.Sync.StagingDir, req.Store.Dir, inputs.Commits.Dir} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			put(t, req.Store.Dir, ".capture-work-old/file", "scratch")
			put(t, req.Store.Dir, "retained.capture", "retained")
			put(t, req.Store.Dir, "merges/intent/sealed", "repair")
			put(t, req.Sync.StagingDir, "untouched", "staging")
			original, err := os.ReadFile(filepath.Join(req.Sync.StagingDir, "untouched"))
			if err != nil {
				t.Fatal(err)
			}
			binding := req.Binding
			switch mode {
			case "binding":
				binding.ConfigID = "changed"
			case "source-overlap":
				inputs.Commits.Dir = filepath.Join(os.Getenv("HOME"), ".claude")
			case "staging-overlap":
				inputs.Commits.Dir = filepath.Join(req.Sync.StagingDir, "scratch")
			case "busy":
				_, release, err := storelock.Acquire(t.Context(), req.Sync.StagingDir, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			result, err := (service.Service{}).CleanupArtifactWorkspaces(t.Context(), binding, inputs)
			if mode == "clean" {
				if err != nil || result.RemovedWorkspaces != 1 {
					t.Fatal(result, err)
				}
			} else {
				if err == nil || result.RemovedWorkspaces != 0 {
					t.Fatal(result, err)
				}
				if _, err := os.Stat(filepath.Join(req.Store.Dir, ".capture-work-old/file")); err != nil {
					t.Fatal("changed scratch on refusal", err)
				}
			}
			if mode == "binding" && !errors.Is(err, queue.ErrBinding) {
				t.Fatal(err)
			}
			for name, want := range map[string]string{"retained.capture": "retained", "merges/intent/sealed": "repair"} {
				if b, e := os.ReadFile(filepath.Join(req.Store.Dir, name)); e != nil || string(b) != want {
					t.Fatal("changed retained state", name, e)
				}
			}
			if b, e := os.ReadFile(filepath.Join(req.Sync.StagingDir, "untouched")); e != nil || string(b) != string(original) {
				t.Fatal("changed staging", e)
			}
		})
	}
}
