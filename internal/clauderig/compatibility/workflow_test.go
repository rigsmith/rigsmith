package compatibility

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	project      = "projects/-workspace-acme/"
	live         = "home/.claude/"
	stage        = "home/.clauderig/repo/"
	account      = "11111111-1111-4111-8111-111111111111"
	otherAccount = "22222222-2222-4222-8222-222222222222"
	org          = "33333333-3333-4333-8333-333333333333"
)

func TestWorkflowCompatibility(t *testing.T) {
	base, next := binaries(t)
	for _, tc := range []struct {
		name string
		run  func(*sandbox)
	}{
		{"round-trip-profiles-and-attribution", roundTrip},
		{"dry-run-and-tripwire", dryRunAndTripwire},
		{"offline-push-retry", offlineRetry},
		{"abandoned-merge-recovery", mergeRecovery},
		{"retention-and-hook-flush", retentionAndFlush},
		{"fresh-machine-pull", freshPull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// Equal-length paths keep original transcript byte counts comparable.
			a := newSandbox(t, filepath.Join(root, "base"), base)
			b := newSandbox(t, filepath.Join(root, "next"), next)
			t.Run("baseline", func(t *testing.T) { a.t = t; tc.run(a) })
			if t.Failed() {
				return
			}
			t.Run("candidate", func(t *testing.T) { b.t = t; tc.run(b) })
			if t.Failed() {
				return
			}
			compare(t, a.observations, b.observations)
		})
	}
}

func session(id, text string) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "sessionId": id, "uuid": id + "-message", "cwd": "/workspace/acme", "timestamp": "2026-01-02T03:04:05Z", "message": map[string]any{"role": "user", "content": text}})
	return string(b) + "\n"
}

func seed(s *sandbox) {
	s.put(live+"settings.json", `{"effortLevel":"high","apiKey":"fixture-local-secret"}`+"\n")
	s.put(live+"skills/demo/SKILL.md", "demo skill\n")
	s.put(live+project+"s.jsonl", session("s", "remember this fixture"))
	s.put(live+project+"s/subagents/agent-a.jsonl", session("agent-a", "subagent fixture"))
	s.put(live+project+"memory/MEMORY.md", "- [topic](topic.md) durable memory\n")
	s.put(live+"statsig/cache", "excluded cache\n")
	s.put(live+project+"file-history/old", "excluded history\n")
}

func roundTrip(s *sandbox) {
	seed(s)
	s.json("home/.claude.json", map[string]any{"oauthAccount": map[string]any{"accountUuid": account, "organizationUuid": org, "emailAddress": "fixture@example.com"}})
	// A large chunked session plus a separate CLI-only session whose attribution
	// comes from the current login, while Desktop has stronger provenance for s.
	body := session("large", strings.Repeat("ordinary filler ", 600000))
	s.put(live+project+"large.jsonl", body)
	for _, name := range []string{"work", "personal"} {
		root := "home/.clauderig/desktop/" + name + "/"
		s.json(root+"profile.json", map[string]any{"name": name, "email": name + "@example.com"})
		s.json(root+"data/claude-code-sessions/"+otherAccount+"/"+org+"/local_s.json", map[string]any{"cliSessionId": "s", "cwd": "/workspace/acme", "title": "Desktop fixture"})
		s.put(root+"data/Cookies", "local profile cookie fixture")
	}
	s.run("sync", "", 0, "synced & pushed", "sync")
	index := s.read(stage + projectPath("large.jsonl"))
	if !strings.Contains(index, "clauderig") || len(index) > 4096 {
		s.t.Fatal("large transcript did not become a storage index")
	}
	s.absent(stage + "cli/statsig/cache")
	s.absent(stage + projectPath("file-history/old"))
	for _, name := range []string{"work", "personal"} {
		s.absent(stage + "desktop@" + name + "/data/Cookies")
		if !strings.Contains(s.read(stage+"desktop@"+name+"/profile.json"), name) {
			s.t.Fatal("profile metadata lost")
		}
	}
	rows := s.read(stage + "index/fixture.jsonl")
	for _, want := range []string{account, otherAccount, `"accountSource":"desktop"`, `"accountSource":"sync"`} {
		if !strings.Contains(rows, want) {
			s.t.Fatalf("ledger missing %s: %s", want, rows)
		}
	}
	if s.read(live+project+"large.jsonl") != body {
		s.t.Fatal("sync changed live transcript")
	}
	s.snapshot("synced")
	// Existing local secrets survive an in-place restore, and --backup captures
	// the prior tree before prune removes a stale allowed file.
	s.put(live+"settings.json", `{"effortLevel":"low","apiKey":"target-local-secret"}`+"\n")
	s.put(live+"skills/stale/SKILL.md", "stale skill\n")
	s.run("restore-refusal", "", 1, "not empty", "restore")
	s.run("restore", "", 0, "restored", "restore", "--backup", "--prune")
	s.absent(live + "skills/stale/SKILL.md")
	if s.read("home/.claude.bak/skills/stale/SKILL.md") != "stale skill\n" {
		s.t.Fatal("backup lost prior file")
	}
	settings := s.read(live + "settings.json")
	if !strings.Contains(settings, "target-local-secret") || !strings.Contains(settings, "high") {
		s.t.Fatal("restore lost local secret or synced setting")
	}
	if s.read(live+project+"large.jsonl") != body {
		s.t.Fatal("chunked restore changed bytes")
	}
	s.absent(live + project + "large.jsonl.chunks")
	s.snapshot("restored")
}

func projectPath(rel string) string { return "cli/" + project + rel }

func dryRunAndTripwire(s *sandbox) {
	seed(s)
	s.run("dry-run", "", 0, "staged + scanned, not committing", "sync", "--dry-run")
	s.absent(stage + ".git")
	if s.read(stage+"cli/skills/demo/SKILL.md") != "demo skill\n" {
		s.t.Fatal("dry-run no longer stages")
	}
	s.snapshot("dry-run")
	s.run("initial", "", 0, "synced & pushed", "sync")
	head := s.git(s.stage, "rev-parse", "HEAD")
	key := "ghp_" + strings.Repeat("z", 40)
	s.put(live+project+"s.jsonl", session("s", key))
	s.run("tripwire", "", 1, "Secret tripwire", "sync")
	if s.git(s.stage, "rev-parse", "HEAD") != head || s.git(s.remote, "rev-parse", "main") != head {
		s.t.Fatal("tripwire advanced published history")
	}
	s.snapshot("refused")
	s.put(live+project+"s.jsonl", session("s", "clean replacement"))
	s.run("clean-retry", "", 0, "synced & pushed", "sync")
	if strings.Contains(s.read(stage+"index/fixture.jsonl"), `"account":`) {
		s.t.Fatal("unknown login acquired an account attribution")
	}
	s.snapshot("retried")
}

func offlineRetry(s *sandbox) {
	seed(s)
	s.run("initial", "", 0, "synced & pushed", "sync")
	before := s.git(s.remote, "rev-parse", "main")
	offline := s.remote + ".offline"
	must(s.t, os.Rename(s.remote, offline))
	s.put(live+"skills/demo/SKILL.md", "changed while offline\n")
	s.run("offline", "", 1, "", "sync")
	committed := s.git(s.stage, "rev-parse", "HEAD")
	if committed == before {
		s.t.Fatal("offline sync failed to retain local commit")
	}
	must(s.t, os.Rename(offline, s.remote))
	if s.git(s.remote, "rev-parse", "main") != before {
		s.t.Fatal("offline run changed remote")
	}
	s.snapshot("offline")
	// No source edits between failure and retry. A journal/device metadata commit
	// may still be made; the contract is that the pending payload reaches origin.
	s.run("retry", "", 0, "pushed", "sync")
	s.git(s.remote, "merge-base", "--is-ancestor", committed, "main")
	if s.git(s.remote, "show", "main:cli/skills/demo/SKILL.md") != "changed while offline" {
		s.t.Fatal("retry did not publish pending payload")
	}
	s.snapshot("recovered")
}

func mergeRecovery(s *sandbox) {
	seed(s)
	s.run("initial", "", 0, "synced & pushed", "sync")
	s.git(s.stage, "checkout", "-b", "incoming")
	s.put(stage+"cli/settings.json", `{"effortLevel":"high","remoteSetting":true}`+"\n")
	s.git(s.stage, "add", ".")
	s.git(s.stage, "commit", "-m", "incoming settings")
	s.git(s.stage, "checkout", "main")
	s.put(stage+"cli/settings.json", `{"effortLevel":"high","localSetting":true}`+"\n")
	s.git(s.stage, "add", ".")
	s.git(s.stage, "commit", "-m", "local settings")
	// Leave a genuine conflicted index for the command to repair on entry.
	if err := execMerge(s); err == nil {
		s.t.Fatal("fixture did not create a conflicted merge")
	}
	if _, err := os.Stat(filepath.Join(s.stage, ".git", "MERGE_HEAD")); err != nil {
		s.t.Fatal(err)
	}
	s.run("repair", "", 0, "pushed", "sync")
	s.absent(stage + ".git/MERGE_HEAD")
	merged := s.git(s.stage, "show", "HEAD^:cli/settings.json")
	// Settings use the existing whole-snapshot policy, with incoming winning
	// equal commit timestamps. They are not a field-wise JSON union.
	if !strings.Contains(merged, "remoteSetting") || strings.Contains(merged, "localSetting") || len(strings.Fields(s.git(s.stage, "rev-list", "--parents", "-n", "1", "HEAD^"))) != 3 {
		s.t.Fatalf("merge was not settled before snapshot: %s", merged)
	}
	s.snapshot("repaired")
}

func execMerge(s *sandbox) error {
	cmd := exec.CommandContext(s.t.Context(), "git", "merge", "--no-edit", "incoming")
	cmd.Dir, cmd.Env = s.stage, s.env
	return cmd.Run()
}

func retentionAndFlush(s *sandbox) {
	seed(s)
	s.cfg["chunkTranscripts"] = false
	r := s.cfg["retention"].(map[string]any)
	r["historyDays"], r["largeFileBytes"], r["maxFileBytes"] = 30, 1024, 4096
	s.saveConfig()
	// Timestamp-free growing transcripts exercise mtime-based throttling.
	old := strings.Repeat("ordinary filler\n", 100)
	for _, id := range []string{"a", "b"} {
		s.put(live+project+id+".jsonl", old)
	}
	s.put(live+project+"ancient.jsonl", session("ancient", "old session"))
	ancient := time.Now().Add(-365 * 24 * time.Hour)
	must(s.t, os.Chtimes(filepath.Join(s.home, ".claude", filepath.FromSlash(project), "ancient.jsonl"), ancient, ancient))
	s.put(live+project+"oversize.jsonl", strings.Repeat("plain prose\n", 1000))
	s.run("initial", "", 0, "too large", "sync")
	s.absent(stage + projectPath("ancient.jsonl"))
	s.absent(stage + projectPath("oversize.jsonl"))
	for _, id := range []string{"a", "b"} {
		s.put(live+project+id+".jsonl", old+"tail\n")
	}
	s.run("blank-flush", " \n", 0, "no transcript flushed", "sync", "--flush")
	for _, id := range []string{"a", "b"} {
		if s.read(stage+projectPath(id+".jsonl")) != old {
			s.t.Fatal("blank payload flushed a growing transcript")
		}
	}
	payload, _ := json.Marshal(map[string]string{"transcript_path": filepath.Join(s.home, ".claude", filepath.FromSlash(project), "a.jsonl")})
	s.run("selected-flush", string(payload), 0, "pushed", "sync", "--flush")
	if s.read(stage+projectPath("a.jsonl")) != old+"tail\n" || s.read(stage+projectPath("b.jsonl")) != old {
		s.t.Fatal("selected flush covered the wrong sessions")
	}
	s.run("all-flush", "", 0, "pushed", "sync", "--flush")
	if s.read(stage+projectPath("b.jsonl")) != old+"tail\n" {
		s.t.Fatal("empty stdin did not flush all")
	}
	s.snapshot("flushed")
}

func freshPull(s *sandbox) {
	seed(s)
	s.run("initial", "", 0, "synced & pushed", "sync")
	must(s.t, os.RemoveAll(filepath.Join(s.home, ".claude")))
	must(s.t, os.RemoveAll(s.stage))
	s.run("disabled", "", 0, "", "pull")
	s.absent(live + "settings.json")
	s.cfg["autoRestore"] = true
	s.saveConfig()
	s.run("fresh", "", 0, "auto-restored", "pull")
	if !strings.Contains(s.read(live+"settings.json"), "high") {
		s.t.Fatal("fresh machine not restored")
	}
	s.put(live+"settings.json", `{"effortLevel":"local"}`+"\n")
	s.run("established", "", 0, "", "pull")
	if !strings.Contains(s.read(live+"settings.json"), "local") {
		s.t.Fatal("auto-restore clobbered established machine")
	}
	s.snapshot("pulled")
}
