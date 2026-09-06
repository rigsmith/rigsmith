package service_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "no-global-config"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	t.Setenv("GIT_AUTHOR_NAME", "Fixture")
	t.Setenv("GIT_AUTHOR_EMAIL", "fixture@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Fixture")
	t.Setenv("GIT_COMMITTER_EMAIL", "fixture@example.com")
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-10T12:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-10T12:00:00Z")
	t.Setenv("GIT_ALLOW_PROTOCOL", "file")
	return root
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublishRetriesPendingCommitWithoutAnotherCapture(t *testing.T) {
	root := fixture(t)
	staging, remote := filepath.Join(root, "repo"), filepath.Join(root, "remote.git")
	put(t, staging, "cli/projects/p/s.jsonl", "{\"message\":\"fixture\"}\n")
	req := service.PublishRequest{StagingDir: staging, Remote: remote, MachineName: "fixture",
		Retention: config.Retention{FloorBytes: 1 << 30}}
	var events []service.Event
	svc := service.Service{Observe: func(e service.Event) { events = append(events, e) }}
	first, err := svc.Publish(t.Context(), req)
	if err == nil || !first.Committed || first.Pushed {
		t.Fatalf("offline publication: result=%+v, err=%v", first, err)
	}
	if len(events) != 0 {
		t.Fatalf("offline publication emitted success: %v", events)
	}
	head := git(t, staging, "rev-parse", "HEAD")
	git(t, root, "init", "--bare", "-b", "main", remote)
	second, err := svc.Publish(t.Context(), req)
	if err != nil || second.Committed || !second.Pushed {
		t.Fatalf("retry without capture: result=%+v, err=%v", second, err)
	}
	if got := git(t, remote, "rev-parse", "main"); got != head {
		t.Fatal("retry did not publish the original pending commit")
	}
	if len(events) != 1 {
		t.Fatalf("retry events: %v", events)
	}
	if e, ok := events[0].(service.Published); !ok || e.Result != second || e.LocalOnly {
		t.Fatalf("retry success event: %+v", events[0])
	}
}

func TestPublishRefusesSecretBeforeCommit(t *testing.T) {
	root := fixture(t)
	staging := filepath.Join(root, "repo")
	put(t, staging, "cli/projects/p/s.jsonl", "{\"message\":\"sk-ant-api03-"+strings.Repeat("z", 60)+"\"}\n")
	result, err := (service.Service{}).Publish(t.Context(), service.PublishRequest{StagingDir: staging, MachineName: "fixture"})
	if err == nil || result.Committed || result.Pushed {
		t.Fatalf("unsafe publication: result=%+v, err=%v", result, err)
	}
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = staging
	if err := cmd.Run(); err == nil {
		t.Fatal("refused content reached a commit")
	}
}

func TestPullFailedCloneDoesNotCreateJournalDestination(t *testing.T) {
	root := fixture(t)
	staging := filepath.Join(root, "repo")
	var events []service.Event
	svc := service.Service{Observe: func(e service.Event) { events = append(events, e) }}
	result := svc.Pull(t.Context(), service.PullRequest{StagingDir: staging,
		Config: &config.Config{Remote: filepath.Join(root, "missing.git")}})
	if result.CloneError == nil {
		t.Fatal("missing remote did not report a clone failure")
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("clone failure created a destination that blocks the next clone: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("clone failure events: %v", events)
	}
	if _, ok := events[0].(service.CloneSkipped); !ok {
		t.Fatalf("clone failure event: %T", events[0])
	}
}

func TestPublishMaintainsHistoryAfterSuccess(t *testing.T) {
	root := fixture(t)
	staging, remote := filepath.Join(root, "repo"), filepath.Join(root, "remote.git")
	git(t, root, "init", "--bare", "-b", "main", remote)
	git(t, root, "init", "-b", "main", staging)
	for i, date := range []string{"2026-01-01T12:00:00Z", "2026-01-02T12:00:00Z"} {
		t.Setenv("GIT_AUTHOR_DATE", date)
		t.Setenv("GIT_COMMITTER_DATE", date)
		put(t, staging, "cli/projects/p/s.jsonl", strings.Repeat("{\"message\":\"fixture\"}\n", i+1))
		git(t, staging, "add", ".")
		git(t, staging, "commit", "-m", "old capture")
	}
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-10T12:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-10T12:00:00Z")
	put(t, staging, "cli/settings.json", "{}\n")
	var events []service.Event
	svc := service.Service{
		Observe: func(e service.Event) { events = append(events, e) },
		Now:     func() time.Time { return time.Date(2026, 1, 12, 12, 0, 0, 0, time.Local) },
	}
	result, err := svc.Publish(t.Context(), service.PublishRequest{
		StagingDir: staging, Remote: remote, MachineName: "fixture",
		Retention: config.Retention{FloorBytes: 1, SquashFactor: 0.000001, SquashKeepDays: 3},
	})
	if err != nil || !result.Committed || !result.Pushed {
		t.Fatalf("publication with maintenance: %+v, %v", result, err)
	}
	if len(events) != 3 {
		t.Fatalf("maintenance events: %v", events)
	}
	if _, ok := events[0].(service.Published); !ok {
		t.Fatalf("first event = %T, want publication success", events[0])
	}
	if _, ok := events[1].(service.Repacking); !ok {
		t.Fatalf("second event = %T, want repack before squash", events[1])
	}
	folded, ok := events[2].(service.HistoryFolded)
	if !ok || folded.Count != 1 || folded.KeepDays != 3 || folded.Cutoff.Format("2006-01-02 15:04:05") != "2026-01-09 00:00:00" {
		t.Fatalf("history event: %+v", events[2])
	}
	if count := git(t, remote, "rev-list", "--count", "main"); count != "2" {
		t.Fatalf("remote retained %s commits, want old base plus recent capture", count)
	}
	if tree := git(t, remote, "ls-tree", "-r", "--name-only", "config-history"); strings.Contains(tree, "cli/projects/") || !strings.Contains(tree, "cli/settings.json") {
		t.Fatalf("config-history selection changed: %s", tree)
	}
	if git(t, remote, "rev-parse", "main") != git(t, staging, "rev-parse", "HEAD") {
		t.Fatal("maintained history did not reach the remote")
	}
}

func TestPullRejectsMissingConfigBeforeTouchingStore(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	var events []service.Event
	svc := service.Service{Observe: func(e service.Event) { events = append(events, e) }}
	result := svc.Pull(t.Context(), service.PullRequest{StagingDir: staging})
	if result.RequestError == nil || result.CloneError != nil || result.ReconcileError != nil || result.RestoreError != nil || result.Restore != nil {
		t.Fatalf("invalid request result: %+v", result)
	}
	if len(events) != 1 {
		t.Fatalf("failure events: %v", events)
	}
	if e, ok := events[0].(service.PullFailed); !ok || e.Err != result.RequestError {
		t.Fatalf("failure event: %+v", events[0])
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("invalid request touched the store: %v", err)
	}
}

func TestSyncRejectsMissingConfigBeforeRepairOrInput(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "repo")
	svc := service.Service{
		Observe: func(service.Event) { t.Fatal("invalid request started a workflow") },
		ReadIdentity: func() (service.Identity, error) {
			t.Fatal("invalid request read identity")
			return service.Identity{}, nil
		},
	}
	result, err := svc.Sync(t.Context(), service.SyncRequest{
		StagingDir: staging,
		ResolveFlush: func() service.FlushIntent {
			t.Fatal("invalid request read input")
			return service.FlushIntent{}
		},
	})
	if err == nil || result.Capture != nil || result.Publication.Committed || result.Publication.Pushed {
		t.Fatalf("invalid request: result=%+v, err=%v", result, err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("invalid request touched the store: %v", err)
	}
}
