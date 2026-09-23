package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The pre-push guard in lefthook.yml is .lefthook/pre-push/no-clobber-main.sh. These
// drive the real script: most feed it the lines git would put on stdin, and
// one goes through a real `git push --force` so git itself feeds the hook.

const zeroSHA = "0000000000000000000000000000000000000000"

// clobberRepos is a bare "origin" plus a clone "work" whose main is at the
// commit it returns as base. ahead is a second commit on origin's main that
// work has not fetched, so work's main is behind.
type clobberRepos struct {
	origin, work string
	base, ahead  string
}

func runGit(t *testing.T, dir string, args ...string) string {
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

func commit(t *testing.T, dir, msg string) string {
	t.Helper()
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
	return runGit(t, dir, "rev-parse", "HEAD")
}

func newClobberRepos(t *testing.T) clobberRepos {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	r := clobberRepos{
		origin: filepath.Join(root, "origin.git"),
		work:   filepath.Join(root, "work"),
	}
	runGit(t, root, "init", "-q", "--bare", "-b", "main", r.origin)
	runGit(t, root, "clone", "-q", r.origin, r.work)
	runGit(t, r.work, "checkout", "-q", "-b", "main")
	r.base = commit(t, r.work, "base")
	runGit(t, r.work, "push", "-q", "origin", "main")

	// Someone else merges a PR: origin's main moves on without work.
	other := filepath.Join(root, "other")
	runGit(t, root, "clone", "-q", r.origin, other)
	r.ahead = commit(t, other, "merged PR")
	runGit(t, other, "push", "-q", "origin", "main")
	return r
}

// guard runs the script in dir as the hook would for a push to origin, with
// stdin as given, and reports whether it allowed the push and what it said.
func guard(t *testing.T, dir, stdin string) (bool, string) {
	t.Helper()
	return guardArgs(t, dir, stdin, "origin", "origin")
}

// guardArgs is guard with the remote name and push URL git would pass.
func guardArgs(t *testing.T, dir, stdin, remote, url string) (bool, string) {
	t.Helper()
	script, err := filepath.Abs("../.lefthook/pre-push/no-clobber-main.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("no-clobber-main.sh not found — fix this test rather than deleting it: %v", err)
	}
	cmd := exec.Command("sh", script, remote, url)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, string(out)
	}
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("running the guard: %v\n%s", err, out)
	}
	return false, string(out)
}

func line(localRef, localSHA, remoteRef, remoteSHA string) string {
	return localRef + " " + localSHA + " " + remoteRef + " " + remoteSHA + "\n"
}

// The case that started this: main checked out and behind a freshly fetched
// origin/main (nothing of its own), and the push is only a tag. The old hook
// looked at the checked-out branch and refused.
func TestNoClobberMainAllowsATagWhileMainIsBehind(t *testing.T) {
	r := newClobberRepos(t)
	runGit(t, r.work, "fetch", "-q", "origin")
	ok, out := guard(t, r.work, line("refs/tags/v1.2.3", r.base, "refs/tags/v1.2.3", zeroSHA))
	if !ok {
		t.Fatalf("a tag push was refused:\n%s", out)
	}
}

func TestNoClobberMainAllowsOtherBranchesWhileMainIsBehind(t *testing.T) {
	r := newClobberRepos(t)
	runGit(t, r.work, "fetch", "-q", "origin")
	ok, out := guard(t, r.work, line("refs/heads/topic", r.base, "refs/heads/topic", zeroSHA))
	if !ok {
		t.Fatalf("a topic-branch push was refused:\n%s", out)
	}
}

func TestNoClobberMainAllowsAFastForward(t *testing.T) {
	r := newClobberRepos(t)
	runGit(t, r.work, "pull", "-q", "--ff-only", "origin", "main")
	next := commit(t, r.work, "next")
	ok, out := guard(t, r.work, line("refs/heads/main", next, "refs/heads/main", r.ahead))
	if !ok {
		t.Fatalf("a fast-forward of main was refused:\n%s", out)
	}
}

func TestNoClobberMainRefusesAMainThatIsBehind(t *testing.T) {
	r := newClobberRepos(t)
	runGit(t, r.work, "fetch", "-q", "origin")
	ok, out := guard(t, r.work, line("refs/heads/main", r.base, "refs/heads/main", r.base))
	if ok {
		t.Fatalf("pushing a main behind origin was allowed:\n%s", out)
	}
	if !strings.Contains(out, "behind") || !strings.Contains(out, "merge --ff-only") {
		t.Errorf("want the behind message and the ff-only fix, got:\n%s", out)
	}
}

// git's own remote sha can be stale; the guard asks the remote, so a main that
// was a fast-forward of what git last saw is still refused once origin has
// moved on with commits this clone doesn't have.
func TestNoClobberMainRefusesWhenOriginHasCommitsNotFetched(t *testing.T) {
	r := newClobberRepos(t)
	ok, out := guard(t, r.work, line("refs/heads/main", r.base, "refs/heads/main", r.base))
	if ok {
		t.Fatalf("pushing main over unfetched commits was allowed:\n%s", out)
	}
	if !strings.Contains(out, "haven't fetched") {
		t.Errorf("want the unfetched-commits message, got:\n%s", out)
	}
}

// Fails closed: a guard that can't see the remote's main can't clear a push.
func TestNoClobberMainRefusesWhenTheRemoteCannotBeReached(t *testing.T) {
	r := newClobberRepos(t)
	missing := filepath.Join(t.TempDir(), "gone.git")
	ok, out := guardArgs(t, r.work, line("refs/heads/main", r.base, "refs/heads/main", r.base), "origin", missing)
	if ok {
		t.Fatalf("a push the guard couldn't check was allowed:\n%s", out)
	}
	if !strings.Contains(out, "can't reach") {
		t.Errorf("want the unreachable message, got:\n%s", out)
	}
}

// It asks the URL being pushed to, not whatever the remote's name resolves to:
// here the name doesn't resolve at all, and the fast-forward still passes.
func TestNoClobberMainAsksThePushURL(t *testing.T) {
	r := newClobberRepos(t)
	runGit(t, r.work, "pull", "-q", "--ff-only", "origin", "main")
	next := commit(t, r.work, "next")
	ok, out := guardArgs(t, r.work, line("refs/heads/main", next, "refs/heads/main", r.ahead), "no-such-remote", r.origin)
	if !ok {
		t.Fatalf("a fast-forward checked against the push URL was refused:\n%s", out)
	}
}

func TestNoClobberMainRefusesADivergedMain(t *testing.T) {
	r := newClobberRepos(t)
	runGit(t, r.work, "fetch", "-q", "origin")
	mine := commit(t, r.work, "local only")
	ok, out := guard(t, r.work, line("refs/heads/main", mine, "refs/heads/main", r.base))
	if ok {
		t.Fatalf("pushing a diverged main was allowed:\n%s", out)
	}
	// The fix must keep the local commit, so not reset --hard.
	if !strings.Contains(out, "diverged") || !strings.Contains(out, "rebase") || strings.Contains(out, "reset --hard") {
		t.Errorf("want the diverged message with a rebase fix, got:\n%s", out)
	}
}

func TestNoClobberMainRefusesDeletingMain(t *testing.T) {
	r := newClobberRepos(t)
	ok, out := guard(t, r.work, line("(delete)", zeroSHA, "refs/heads/main", r.ahead))
	if ok {
		t.Fatalf("deleting main was allowed:\n%s", out)
	}
}

// A refused main among several refs refuses the push, whatever the order.
func TestNoClobberMainChecksEveryRef(t *testing.T) {
	r := newClobberRepos(t)
	stdin := line("refs/heads/main", r.base, "refs/heads/main", r.base) +
		line("refs/tags/v1.2.3", r.base, "refs/tags/v1.2.3", zeroSHA)
	if ok, out := guard(t, r.work, stdin); ok {
		t.Fatalf("a behind main pushed alongside a tag was allowed:\n%s", out)
	}
}

// The one test through git itself: installed as a pre-push hook, the guard
// stops a real force push of a behind main, and lets a tag push through.
func TestNoClobberMainAsARealPrePushHook(t *testing.T) {
	r := newClobberRepos(t)
	script, err := filepath.Abs("../.lefthook/pre-push/no-clobber-main.sh")
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(r.work, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexec sh '"+script+"' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "push", "-q", "--force", "origin", "main")
	cmd.Dir = r.work
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the force push of a behind main went through:\n%s", out)
	}
	if got := runGit(t, r.origin, "rev-parse", "main"); got != r.ahead {
		t.Fatalf("origin's main moved to %s; want it still at the merged PR %s", got, r.ahead)
	}

	runGit(t, r.work, "tag", "v1.2.3")
	runGit(t, r.work, "push", "-q", "origin", "v1.2.3")
}

// lefthook skips a pre-push *command* when the push carries no new files, and
// a force push of an older main carries none, so as a command this guard never
// runs for the push it exists to stop. Scripts have no such skip. lefthook
// isn't in CI, so this pins the config shape instead.
func TestNoClobberMainIsALefthookScriptWithStdin(t *testing.T) {
	raw, err := os.ReadFile("../lefthook.yml")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		PrePush struct {
			Commands map[string]any `yaml:"commands"`
			Scripts  map[string]struct {
				Runner   string `yaml:"runner"`
				UseStdin bool   `yaml:"use_stdin"`
			} `yaml:"scripts"`
		} `yaml:"pre-push"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for name := range cfg.PrePush.Commands {
		if strings.Contains(name, "clobber") {
			t.Errorf("pre-push command %q: the guard must be a script, or lefthook skips it on a force push", name)
		}
	}
	s, ok := cfg.PrePush.Scripts["no-clobber-main.sh"]
	if !ok {
		t.Fatal("lefthook.yml has no pre-push script no-clobber-main.sh")
	}
	if !s.UseStdin {
		t.Error("no-clobber-main.sh needs use_stdin: true — without git's ref lines it checks nothing")
	}
}
