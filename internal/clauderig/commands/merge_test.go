package commands

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// divergedRepos builds the shape of the 2026-08-07 incident: two clones of one
// remote that have each moved on, editing the same files. It returns the local
// clone, already fetched so origin/main is the other machine's work.
//
// files maps a repo-relative path to the (base, ours, theirs) content; a base of
// "" means the file is created independently on both sides.
func divergedRepos(t *testing.T, files map[string][3]string) *gitrepo.Repo {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	put := func(dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	bare := filepath.Join(root, "remote.git")
	run(root, "init", "-q", "--bare", "-b", "main", bare)

	remoteWC := filepath.Join(root, "remote-wc")
	run(root, "clone", "-q", bare, remoteWC)
	run(remoteWC, "config", "user.email", "t@t")
	run(remoteWC, "config", "user.name", "t")

	// Base commit — only files that have one.
	wrote := false
	for rel, v := range files {
		if v[0] != "" {
			put(remoteWC, rel, v[0])
			wrote = true
		}
	}
	if !wrote {
		put(remoteWC, ".keep", "x\n") // git needs something to commit
	}
	run(remoteWC, "add", "-A")
	run(remoteWC, "commit", "-qm", "base")
	run(remoteWC, "push", "-q", "origin", "HEAD:main")

	local := filepath.Join(root, "local")
	run(root, "clone", "-q", bare, local)
	run(local, "config", "user.email", "t@t")
	run(local, "config", "user.name", "t")

	// The remote machine moves on and pushes.
	for rel, v := range files {
		put(remoteWC, rel, v[2])
	}
	run(remoteWC, "add", "-A")
	run(remoteWC, "commit", "-qm", "their side")
	run(remoteWC, "push", "-q", "origin", "HEAD:main")

	// The local machine moves on independently and never pulls.
	for rel, v := range files {
		put(local, rel, v[1])
	}
	run(local, "add", "-A")
	run(local, "commit", "-qm", "our side")
	run(local, "fetch", "-q", "origin", "main")

	repo, err := gitrepo.Open(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// The incident, end to end: every file class conflicts at once and every one
// resolves without a human, keeping both machines' data.
func TestMergeResolvesEveryPolicy(t *testing.T) {
	ctx := context.Background()
	repo := divergedRepos(t, map[string][3]string{
		"clauderig-devices.json": {
			`{"schema":1,"devices":{"Pro":{"name":"Pro","lastSync":"2026-08-08T09:00:00Z"}}}`,
			`{"schema":1,"devices":{"Air":{"name":"Air","lastSync":"2026-08-08T12:00:00Z"}}}`,
			`{"schema":1,"devices":{"Pro":{"name":"Pro","lastSync":"2026-08-08T18:00:00Z"}}}`,
		},
		"clauderig-manifest.json": {
			`{"schema":1,"claudeVersion":"2.1.200","sourceOS":"macos","projects":{"-demo":{"cwd":"/demo"}}}`,
			`{"schema":1,"claudeVersion":"2.1.223","sourceOS":"macos","projects":{"-demo":{"cwd":"/demo"},"-air":{"cwd":"/air"}}}`,
			`{"schema":1,"claudeVersion":"2.1.212","sourceOS":"macos","projects":{"-demo":{"cwd":"/demo"},"-pro":{"cwd":"/pro"}}}`,
		},
		"cli/projects/-demo/s.jsonl": {
			"{\"i\":1}\n",
			"{\"i\":1}\n{\"air\":\"tail\"}\n",
			"{\"i\":1}\n{\"pro\":\"tail\"}\n",
		},
		"cli/projects/-demo/memory/MEMORY.md": {
			"- [shared](shared.md) — base\n",
			"- [shared](shared.md) — base\n- [airnote](airnote.md) — air only\n",
			"- [shared](shared.md) — base\n- [pronote](pronote.md) — pro only\n",
		},
		"extensions-blocklist.json": {
			`[{"entries":["base"],"lastUpdated":"2026-08-01T00:00:00Z"}]`,
			`[{"entries":["local"],"lastUpdated":"2026-08-02T00:00:00Z"}]`,
			`[{"entries":["remote"],"lastUpdated":"2026-08-09T00:00:00Z"}]`,
		},
	})

	conflicted, err := repo.MergeRef(ctx, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if !conflicted {
		t.Fatal("expected the merge to conflict — the fixture edits the same files on both sides")
	}

	ledger, residual, err := applyPolicies(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(residual) != 0 {
		t.Fatalf("every file has a policy, but these were left: %v", residual)
	}
	if len(ledger) != 5 {
		t.Errorf("resolved %d files, want 5", len(ledger))
	}
	// Every entry names its policy and says what it did — that is the ledger
	// the Resolve panel renders, and the reason the merge is auditable.
	for _, r := range ledger {
		if r.Path == "" || r.Policy == "" || r.Detail == "" {
			t.Errorf("incomplete ledger entry: %+v", r)
		}
	}

	// Both machines survive in every file.
	devices := readRepo(t, repo.Dir, "clauderig-devices.json")
	if !strings.Contains(devices, `"Air"`) || !strings.Contains(devices, `"Pro"`) {
		t.Errorf("a machine was dropped from the registry:\n%s", devices)
	}
	manifest := readRepo(t, repo.Dir, "clauderig-manifest.json")
	for _, slug := range []string{"-demo", "-air", "-pro"} {
		if !strings.Contains(manifest, slug) {
			t.Errorf("manifest lost %s:\n%s", slug, manifest)
		}
	}
	transcript := readRepo(t, repo.Dir, "cli/projects/-demo/s.jsonl")
	if !strings.Contains(transcript, `"air"`) || !strings.Contains(transcript, `"pro"`) {
		t.Errorf("a transcript tail was lost:\n%s", transcript)
	}
	memory := readRepo(t, repo.Dir, "cli/projects/-demo/memory/MEMORY.md")
	if !strings.Contains(memory, "airnote.md") || !strings.Contains(memory, "pronote.md") {
		t.Errorf("a memory entry was lost:\n%s", memory)
	}
	if blocklist := readRepo(t, repo.Dir, "extensions-blocklist.json"); !strings.Contains(blocklist, "remote") {
		t.Errorf("the newer cache did not win:\n%s", blocklist)
	}

	// Everything is staged, so the merge can be committed.
	if err := repo.CommitMerge(ctx); err != nil {
		t.Fatalf("merge could not be committed: %v", err)
	}
	if dirty, err := repo.Dirty(ctx); err != nil || dirty {
		t.Errorf("tree still dirty after the merge commit: %v %v", dirty, err)
	}
}

// A file with no policy must survive as a conflict for the user's own tool —
// never guessed at, and never silently taking a side.
func TestMergeLeavesUnpoliciedFilesConflicted(t *testing.T) {
	ctx := context.Background()
	repo := divergedRepos(t, map[string][3]string{
		"skills/thing.md": {"base\n", "local edit\n", "remote edit\n"},
		"clauderig-devices.json": {
			`{"schema":1,"devices":{"Pro":{"lastSync":"2026-08-08T09:00:00Z"}}}`,
			`{"schema":1,"devices":{"Air":{"lastSync":"2026-08-08T12:00:00Z"}}}`,
			`{"schema":1,"devices":{"Pro":{"lastSync":"2026-08-08T18:00:00Z"}}}`,
		},
	})

	if _, err := repo.MergeRef(ctx, "origin/main"); err != nil {
		t.Fatal(err)
	}
	ledger, residual, err := applyPolicies(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}

	if len(residual) != 1 || residual[0] != "skills/thing.md" {
		t.Fatalf("residual = %v, want exactly skills/thing.md", residual)
	}
	// The file it *could* handle still got resolved — a partial merge is
	// progress, and aborting would throw that away.
	if len(ledger) != 1 {
		t.Errorf("resolved %d, want the devices file resolved alongside", len(ledger))
	}
	// The unresolved file keeps git's markers so a mergetool can open it.
	if body := readRepo(t, repo.Dir, "skills/thing.md"); !strings.Contains(body, "<<<<<<<") {
		t.Errorf("unresolved file lost its conflict markers:\n%s", body)
	}
}

func readRepo(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
