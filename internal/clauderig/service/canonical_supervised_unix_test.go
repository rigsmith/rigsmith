//go:build linux || darwin

package service_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestCanonicalSupervisorLossStopsCaptureAndLaterJournal(t *testing.T) {
	for _, afterCapture := range []bool{false, true} {
		name := "before-capture"
		if afterCapture {
			name = "after-capture"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("RIG_TEST_CANONICAL_SUPERVISOR_FAILURE", "0")
			req, q, request, _ := coverageFixture(t)
			enqueueCoverage(t, q, request)
			if _, err := gitrepo.Init(t.Context(), req.StagingDir); err != nil {
				t.Fatal(err)
			}
			if !afterCapture {
				t.Setenv("RIG_TEST_CANONICAL_SUPERVISOR_FAILURE", "1")
			}
			captured := false
			svc := service.Service{ReadIdentity: func() (service.Identity, error) {
				if !afterCapture {
					t.Fatal("capture followed lost supervisor")
				}
				return coverageIdentity, nil
			}, Observe: func(e service.Event) {
				if _, ok := e.(service.Captured); ok {
					captured = true
					t.Setenv("RIG_TEST_CANONICAL_SUPERVISOR_FAILURE", "1")
				}
			}}
			result, err := svc.SyncWithCoverage(canonicalSupervisor(t.Context()), req, q)
			if err == nil || captured != afterCapture || result.Sync.Publication.Committed {
				t.Fatalf("loss did not stop workflow: %+v %v", result, err)
			}
			pendingCoverage(t, q, 1)
			rows, err := journal.Read(req.StagingDir, 0)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if afterCapture {
				want = 1
			}
			if len(rows) != want {
				t.Fatalf("wrote journal after uncertainty: %d entries, want %d", len(rows), want)
			}
			if !afterCapture {
				if _, err := os.Stat(filepath.Join(req.StagingDir, "cli")); !os.IsNotExist(err) {
					t.Fatalf("capture changed staging: %v", err)
				}
			}
			if _, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0); !errors.Is(err, storelock.ErrFenced) {
				if release != nil {
					release()
				}
				t.Fatalf("lost supervisor did not fence replacement: %v", err)
			}
			if recovered, err := process.RecoverStore(t.Context(), req.StagingDir); err != nil || !recovered {
				t.Fatalf("prelaunch recovery: %v %v", recovered, err)
			}
			pendingCoverage(t, q, 1) // Recovery itself cannot acknowledge work.
			rows, err = journal.Read(req.StagingDir, 0)
			if err != nil || len(rows) != want {
				t.Fatalf("recovery changed journal: %v %v", rows, err)
			}
			t.Setenv("RIG_TEST_CANONICAL_SUPERVISOR_FAILURE", "0")
			retry := service.Service{ReadIdentity: func() (service.Identity, error) { return coverageIdentity, nil }}
			completed, err := retry.SyncWithCoverage(canonicalSupervisor(t.Context()), req, q)
			if err != nil || !completed.Sync.Publication.Pushed || len(completed.Acknowledged) != 1 {
				t.Fatalf("recovered workflow failed confirmation: %+v %v", completed, err)
			}
			pendingCoverage(t, q, 0)
		})
	}
}
