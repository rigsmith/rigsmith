package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

// publishWithRecording runs `shiprig publish --yes --no-git-tag` plus args in
// the current directory's workspace with its npm adapter recording, and
// returns what was asked to publish, the output, and the command's error.
func publishWithRecording(t *testing.T, args ...string) ([]plugin.PublishRequest, string, error) {
	t.Helper()
	var mu sync.Mutex
	var reqs []plugin.PublishRequest
	was := openPublishWorkspace
	openPublishWorkspace = func() (*commands.Workspace, error) {
		ws, err := commands.Open()
		if err != nil {
			return nil, err
		}
		node, _ := ws.EcosystemFor("node")
		ws.Registry.Register(recordingNode{Ecosystem: node, mu: &mu, reqs: &reqs})
		return ws, nil
	}
	t.Cleanup(func() { openPublishWorkspace = was })

	cmd := newPublishCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--yes", "--no-git-tag"}, args...))
	err := cmd.Execute()
	return reqs, out.String(), err
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const pendingLibChangeset = "---\n\"lib\": patch\n---\n\nFix the thing.\n"

// The run that started this: a changeset merged, nobody ran `version`, and
// publish re-pushed the versions on disk, which were the last release's. It
// refuses, names the package, says what to run, and publishes nothing.
func TestPublishRefusesWhileChangesetsAreUnreleased(t *testing.T) {
	root := planRepo(t, `{}`)
	writeFile(t, root, ".changeset/fix-the-thing.md", pendingLibChangeset)

	reqs, out, err := publishWithRecording(t)
	if err == nil {
		t.Fatalf("publish succeeded with a pending changeset; output:\n%s", out)
	}
	for _, want := range []string{"unreleased changesets (lib)", "shiprig version", "--allow-pending"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if len(reqs) != 0 {
		t.Errorf("publish asked %d package(s) to publish before refusing", len(reqs))
	}
}

func TestPublishAllowPendingPublishesTheVersionsOnDisk(t *testing.T) {
	root := planRepo(t, `{}`)
	writeFile(t, root, ".changeset/fix-the-thing.md", pendingLibChangeset)

	reqs, out, err := publishWithRecording(t, "--allow-pending")
	if err != nil {
		t.Fatalf("publish --allow-pending: %v\n%s", err, out)
	}
	if len(reqs) == 0 {
		t.Error("publish --allow-pending published nothing")
	}
}

// A dry run is how you find out, so it warns rather than refusing.
func TestPublishDryRunWarnsAboutUnreleasedChangesets(t *testing.T) {
	root := planRepo(t, `{}`)
	writeFile(t, root, ".changeset/fix-the-thing.md", pendingLibChangeset)

	_, out, err := publishWithRecording(t, "--dry-run")
	if err != nil {
		t.Fatalf("publish --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "unreleased changesets (lib)") {
		t.Errorf("dry run output does not warn about the pending changeset:\n%s", out)
	}
}

// In prerelease mode a changeset stays on disk after `version` consumed it
// (pre.json lists it). It is released, not pending.
func TestPublishDoesNotCountAChangesetAPrereleaseConsumed(t *testing.T) {
	root := planRepo(t, `{}`)
	writeFile(t, root, ".changeset/fix-the-thing.md", pendingLibChangeset)
	writeFile(t, root, ".changeset/pre.json",
		`{"mode":"pre","tag":"next","initialVersions":{"lib":"1.0.0","app":"2.0.0","done":"1.0.0","priv":"1.0.0"},"changesets":["fix-the-thing"]}`)

	reqs, out, err := publishWithRecording(t)
	if err != nil {
		t.Fatalf("publish refused a changeset the prerelease already consumed: %v\n%s", err, out)
	}
	if len(reqs) == 0 {
		t.Error("publish published nothing")
	}
}

// Commit-based versioning: a release pipeline publishes before it tags, so the
// release's own commits are still "since the last tag" when publish runs.
// They must not count as pending, or every pipeline release would be refused.
// "both" is the mode where that matters: it reads changeset files too, so the
// guard runs, and the commit-derived changesets arrive mixed in with them.
func TestPublishDoesNotCountCommitsAsUnreleasedChangesets(t *testing.T) {
	root := planRepo(t, `{"versioning": {"source": "both"}}`)
	// From the resolved path: commit attribution compares package directories
	// built from the root as given against the resolved paths of the changed
	// files, so through a symlinked temp dir (macOS's /var/folders) it
	// attributes nothing and there would be no commit-derived changeset here.
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = real
	t.Chdir(root)
	tagRepo(t, root, "lib@1.0.0", "app@2.0.0", "done@1.0.0", "priv@1.0.0")
	gitCommit(t, root, "fix(lib): a conventional commit since the last release")
	// The commit must really be a pending change, or this proves nothing.
	ws, err := commands.Open()
	if err != nil {
		t.Fatal(err)
	}
	pkgs, _, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sets, _, err := ws.LoadChangesets(context.Background(), pkgs)
	if err != nil {
		t.Fatal(err)
	}
	fromCommit := 0
	for _, cs := range sets {
		if cs.Commit != "" {
			fromCommit++
		}
	}
	if fromCommit == 0 {
		t.Fatalf("the commit produced no commit-derived changeset (%d changeset(s) loaded); the test is not testing anything", len(sets))
	}

	reqs, out, err := publishWithRecording(t)
	if err != nil {
		t.Fatalf("publish refused over commit-derived changes: %v\n%s", err, out)
	}
	if len(reqs) == 0 {
		t.Error("publish published nothing")
	}
}

func gitCommit(t *testing.T, root, message string) {
	t.Helper()
	writeFile(t, root, "packages/lib/CHANGE.txt", message)
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.email=x@y.z", "-c", "user.name=x", "-c", "commit.gpgsign=false", "commit", "-q", "-m", message},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// A repo with no .changeset/ directory has nothing pending, and publishing it
// must not fail for want of one.
func TestPublishWithNoChangesetDirectoryPublishes(t *testing.T) {
	root := planRepo(t, `{}`)
	if err := os.RemoveAll(filepath.Join(root, ".changeset")); err != nil {
		t.Fatal(err)
	}

	reqs, out, err := publishWithRecording(t)
	if err != nil {
		t.Fatalf("publish with no .changeset/: %v\n%s", err, out)
	}
	if len(reqs) == 0 {
		t.Error("publish published nothing")
	}
}

// A first release with initialRelease.collapse folds its commits into one
// synthetic "Initial release" changeset, which carries no commit SHA. It is
// commit-derived all the same, so it must not block the publish.
func TestPublishDoesNotCountACollapsedInitialRelease(t *testing.T) {
	root := planRepo(t, `{"versioning": {"source": "both", "initialRelease": {"collapse": true}}}`)
	// From the resolved path, as in TestPublishDoesNotCountCommitsAsUnreleasedChangesets.
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = real
	t.Chdir(root)
	gitCommit(t, root, "feat(lib): the first feature, before any release tag")

	// The case must really arise: a collapsed changeset with no commit SHA.
	ws, err := commands.Open()
	if err != nil {
		t.Fatal(err)
	}
	pkgs, _, err := ws.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sets, _, err := ws.LoadChangesets(context.Background(), pkgs)
	if err != nil {
		t.Fatal(err)
	}
	collapsed := 0
	for _, cs := range sets {
		if cs.Commit == "" {
			collapsed++
		}
	}
	if collapsed == 0 {
		t.Fatalf("no collapsed initial-release changeset without a commit SHA (%d changeset(s) loaded); the test is not testing anything", len(sets))
	}

	reqs, out, err := publishWithRecording(t)
	if err != nil {
		t.Fatalf("publish refused a collapsed initial release: %v\n%s", err, out)
	}
	if len(reqs) == 0 {
		t.Error("publish published nothing")
	}
}
