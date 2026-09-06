package service_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func syncFixture(t *testing.T, text string) (service.SyncRequest, string) {
	t.Helper()
	root := fixture(t)
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := config.Default()
	cfg.Roots = cfg.Roots[:1] // Synthetic CLI root only.
	me := config.Detect("fixture")
	cfg.Machines[me.Name] = me
	body, err := json.Marshal(map[string]any{
		"type": "user", "sessionId": "s", "uuid": "s-message", "cwd": "/workspace/acme",
		"timestamp": "2026-01-02T03:04:05Z", "message": map[string]any{"role": "user", "content": text},
	})
	if err != nil {
		t.Fatal(err)
	}
	put(t, home, ".claude/projects/-workspace-acme/s.jsonl", string(body)+"\n")
	return service.SyncRequest{Config: cfg, Machine: me, StagingDir: filepath.Join(home, ".clauderig", "repo")}, root
}

func TestSyncObservesIdentityOnceBeforeFlushAndCommitsJournal(t *testing.T) {
	req, _ := syncFixture(t, "fixture")
	const accountID = "11111111-1111-4111-8111-111111111111"
	identity := service.Identity{AccountUUID: " " + accountID + " ", Email: "fixture@example.com"}
	reads, decodes := 0, 0
	var events []service.Event
	svc := service.Service{
		ReadIdentity: func() (service.Identity, error) { reads++; return identity, nil },
		Observe:      func(e service.Event) { events = append(events, e) },
	}
	req.ResolveFlush = func() service.FlushIntent {
		decodes++
		if reads != 1 {
			t.Fatal("flush input was decoded before the identity observation")
		}
		if _, err := os.Stat(filepath.Join(req.StagingDir, "cli")); !os.IsNotExist(err) {
			t.Fatalf("capture ran before flush decoding: %v", err)
		}
		// A login change after that point must not split ledger/device identity.
		identity = service.Identity{AccountUUID: "22222222-2222-4222-8222-222222222222", Email: "other@example.com"}
		return service.FlushIntent{Mode: service.FlushAll}
	}
	result, err := svc.Sync(t.Context(), req)
	if err != nil || result.Capture == nil || !result.Publication.Committed || result.Publication.Pushed {
		t.Fatalf("sync: %+v, %v", result, err)
	}
	if reads != 1 || decodes != 1 {
		t.Fatalf("identity reads=%d, flush decodes=%d", reads, decodes)
	}
	reg, err := devices.Load(req.StagingDir)
	if err != nil {
		t.Fatal(err)
	}
	device := reg.Devices[req.Machine.Name]
	if device.Account == nil || device.Account.AccountUUID != accountID || device.Account.Email != "fixture@example.com" {
		t.Fatalf("device used a different identity: %+v", device.Account)
	}
	entries := ledger.LoadAll(req.StagingDir)
	if len(entries) == 0 {
		t.Fatal("no ledger attribution was exercised")
	}
	for _, entry := range entries {
		if entry.Account != accountID {
			t.Fatalf("ledger used a different identity: %q", entry.Account)
		}
	}
	records, err := journal.Read(req.StagingDir, 0)
	if err != nil || len(records) != 1 || records[0].Outcome != journal.OutcomeOK {
		t.Fatalf("journal: %+v, %v", records, err)
	}
	committed := git(t, req.StagingDir, "show", "HEAD:journal/fixture.jsonl")
	if !strings.Contains(committed, `"outcome":"ok"`) || strings.Count(committed, "\n") != 0 {
		t.Fatalf("capture record did not travel in its own commit: %s", committed)
	}
	if status := git(t, req.StagingDir, "status", "--porcelain"); status != "" {
		t.Fatalf("successful sync left metadata uncommitted: %s", status)
	}
	if len(events) != 3 {
		t.Fatalf("sync events: %v", events)
	}
	if _, ok := events[0].(service.SyncStarted); !ok {
		t.Fatalf("first event: %T", events[0])
	}
	if _, ok := events[1].(service.Captured); !ok {
		t.Fatalf("second event: %T", events[1])
	}
	if _, ok := events[2].(service.Published); !ok {
		t.Fatalf("third event: %T", events[2])
	}
}

func TestSyncJournalFailureBoundaries(t *testing.T) {
	for _, name := range []string{"dry-run", "refused", "offline"} {
		t.Run(name, func(t *testing.T) {
			text := "fixture"
			if name == "refused" {
				text = "sk-ant-api03-" + strings.Repeat("z", 60)
			}
			req, root := syncFixture(t, text)
			req.DryRun = name == "dry-run"
			if name == "offline" {
				req.Config.Remote = filepath.Join(root, "missing.git")
			}
			result, err := (service.Service{}).Sync(t.Context(), req)
			if (err != nil) != (name != "dry-run") {
				t.Fatalf("sync error=%v", err)
			}
			records, rerr := journal.Read(req.StagingDir, 0)
			if rerr != nil {
				t.Fatal(rerr)
			}
			switch name {
			case "dry-run":
				if result.Capture == nil || len(records) != 0 || result.Publication.Committed {
					t.Fatalf("dry run skipped capture or journalled/published it: %+v, %+v", result, records)
				}
				for _, rel := range []string{".git", devices.FileName} {
					if _, err := os.Stat(filepath.Join(req.StagingDir, rel)); !os.IsNotExist(err) {
						t.Fatalf("dry run wrote %s: %v", rel, err)
					}
				}
			case "refused":
				if len(records) != 1 || records[0].Outcome != journal.OutcomeRefused || result.Publication.Committed {
					t.Fatalf("refusal was misclassified or double-journalled: %+v, %+v", result, records)
				}
				if _, err := os.Stat(filepath.Join(req.StagingDir, ".git")); !os.IsNotExist(err) {
					t.Fatalf("refusal entered publication: %v", err)
				}
			case "offline":
				if !result.Publication.Committed || result.Publication.Pushed || len(records) != 2 {
					t.Fatalf("offline phases/journal: %+v, %+v", result, records)
				}
				if records[0].Outcome != journal.OutcomeFailed || records[1].Outcome != journal.OutcomeOK {
					t.Fatalf("offline outcomes: %+v", records)
				}
				committed := git(t, req.StagingDir, "show", "HEAD:journal/fixture.jsonl")
				if strings.Contains(committed, `"outcome":"failed"`) {
					t.Fatal("Git-phase failure was retroactively placed in the earlier commit")
				}
			}
		})
	}
}

func TestSyncStopsUnsettledMergeBeforeIdentityOrCapture(t *testing.T) {
	req, root := syncFixture(t, "new source snapshot")
	git(t, root, "init", "-b", "main", req.StagingDir)
	put(t, req.StagingDir, "base.txt", "base\n")
	git(t, req.StagingDir, "add", ".")
	git(t, req.StagingDir, "commit", "-m", "base")
	git(t, req.StagingDir, "checkout", "-b", "other")
	put(t, req.StagingDir, "unsafe.txt", "sk-ant-api03-"+strings.Repeat("z", 60)+"\n")
	git(t, req.StagingDir, "add", ".")
	git(t, req.StagingDir, "commit", "-m", "remote")
	git(t, req.StagingDir, "checkout", "main")
	put(t, req.StagingDir, "local.txt", "local\n")
	git(t, req.StagingDir, "add", ".")
	git(t, req.StagingDir, "commit", "-m", "local")
	git(t, req.StagingDir, "merge", "--no-commit", "other")
	reads, decodes := 0, 0
	svc := service.Service{ReadIdentity: func() (service.Identity, error) { reads++; return service.Identity{}, nil }}
	req.ResolveFlush = func() service.FlushIntent { decodes++; return service.FlushIntent{} }
	result, err := svc.Sync(t.Context(), req)
	if err == nil || !strings.Contains(err.Error(), "still mid-merge") || result.Capture != nil || reads != 0 || decodes != 0 {
		t.Fatalf("capture passed an unsettled merge: result=%+v, err=%v, reads=%d, decodes=%d", result, err, reads, decodes)
	}
	if _, err := os.Stat(filepath.Join(req.StagingDir, "cli")); !os.IsNotExist(err) {
		t.Fatalf("source was captured over the unsettled merge: %v", err)
	}
	// A refusal remains resumable, rather than silently discarding the merge.
	cmd := exec.Command("git", "rev-parse", "--verify", "MERGE_HEAD")
	cmd.Dir = req.StagingDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("refused merge was lost: %v", err)
	}
}
