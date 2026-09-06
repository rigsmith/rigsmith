package service_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func TestCaptureCanPublishOfflineRetryWithoutReadingLiveSourcesAgain(t *testing.T) {
	req, root := syncFixture(t, "original capture")
	req.Config.Remote = filepath.Join(root, "remote.git")
	reads := 0
	svc := service.Service{ReadIdentity: func() (service.Identity, error) {
		reads++
		return service.Identity{AccountUUID: "11111111-1111-4111-8111-111111111111", Email: "fixture@example.com"}, nil
	}}
	// Keep one staging lease through capture and the attempted publication.
	ctx, release, err := storelock.Acquire(t.Context(), req.StagingDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	report, err := svc.Capture(ctx, req)
	if err != nil || report == nil {
		t.Fatalf("capture: %+v %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(req.StagingDir, ".git")); !os.IsNotExist(err) {
		t.Fatal("capture committed", err)
	}
	if _, err := devices.Load(req.StagingDir); err != nil {
		t.Fatal("capture omitted metadata", err)
	}
	entries, err := journal.Read(req.StagingDir, 0)
	if err != nil || len(entries) != 1 || entries[0].Outcome != journal.OutcomeOK {
		t.Fatalf("capture journal: %+v %v", entries, err)
	}
	// Deliberately change the source after capture. Publication must use the
	// captured tree, and retry must not invoke the identity reader or engine.
	source := filepath.Join(req.Machine.Home, ".claude", "projects", "-workspace-acme", "s.jsonl")
	if err := os.WriteFile(source, []byte("changed after capture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pub := service.PublishRequest{StagingDir: req.StagingDir, Remote: req.Config.Remote, MachineName: req.Machine.Name, Retention: req.Config.Retention}
	first, err := svc.Publish(ctx, pub)
	if err == nil || !first.Committed || first.Pushed {
		t.Fatalf("offline publish %+v %v", first, err)
	}
	head := git(t, req.StagingDir, "rev-parse", "HEAD")
	release()
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "--bare", "-b", "main", req.Config.Remote)
	second, err := svc.Publish(t.Context(), pub)
	if err != nil || second.Committed || !second.Pushed {
		t.Fatalf("retry %+v %v", second, err)
	}
	if got := git(t, req.Config.Remote, "rev-parse", "main"); got != head {
		t.Fatal("retry changed the captured commit")
	}
	content := git(t, req.Config.Remote, "show", "main:cli/projects/-workspace-acme/s.jsonl")
	if !strings.Contains(content, "original capture") || strings.Contains(content, "changed after capture") {
		t.Fatal("published different source bytes")
	}
	if reads != 1 {
		t.Fatalf("identity observed %d times", reads)
	}
}

func TestCaptureRefusesSecretBeforePublication(t *testing.T) {
	req, _ := syncFixture(t, "sk-ant-api03-"+strings.Repeat("z", 60))
	report, err := (service.Service{}).Capture(t.Context(), req)
	if err == nil || report == nil || len(report.Findings) == 0 {
		t.Fatalf("capture did not refuse: %+v %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(req.StagingDir, ".git")); !os.IsNotExist(err) {
		t.Fatal("refused capture committed", err)
	}
}
