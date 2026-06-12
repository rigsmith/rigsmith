// Package e2e holds clauderig's full round-trip test: sync a synthetic ~/.claude
// on one "machine", push it through a real git remote, then clone + restore it on
// a second "machine" with a different home, and assert the cross-machine
// invariants (slugs rewritten, secrets stripped, transcripts preserved, junk
// excluded, no placeholder leakage).
//
// It is GATED behind CLAUDERIG_E2E=1 so a normal `go test ./...` skips it — it
// shells git and copies a tree. It uses a LOCAL bare remote (no GitHub/gh/network)
// so it can run at any point:
//
//	CLAUDERIG_E2E=1 go test ./clauderig/internal/e2e/ -run E2E -v
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/engine"
	"github.com/rigsmith/clauderig/internal/gitrepo"
	"github.com/rigsmith/clauderig/internal/manifest"
	"github.com/rigsmith/clauderig/internal/project"
	"github.com/rigsmith/core/pathmap"
)

func TestE2E_RoundTrip(t *testing.T) {
	if os.Getenv("CLAUDERIG_E2E") == "" {
		t.Skip("gated: set CLAUDERIG_E2E=1 to run the full sync→git→restore round-trip")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := t.Context()

	// ---- source machine: a synthetic ~/.claude ----
	srcHome := t.TempDir()
	claudeSrc := filepath.Join(srcHome, ".claude")
	srcCwd := filepath.Join(srcHome, "Git", "proj")
	srcSlug := project.Flatten(srcCwd)

	write(t, claudeSrc, "settings.json",
		`{"effortLevel":"high","apiKey":"sk-ant-SECRET12345678","env":{"TOKEN":"ghp_aaaaaaaaaaaaaaaaaaaa","DEBUG":"true"}}`)
	write(t, claudeSrc, "skills/demo/SKILL.md", "demo skill body")
	write(t, claudeSrc, "plans/p.md", "a plan")
	write(t, claudeSrc, "projects/"+srcSlug+"/s.jsonl",
		`{"type":"user","cwd":"`+jsonEsc(srcCwd)+`","isSidechain":false}`+"\n")
	write(t, claudeSrc, "projects/"+srcSlug+"/data.jsonl", "line1\nline2\n")
	write(t, claudeSrc, "statsig/cache", "machine-local junk")         // must NOT sync
	write(t, claudeSrc, "projects/"+srcSlug+"/file-history/snap", "x") // carve-out, must NOT sync

	srcMachine := config.Machine{Name: "src", OS: pathmap.OSMacOS, Home: srcHome}

	// ---- sync into staging A, push to a local bare remote ----
	stagingA := t.TempDir()
	rep, err := engine.Sync(engine.Options{
		StagingDir: stagingA, Config: cliOnly(claudeSrc), Machine: srcMachine, ClaudeVersion: "e2e",
	})
	if err != nil {
		t.Fatalf("sync: %v (findings=%v)", err, rep.Findings)
	}
	if len(rep.Findings) != 0 {
		t.Fatalf("tripwire flagged on clean input: %v", rep.Findings)
	}

	bare := filepath.Join(t.TempDir(), "remote.git")
	mustGit(t, filepath.Dir(bare), "init", "--bare", "-b", "main", filepath.Base(bare))

	repoA, err := gitrepo.Init(ctx, stagingA)
	if err != nil {
		t.Fatal(err)
	}
	must(t, repoA.SetRemote(ctx, "origin", bare))
	if _, err := repoA.Commit(ctx, "sync from src"); err != nil {
		t.Fatal(err)
	}
	must(t, repoA.Push(ctx, "origin", "main"))

	// ---- target machine: clone, then restore to a DIFFERENT home ----
	tgtHome := t.TempDir()
	claudeTgt := filepath.Join(tgtHome, ".claude")
	tgtCwd := filepath.Join(tgtHome, "Git", "proj")
	tgtSlug := project.Flatten(tgtCwd)

	stagingB := filepath.Join(t.TempDir(), "repo")
	if _, err := gitrepo.Clone(ctx, bare, stagingB); err != nil {
		t.Fatal(err)
	}
	man, err := manifest.Load(stagingB)
	if err != nil {
		t.Fatal(err)
	}
	tgtMachine := config.Machine{Name: "tgt", OS: pathmap.OSMacOS, Home: tgtHome}
	if _, err := engine.Restore(engine.RestoreOptions{
		StagingDir: stagingB, Config: cliOnly(claudeTgt), Machine: tgtMachine, Manifest: man,
	}); err != nil {
		t.Fatal(err)
	}

	// ---- invariants ----

	// 1. slug rewritten for the target machine; source slug absent.
	if !exists(filepath.Join(claudeTgt, "projects", tgtSlug)) {
		t.Errorf("target slug %q not restored", tgtSlug)
	}
	if exists(filepath.Join(claudeTgt, "projects", srcSlug)) && srcSlug != tgtSlug {
		t.Errorf("source slug %q should not exist on target", srcSlug)
	}

	// 2. transcript content preserved verbatim.
	if got := read(t, filepath.Join(claudeTgt, "projects", tgtSlug, "data.jsonl")); got != "line1\nline2\n" {
		t.Errorf("transcript content changed: %q", got)
	}

	// 3. secrets stripped; non-secret config preserved; no placeholder literal.
	s := readJSON(t, filepath.Join(claudeTgt, "settings.json"))
	if _, ok := s["apiKey"]; ok {
		t.Errorf("apiKey should be stripped, got %v", s["apiKey"])
	}
	if s["effortLevel"] != "high" {
		t.Errorf("non-secret config lost: %v", s["effortLevel"])
	}
	if env, ok := s["env"].(map[string]any); ok {
		if _, ok := env["TOKEN"]; ok {
			t.Errorf("env secret survived: %v", env)
		}
	}
	for _, f := range jsonFiles(t, claudeTgt) {
		if strings.Contains(read(t, f), "__CLAUDERIG_REDACTED__") {
			t.Errorf("placeholder leaked into %s", f)
		}
	}

	// 4. skills/plans restored.
	if read(t, filepath.Join(claudeTgt, "skills", "demo", "SKILL.md")) != "demo skill body" {
		t.Error("skill not restored")
	}

	// 5. junk + carve-outs excluded.
	if exists(filepath.Join(claudeTgt, "statsig")) {
		t.Error("statsig junk should not have synced")
	}
	if exists(filepath.Join(claudeTgt, "projects", tgtSlug, "file-history")) {
		t.Error("file-history carve-out should not have synced")
	}

	t.Logf("round-trip OK: %s → %s, secrets stripped, junk excluded", srcSlug, tgtSlug)
}

// --- helpers ---

func cliOnly(claudeDir string) *config.Config {
	c := config.Default()
	c.Roots = []config.Root{{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: claudeDir}}}
	return c
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(read(t, p)), &m); err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return m
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func jsonFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func jsonEsc(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1]) // strip surrounding quotes
}
