package changelog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// execRun is a Runner that really runs the command, for the tests that need
// git's own behaviour rather than a canned answer.
func execRun(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// CI checks out one commit deep. The clone's only commit has no parent, so
// `git log --diff-filter=A` names it as the commit that added every file, and
// every changelog entry would link the release commit's pull request. As
// @changesets/git does, the resolver deepens the clone until it finds the
// commit that really added each changeset.
func TestResolveDeepensAShallowCloneToFindTheAddingCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	if err := os.MkdirAll(filepath.Join(origin, ".changeset"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "init", "-q", "-b", "main")

	add := func(name, msg string) string {
		if err := os.WriteFile(filepath.Join(origin, ".changeset", name), []byte("---\n---\n"+msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, origin, "add", "-A")
		gitIn(t, origin, "commit", "-q", "-m", msg)
		return gitIn(t, origin, "rev-parse", "--short", "HEAD")
	}
	add("readme.md", "base") // a root commit for the history to start from
	first := add("first-change.md", "first change")
	second := add("second-change.md", "second change")
	for i := 0; i < 3; i++ {
		gitIn(t, origin, "commit", "-q", "--allow-empty", "-m", "later")
	}
	head := gitIn(t, origin, "rev-parse", "--short", "HEAD")

	clone := filepath.Join(root, "clone")
	gitIn(t, root, "clone", "-q", "--depth", "1", "file://"+origin, clone)
	if got := gitIn(t, clone, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Fatalf("the fixture clone isn't shallow (%s); the test would prove nothing", got)
	}

	got := Resolve([]string{"first-change", "second-change"}, Setting{Kind: KindGit}, clone, execRun)

	for id, want := range map[string]string{"first-change": first, "second-change": second} {
		if got[id].Commit != want {
			t.Errorf("%s: commit %q, want %q (the clone's boundary is %s)", id, got[id].Commit, want, head)
		}
	}
}

// A shallow clone that can't be deepened (no remote to fetch from) leaves the
// changeset unattributed rather than naming the boundary commit.
func TestResolveLeavesAChangesetUnattributedWhenTheCloneCannotDeepen(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--diff-filter=A", output: "bound12:"},
		{name: "git", marker: "--is-shallow-repository", output: "true"},
		// git fetch --deepen matches nothing, so it fails.
	}}

	result := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, "/repo", runner.run)

	if info, ok := result["cs1"]; ok {
		t.Errorf("result[cs1] = %+v, want it omitted", info)
	}
}
