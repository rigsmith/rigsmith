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
		return gitIn(t, origin, "rev-parse", "HEAD")
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

// A parentless commit is trusted as a true root only when the repository is
// known not to be shallow; if that can't be told, the changeset is left
// unattributed.
func TestResolveLeavesAChangesetUnattributedWhenShallownessIsUnknown(t *testing.T) {
	for name, status := range map[string]*fakeResponse{
		"the probe fails":           nil,
		"the probe prints nonsense": {name: "git", marker: "--is-shallow-repository", output: "maybe"},
	} {
		t.Run(name, func(t *testing.T) {
			responses := []fakeResponse{{name: "git", marker: "--diff-filter=A", output: "root123:"}}
			if status != nil {
				responses = append(responses, *status)
			}
			runner := &fakeRunner{responses: responses}
			result := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, "/repo", runner.run)
			if info, ok := result["cs1"]; ok {
				t.Errorf("result[cs1] = %+v, want it omitted", info)
			}
		})
	}
}

// A true root commit in a complete clone has no parent either, and is the
// right answer.
func TestResolveTrustsAParentlessCommitInACompleteClone(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--diff-filter=A", output: "root123:"},
		{name: "git", marker: "--is-shallow-repository", output: "false"},
	}}
	result := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, "/repo", runner.run)
	if got := result["cs1"].Commit; got != "root123" {
		t.Errorf("commit %q, want root123", got)
	}
}

// A git too old for --is-shallow-repository echoes the flag; the clone is
// then shallow exactly when .git/shallow exists.
func TestResolveReadsShallownessFromTheShallowFileOnOldGit(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--diff-filter=A", output: "root123:"},
		{name: "git", marker: "--is-shallow-repository", output: "--is-shallow-repository"},
		{name: "git", marker: "--git-path shallow", output: ".git/shallow"},
	}}
	// No .git/shallow: complete, so the root commit stands.
	if got := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, dir, runner.run)["cs1"].Commit; got != "root123" {
		t.Errorf("without .git/shallow: commit %q, want root123", got)
	}
	// .git/shallow present and no way to deepen: unattributed.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "shallow"), []byte("root123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, ok := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, dir, runner.run)["cs1"]; ok {
		t.Errorf("with .git/shallow: result %+v, want it omitted", info)
	}
}

// A git older than 2.5 knows neither flag and echoes both back; that's no
// answer, so the changeset is left unattributed.
func TestResolveTreatsAnEchoedGitPathAsUnknown(t *testing.T) {
	runner := &fakeRunner{responses: []fakeResponse{
		{name: "git", marker: "--diff-filter=A", output: "root123:"},
		{name: "git", marker: "--is-shallow-repository", output: "--is-shallow-repository"},
		{name: "git", marker: "--git-path shallow", output: "--git-path\nshallow"},
	}}
	if info, ok := Resolve([]string{"cs1"}, Setting{Kind: KindGit}, t.TempDir(), runner.run)["cs1"]; ok {
		t.Errorf("result[cs1] = %+v, want it omitted", info)
	}
}
