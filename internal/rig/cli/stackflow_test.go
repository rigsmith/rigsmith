package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The stack verbs are only really exercised against a git server and a real
// engine: this walks the whole cycle — fuse two upstreams, change both in one
// commit, send one project's slice, take upstream's movement back — because
// every bug worth finding here lived in how those pieces meet, not in any one
// of them. Skipped unless RIG_STACK_E2E is set and josh-proxy is installed.
func TestStackFlow(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1 to run the stack end-to-end flow")
	}
	proxy, err := stackJoshProxyBin(stackJoshVersion)
	if err != nil || stackJoshInstalled(proxy) != nil {
		t.Skip("no josh-proxy installed; run `rig stack doctor --fix` first")
	}

	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))

	// Two upstreams and a fork for each — the fork is where `send` pushes, and
	// it has to be a separate repo or the test could not tell them apart.
	for _, name := range []string{"libfoo", "libbar"} {
		srv.seed(t, "org/"+name, name)
		srv.bare(t, "me/"+name)
	}

	ws := filepath.Join(work, "stackspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "init", "-q", "-b", "main")
	mustGitStack(t, ws, "config", "user.email", "t@t")
	mustGitStack(t, ws, "config", "user.name", "t")
	// libfoo uses the current key, libbar the older `branch` spelling, so the
	// whole flow is walked once through each.
	writeStackManifest(t, ws, fmt.Sprintf(`{
  "branchPrefix": "stack/",
  "repos": {
    "libfoo": { "upstream": %[1]q, "fork": %[2]q, "upstreamBranch": "main" },
    "libbar": { "upstream": %[3]q, "fork": %[4]q, "branch": "main" }
  }
}`, srv.spec("org/libfoo"), srv.spec("me/libfoo"),
		srv.spec("org/libbar"), srv.spec("me/libbar")))

	chdir(t, ws)
	ctx := context.Background()

	// ---- init imports both upstreams under their prefixes ----------------

	t.Run("init refuses to swallow unrelated work", func(t *testing.T) {
		stray := filepath.Join(ws, "notes.txt")
		if err := os.WriteFile(stray, []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(stray)
		if err := runVerb(ctx, newStackInitCmd()); err == nil {
			t.Fatal("expected init to refuse a dirty worktree")
		} else if !strings.Contains(err.Error(), "uncommitted") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if err := runVerb(ctx, newStackInitCmd()); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, name := range []string{"libfoo", "libbar"} {
		if _, err := os.Stat(filepath.Join(ws, name, "src", name+".txt")); err != nil {
			t.Fatalf("%s was not imported: %v", name, err)
		}
	}
	// The prefix content must be the upstream tree, not a nested copy of it.
	if _, err := os.Stat(filepath.Join(ws, "libfoo", "libfoo")); err == nil {
		t.Fatal("libfoo was imported under a doubled prefix")
	}

	// ---- one commit spanning both projects -------------------------------

	for _, name := range []string{"libfoo", "libbar"} {
		p := filepath.Join(ws, name, "src", name+".txt")
		if err := os.WriteFile(p, []byte(name+" v2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "cross-cutting: bump both")

	// ---- send extracts one project, and only that project ----------------

	if err := runVerb(ctx, newStackSendCmd(), "libfoo", "bump"); err != nil {
		t.Fatalf("send: %v", err)
	}
	fork := srv.path("me/libfoo")
	// The name given was bare; branchPrefix decides what actually exists.
	if refExists(t, fork, "refs/heads/bump") {
		t.Fatal("send ignored branchPrefix and pushed the bare name")
	}
	upstreamTip := strings.TrimSpace(mustGitStack(t, srv.path("org/libfoo"), "rev-parse", "main"))

	if got := strings.TrimSpace(mustGitStack(t, fork, "rev-parse", "stack/bump^")); got != upstreamTip {
		t.Fatalf("branch is not parented on the upstream tip: %s vs %s", got, upstreamTip)
	}
	if got := strings.TrimSpace(mustGitStack(t, fork, "rev-list", "--count", upstreamTip+"..stack/bump")); got != "1" {
		t.Fatalf("branch holds %s commits, want exactly 1", got)
	}
	files := strings.Fields(mustGitStack(t, fork, "ls-tree", "-r", "--name-only", "stack/bump"))
	for _, f := range files {
		if strings.HasPrefix(f, "libfoo/") || strings.HasPrefix(f, "libbar/") || f == "rig.stack.jsonc" {
			t.Fatalf("branch leaked stackspace layout: %v", files)
		}
	}
	if !contains(files, "src/libfoo.txt") {
		t.Fatalf("branch is missing the project's own file: %v", files)
	}

	// ---- sending again must be able to update the same PR branch ---------

	p := filepath.Join(ws, "libfoo", "src", "libfoo.txt")
	if err := os.WriteFile(p, []byte("libfoo v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "commit", "-qam", "libfoo: v3")
	// Given the full branch name this time: the prefix must not stutter.
	if err := runVerb(ctx, newStackSendCmd(), "libfoo", "stack/bump"); err != nil {
		t.Fatalf("second send to the same branch: %v", err)
	}
	if refExists(t, fork, "refs/heads/stack/stack/bump") {
		t.Fatal("an already-prefixed name was prefixed again")
	}
	if got := strings.TrimSpace(mustGitStack(t, fork, "show", "stack/bump:src/libfoo.txt")); got != "libfoo v3" {
		t.Fatalf("branch was not updated: %q", got)
	}

	// ---- an unchanged project is a no-op, not an empty commit ------------

	// Put libbar back to exactly upstream's content, so the only thing that can
	// make send act is a tree comparison it is not doing.
	if err := os.WriteFile(filepath.Join(ws, "libbar", "src", "libbar.txt"), []byte("libbar v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "commit", "-qam", "libbar: back to upstream's content")
	out, err := runVerbOut(ctx, newStackSendCmd(), "libbar", "noop")
	if err != nil {
		t.Fatalf("no-op send: %v", err)
	}
	if !strings.Contains(out, "nothing to send") {
		t.Fatalf("expected a no-op, got: %s", out)
	}
	if refExists(t, srv.path("me/libbar"), "refs/heads/stack/noop") {
		t.Fatal("no-op send pushed a branch anyway")
	}

	// ---- upstream moves: send must refuse rather than revert it ----------

	srv.commit(t, "org/libfoo", "src/extra.txt", "added upstream\n", "upstream: add extra")
	err = runVerb(ctx, newStackSendCmd(), "libfoo", "stale")
	if err == nil {
		t.Fatal("send accepted a stale cursor — the PR would have reverted upstream")
	}
	if !strings.Contains(err.Error(), "has moved") {
		t.Fatalf("unexpected error: %v", err)
	}

	// ---- pull takes the movement, and then send works again --------------

	if err := runVerb(ctx, newStackPullCmd(), "libfoo"); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "libfoo", "src", "extra.txt")); err != nil {
		t.Fatalf("pull did not bring upstream's new file in: %v", err)
	}
	if out, err := runVerbOut(ctx, newStackPullCmd(), "libfoo"); err != nil {
		t.Fatalf("second pull: %v", err)
	} else if !strings.Contains(out, "nothing to pull") {
		t.Fatalf("a repeated pull should be a no-op, got: %s", out)
	}
	if err := runVerb(ctx, newStackSendCmd(), "libfoo", "after-pull"); err != nil {
		t.Fatalf("send after pull: %v", err)
	}
	newTip := strings.TrimSpace(mustGitStack(t, srv.path("org/libfoo"), "rev-parse", "main"))
	if got := strings.TrimSpace(mustGitStack(t, fork, "rev-parse", "stack/after-pull^")); got != newTip {
		t.Fatalf("branch is not on the new upstream tip: %s vs %s", got, newTip)
	}
	files = strings.Fields(mustGitStack(t, fork, "ls-tree", "-r", "--name-only", "stack/after-pull"))
	if !contains(files, "src/extra.txt") {
		t.Fatalf("the branch dropped upstream's own file — it would revert it: %v", files)
	}
}

// ---- harness ----------------------------------------------------------

// gitServer serves bare repositories over http, which is what josh-proxy needs:
// it accepts an http(s) or ssh upstream and nothing else, so `git daemon` and a
// file:// path are both out. git ships the CGI program that does this; the only
// missing piece is something to host it.
type gitServer struct {
	root string
	addr string
}

func newGitServer(t *testing.T, root string) *gitServer {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	execPath := strings.TrimSpace(mustGitStack(t, root, "--exec-path"))
	backend := filepath.Join(execPath, "git-http-backend"+stackExeSuffix())
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git-http-backend not found at %s", backend)
	}
	srv := httptest.NewServer(&cgi.Handler{
		Path: backend,
		Env: []string{
			"GIT_PROJECT_ROOT=" + root,
			"GIT_HTTP_EXPORT_ALL=1",
		},
	})
	t.Cleanup(srv.Close)
	return &gitServer{root: root, addr: strings.TrimPrefix(srv.URL, "http://")}
}

// spec is the host/owner/name the manifest names this repo by.
func (g *gitServer) spec(name string) string { return g.addr + "/" + name }

// path is the bare repository on disk, for asserting against directly.
func (g *gitServer) path(name string) string { return filepath.Join(g.root, name+".git") }

func (g *gitServer) bare(t *testing.T, name string) string {
	t.Helper()
	dir := g.path(name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, g.root, "init", "-q", "--bare", "-b", "main", dir)
	// Pushes over http are refused unless the repo opts in.
	mustGitStack(t, dir, "config", "http.receivepack", "true")
	return dir
}

// seed creates a bare repo with one commit, so it looks like a real upstream.
func (g *gitServer) seed(t *testing.T, name, file string) {
	t.Helper()
	bare := g.bare(t, name)
	work := t.TempDir()
	mustGitStack(t, work, "init", "-q", "-b", "main", work)
	mustGitStack(t, work, "config", "user.email", "t@t")
	mustGitStack(t, work, "config", "user.name", "t")
	if err := os.MkdirAll(filepath.Join(work, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "src", file+".txt"), []byte(file+" v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, work, "add", ".")
	mustGitStack(t, work, "commit", "-qm", file+": initial")
	mustGitStack(t, work, "push", "-q", bare, "main")
}

// commit adds a commit to an upstream directly, standing in for another
// contributor landing something while the stackspace was not looking.
func (g *gitServer) commit(t *testing.T, name, file, body, msg string) {
	t.Helper()
	bare := g.path(name)
	work := t.TempDir()
	mustGitStack(t, work, "clone", "-q", bare, work)
	mustGitStack(t, work, "config", "user.email", "t@t")
	mustGitStack(t, work, "config", "user.name", "t")
	full := filepath.Join(work, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, work, "add", "-A")
	mustGitStack(t, work, "commit", "-qm", msg)
	mustGitStack(t, work, "push", "-q", "origin", "main")
}

// runVerb executes one stack command the way the CLI would, discarding output.
func runVerb(ctx context.Context, cmd *cobra.Command, args ...string) error {
	_, err := runVerbOut(ctx, cmd, args...)
	return err
}

// runVerbOut is runVerb, returning what the command printed.
func runVerbOut(ctx context.Context, cmd *cobra.Command, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd.SetContext(ctx)
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.RunE(cmd, args)
	return buf.String(), err
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// refExists asks whether a ref is present without failing when it is not —
// mustGitStack treats git's exit status as fatal, and absence is the assertion.
func refExists(t *testing.T, dir, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = dir
	return cmd.Run() == nil
}

func contains(haystack []string, want string) bool {
	for _, h := range haystack {
		if h == want {
			return true
		}
	}
	return false
}
