package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/journal"
)

// A refusal is the record the journal exists for, and on refusal Sync used to
// return before Publish, so a fresh clone could not see that a machine had been
// refusing. The record alone travels now — this machine's file, scanned, and
// nothing else under journal/ with it.
func TestARefusalPublishesItsOwnJournalRecordAndNothingElse(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	codex := filepath.Join(home, ".codex")
	rollout := filepath.Join(codex, "sessions", "2026", "09", "05", "rollout-2026-09-05T11-22-59-01a0722a-7356-7592-922a-336289bdc101.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o755); err != nil {
		t.Fatal(err)
	}
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBAK\n-----END RSA PRIVATE KEY-----\n"
	body := `{"type":"session_meta","payload":{"session_id":"01a0722a","cwd":"/x"}}` + "\n" +
		`{"type":"response_item","payload":{"text":"` + strings.ReplaceAll(pem, "\n", "\\n") + `"}}` + "\n"
	if err := os.WriteFile(rollout, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "config.toml"), []byte("model = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bare := filepath.Join(t.TempDir(), "remote.git")
	init := exec.CommandContext(ctx, "git", "init", "--bare", "-b", Branch, bare)
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	staging := filepath.Join(t.TempDir(), "repo")
	if _, err := gitrepo.Init(ctx, staging); err != nil {
		t.Fatal(err)
	}
	// Something an older run or a merge left under journal/: the refusal
	// path must not sweep it up.
	leak := filepath.Join(staging, journal.DirName, "leak.jsonl")
	if err := os.MkdirAll(filepath.Dir(leak), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leak, []byte(`{"token":"ghp_`+strings.Repeat("a", 40)+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.SyncSessions = true
	cfg.RedactTranscripts = false
	cfg.Remote = bare
	m := config.Machine{Name: "mbp", OS: config.OSToken(), Home: home}
	cfg.Machines[m.Name] = m

	_, err := Service{}.Sync(ctx, SyncRequest{Config: cfg, Machine: m, StagingDir: staging})
	if err == nil {
		t.Fatal("a rollout holding a private key was not refused")
	}

	clone := filepath.Join(t.TempDir(), "clone")
	if _, err := gitrepo.Clone(ctx, bare, clone); err != nil {
		t.Fatalf("clone: %v", err)
	}
	rec, err := os.ReadFile(filepath.Join(clone, filepath.FromSlash(journal.RelPathFor("mbp"))))
	if err != nil {
		t.Fatalf("the refusal record did not reach the remote: %v", err)
	}
	if !strings.Contains(string(rec), "mbp") {
		t.Errorf("record does not name the machine: %s", rec)
	}
	if _, err := os.Stat(filepath.Join(clone, journal.DirName, "leak.jsonl")); !os.IsNotExist(err) {
		t.Error("the refusal path published a file it never wrote")
	}
	if _, err := os.Stat(filepath.Join(clone, "cli", "config.toml")); !os.IsNotExist(err) {
		t.Error("the refusal path published content the gate had refused")
	}
}
