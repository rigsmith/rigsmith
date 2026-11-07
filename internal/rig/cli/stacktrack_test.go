package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func trackOnly(t *testing.T, preview bool) (string, error) {
	t.Helper()
	cmd := newStackSendCmd()
	if err := cmd.Flags().Set("track-only", "true"); err != nil {
		t.Fatal(err)
	}
	previous := dryRun
	dryRun = preview
	defer func() { dryRun = previous }()
	return runVerbOut(context.Background(), cmd, "lib")
}

func trackStackspace(t *testing.T, track string) (string, string, *gitServer) {
	t.Helper()
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	ws, _ := twoFixStackspace(t, work, srv, track)
	chdir(t, ws)
	return ws, fork, srv
}

func assertIntegrationSnapshot(t *testing.T, ws, fork, tip string) {
	t.Helper()
	commit := strings.TrimSpace(mustGitStack(t, fork, "rev-parse", "stack/integration"))
	if got := strings.TrimSpace(mustGitStack(t, fork, "rev-parse", commit+"^{tree}")); got != strings.TrimSpace(mustGitStack(t, ws, "rev-parse", "HEAD:lib")) {
		t.Fatal("integration tree differs from HEAD:lib")
	}
	if got := strings.TrimSpace(mustGitStack(t, fork, "rev-parse", commit+"^@")); got != tip {
		t.Fatalf("integration parents = %q, want only upstream tip %s", got, tip)
	}
	if got := strings.TrimSpace(mustGitStack(t, ws, "rev-parse", "refs/rigsmith/integration/lib")); got != commit {
		t.Fatalf("integration ref = %s, want %s", got, commit)
	}
}

func TestStackProposeFromRequiresIntegrationLine(t *testing.T) {
	ws, fork, _ := trackStackspace(t, "stack/integration")
	// This checkout carries A alone; the topic being proposed is B.
	mustGitStack(t, ws, "switch", "-q", "-c", "stack-pr-fix-a", "main^1")
	for _, preview := range []bool{false, true} {
		previous := dryRun
		dryRun = preview
		out, err := propose(t, "lib", "fix-b", "fix-b")
		dryRun = previous
		if err == nil || !strings.Contains(err.Error(), "stack-pr-fix-b") || !strings.Contains(err.Error(), "check out the integration line") {
			t.Fatalf("preview=%t: want named integration refusal, got %v\n%s", preview, err, out)
		}
	}
	if got := mustGitStack(t, fork, "for-each-ref"); got != "" {
		t.Fatalf("refusal wrote remote refs: %s", got)
	}
	if refExists(t, ws, "refs/rigsmith/propose/lib") || refExists(t, ws, "refs/rigsmith/integration/lib") {
		t.Fatal("refusal recorded a push")
	}
}

func TestStackProposeTrackOnlyRefresh(t *testing.T) {
	ws, fork, srv := trackStackspace(t, "stack/integration")
	if out, err := propose(t, "lib", "fix-b", "fix-b"); err != nil {
		t.Fatalf("initial proposal: %v\n%s", err, out)
	}
	oldIntegration := mustGitStack(t, fork, "rev-parse", "stack/integration")
	proposal := mustGitStack(t, fork, "rev-parse", "stack/fix-b")

	// Simulate pull's result without an engine: new upstream content and cursor,
	// with the original topics still merged in HEAD but rooted before this import.
	srv.commit(t, "acme/lib", "src/upstream.txt", "new base\n", "new upstream")
	tip := strings.TrimSpace(mustGitStack(t, srv.path("acme/lib"), "rev-parse", "main"))
	m, src, _, err := stackspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stackSetCursor(src, m, "lib", tip); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "lib", "src", "upstream.txt"), []byte("new base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "stack: pull lib @ "+short(tip))
	beforeManifest := mustGitStack(t, ws, "show", "HEAD:rig.stack.jsonc")
	beforeHEAD := mustGitStack(t, ws, "rev-parse", "HEAD")
	if out, err := trackOnly(t, true); err != nil || !strings.Contains(out, "would push") {
		t.Fatalf("preview: %v\n%s", err, out)
	}
	if got := mustGitStack(t, fork, "rev-parse", "stack/integration"); got != oldIntegration {
		t.Fatal("preview moved remote integration")
	}
	if got := mustGitStack(t, ws, "rev-parse", "refs/rigsmith/integration/lib"); got != oldIntegration {
		t.Fatal("preview moved local integration ref")
	}
	if out, err := trackOnly(t, false); err != nil || !strings.Contains(out, "now carries everything") {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	assertIntegrationSnapshot(t, ws, fork, tip)
	if got := mustGitStack(t, fork, "rev-parse", "stack/integration"); got == oldIntegration {
		t.Fatal("refresh left old integration base")
	}
	if got := mustGitStack(t, fork, "rev-parse", "stack/fix-b"); got != proposal {
		t.Fatal("refresh moved proposal")
	}
	if got := mustGitStack(t, ws, "rev-parse", "HEAD"); got != beforeHEAD {
		t.Fatal("refresh changed stackspace history")
	}
	if got, err := os.ReadFile(src.File); err != nil || string(got) != beforeManifest {
		t.Fatalf("refresh changed proposal records: %v", err)
	}
}

func TestStackProposeTrackOnlyIdempotent(t *testing.T) {
	ws, fork, srv := trackStackspace(t, "stack/integration")
	if out, err := trackOnly(t, false); err != nil {
		t.Fatalf("initial refresh: %v\n%s", err, out)
	}
	before := mustGitStack(t, fork, "rev-parse", "stack/integration")
	// A client hook catches even a push that Git would find already up to date.
	hook := filepath.Join(ws, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Recover the local receipt from the remote, rather than trusting a local no-op.
	mustGitStack(t, ws, "update-ref", "-d", "refs/rigsmith/integration/lib")
	for _, preview := range []bool{true, false} {
		out, err := trackOnly(t, preview)
		if err != nil || !strings.Contains(out, "up to date") {
			t.Fatalf("preview=%t: repeat refresh: %v\n%s", preview, err, out)
		}
		if preview && refExists(t, ws, "refs/rigsmith/integration/lib") {
			t.Fatal("preview recorded a receipt")
		}
	}
	if got := mustGitStack(t, fork, "rev-parse", "stack/integration"); got != before {
		t.Fatal("repeat refresh replaced commit")
	}
	tip := strings.TrimSpace(mustGitStack(t, srv.path("acme/lib"), "rev-parse", "main"))
	assertIntegrationSnapshot(t, ws, fork, tip)
	if refExists(t, ws, "refs/rigsmith/propose/lib") || strings.Contains(mustGitStack(t, ws, "show", "HEAD:rig.stack.jsonc"), "lastPropose") {
		t.Fatal("integration-only refresh recorded a proposal")
	}
}

func TestStackProposeTrackOnlyGuards(t *testing.T) {
	for _, guard := range []string{"missing-track", "stale-cursor", "dirty-prefix", "unmerged-topic", "deleted-topic"} {
		t.Run(guard, func(t *testing.T) {
			track := "stack/integration"
			if guard == "missing-track" {
				track = ""
			}
			ws, fork, srv := trackStackspace(t, track)
			want := ""
			switch guard {
			case "missing-track":
				want = "needs a trackBranch"
			case "stale-cursor":
				srv.commit(t, "acme/lib", "src/new.txt", "new\n", "upstream moved")
				want = "rig stack pull lib"
			case "dirty-prefix":
				if err := os.WriteFile(filepath.Join(ws, "lib", "src", "a.txt"), []byte("dirty\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				want = "uncommitted changes"
			case "unmerged-topic", "deleted-topic":
				if out, err := propose(t, "lib", "fix-b", "fix-b"); err != nil {
					t.Fatalf("initial proposal: %v\n%s", err, out)
				}
				if guard == "unmerged-topic" {
					// The recorded branch advances beyond the integration line.
					mustGitStack(t, ws, "switch", "-q", "stack-pr-fix-b")
					mustGitStack(t, ws, "commit", "-q", "--allow-empty", "-m", "topic moved")
					mustGitStack(t, ws, "switch", "-q", "main")
					want = "check out the integration line"
				} else {
					mustGitStack(t, ws, "branch", "-D", "stack-pr-fix-b")
				}
			}
			before := mustGitStack(t, fork, "for-each-ref")
			for _, preview := range []bool{true, false} {
				out, err := trackOnly(t, preview)
				if want == "" {
					if err != nil {
						t.Fatalf("deleted topic must be ignored: %v\n%s", err, out)
					}
				} else if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("preview=%t: want %q, got %v\n%s", preview, want, err, out)
				}
			}
			if got := mustGitStack(t, fork, "for-each-ref"); got != before {
				t.Fatal("guard moved remote refs")
			}
		})
	}
}

func TestStackProposeTrackOnlyExactParent(t *testing.T) {
	ws, fork, srv := trackStackspace(t, "stack/integration")
	if out, err := trackOnly(t, false); err != nil {
		t.Fatalf("initial refresh: %v\n%s", err, out)
	}
	tip := strings.TrimSpace(mustGitStack(t, srv.path("acme/lib"), "rev-parse", "main"))
	// Upstream moves without changing its tree. Matching only the integration
	// tree would incorrectly leave the branch based on the previous cursor.
	newTip := strings.TrimSpace(mustGitStack(t, ws, "commit-tree", tip+"^{tree}", "-p", tip, "-m", "upstream metadata"))
	mustGitStack(t, ws, "push", "-q", srv.path("acme/lib"), newTip+":refs/heads/main")
	m, src, _, err := stackspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stackSetCursor(src, m, "lib", newTip); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "stack: pull lib @ "+short(newTip))
	if out, err := trackOnly(t, false); err != nil || strings.Contains(out, "up to date") {
		t.Fatalf("base-only refresh: %v\n%s", err, out)
	}
	assertIntegrationSnapshot(t, ws, fork, newTip)
}

func TestStackProposeTrackOnlyExclusive(t *testing.T) {
	cmd := newStackSendCmd()
	for flag, value := range map[string]string{"track-only": "true", "from": "fix-b"} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runVerbOut(context.Background(), cmd, "lib"); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected exclusive flags refusal, got %v", err)
	}
}

func TestStackProposeTrackOnlyAfterPull(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1")
	}
	for _, tool := range []string{toolFilter, toolProxy} {
		bin, err := stackJoshToolBin(stackJoshVersion, tool)
		if err != nil || stackJoshInstalled(bin) != nil {
			t.Skipf("no %s installed", tool)
		}
	}
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	mustGitStack(t, srv.path("acme/lib"), "push", "-q", fork, "main:refs/heads/stack/integration")
	ws := t.TempDir()
	mustGitStack(t, ws, "init", "-q", "-b", "main")
	mustGitStack(t, ws, "config", "user.email", "t@t")
	mustGitStack(t, ws, "config", "user.name", "t")
	writeStackManifest(t, ws, fmt.Sprintf(`{"repos":{"lib":{"upstream":%q,"fork":%q,"trackBranch":"stack/integration"}}}`, srv.spec("acme/lib"), srv.spec("you/lib")))
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "stack manifest")
	chdir(t, ws)
	ctx := context.Background()
	if err := runVerb(ctx, newStackInitCmd()); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "switch", "-q", "-c", "stack-pr-fix")
	if err := os.WriteFile(filepath.Join(ws, "lib", "src", "fix.txt"), []byte("carried fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "carried fix")
	mustGitStack(t, ws, "switch", "-q", "main")
	mustGitStack(t, ws, "merge", "-q", "--no-edit", "stack-pr-fix")
	if out, err := propose(t, "lib", "fix", "fix"); err != nil {
		t.Fatalf("propose: %v\n%s", err, out)
	}
	before := mustGitStack(t, fork, "rev-parse", "stack/integration")
	srv.commit(t, "acme/lib", "src/new.txt", "new upstream\n", "upstream moved")
	if out, err := runVerbOut(ctx, newStackPullCmd(), "lib"); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if got := mustGitStack(t, fork, "rev-parse", "stack/integration"); got != before {
		t.Fatal("pull unexpectedly moved trackBranch")
	}
	if out, err := trackOnly(t, false); err != nil {
		t.Fatalf("refresh after pull: %v\n%s", err, out)
	}
	tip := strings.TrimSpace(mustGitStack(t, srv.path("acme/lib"), "rev-parse", "main"))
	assertIntegrationSnapshot(t, ws, fork, tip)
	for _, path := range []string{"src/fix.txt", "src/new.txt"} {
		if !strings.Contains(forkTree(t, fork, "stack/integration"), path) {
			t.Fatalf("refreshed integration missing %s", path)
		}
	}
}
