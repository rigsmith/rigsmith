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

	// ---- seed, then rebuild elsewhere ------------------------------------
	//
	// The seed is the root files alone. init on a clone of it has to bring
	// libfoo back FROM THE FORK BRANCH it was last proposed to — that is where
	// its unmerged v3 lives — and libbar back at its cursor, since nothing of
	// it ever left.

	seed := filepath.Join(work, "seed")
	if err := runVerb(ctx, newStackSeedCmd(), seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(seed, "libfoo")); !os.IsNotExist(err) {
		t.Fatal("seed carries a member directory")
	}
	rebuilt := filepath.Join(work, "rebuilt")
	mustGitStack(t, work, "clone", "-q", seed, rebuilt)
	mustGitStack(t, rebuilt, "config", "user.email", "t@t")
	mustGitStack(t, rebuilt, "config", "user.name", "t")
	chdir(t, rebuilt)
	out, err = runVerbOut(ctx, newStackInitCmd())
	if err != nil {
		t.Fatalf("init on the seed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "libfoo: reconstituted from") || !strings.Contains(out, "stack/after-pull") {
		t.Fatalf("libfoo was not rebuilt from its proposed branch:\n%s", out)
	}
	if !strings.Contains(out, "libbar: reconstituted upstream") {
		t.Fatalf("libbar was not rebuilt at its cursor:\n%s", out)
	}
	if got := strings.TrimSpace(mustGitStack(t, rebuilt, "show", "HEAD:libfoo/src/libfoo.txt")); got != "libfoo v3" {
		t.Fatalf("rebuilt libfoo holds %q, want the proposed v3", got)
	}
	if _, err := os.Stat(filepath.Join(rebuilt, "libfoo", "src", "extra.txt")); err != nil {
		t.Fatal("rebuilt libfoo lacks upstream's own file")
	}
	if got := strings.TrimSpace(mustGitStack(t, rebuilt, "show", "HEAD:libbar/src/libbar.txt")); got != "libbar v1" {
		t.Fatalf("rebuilt libbar holds %q, want upstream's content", got)
	}
	// The cursor is an upstream commit, not the fork branch's tip, so status
	// and pull keep measuring against upstream: nothing has moved, so nothing
	// to pull, and a fresh propose is not refused as stale.
	m, _, err := loadStackManifest(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if m.cursor("libfoo") != newTip {
		t.Fatalf("libfoo cursor = %s, want upstream tip %s", short(m.cursor("libfoo")), short(newTip))
	}
	if out, err := runVerbOut(ctx, newStackPullCmd(), "libfoo"); err != nil || !strings.Contains(out, "nothing to pull") {
		t.Fatalf("pull after rebuild: %v\n%s", err, out)
	}
	if out, err := runVerbOut(ctx, newStackSendCmd(), "libfoo", "after-pull"); err != nil || !strings.Contains(out, "nothing to send") {
		t.Fatalf("propose after rebuild should be a no-op against the same branch: %v\n%s", err, out)
	}
	// ---- propose must not commit a manifest edit of the user's own -------
	//
	// Last, because it deliberately leaves the manifest dirty. Against the
	// rebuilt stackspace, which is what the working directory is by now.

	manifest := filepath.Join(rebuilt, "rig.stack.jsonc")
	keep, rerr := os.ReadFile(manifest)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if werr := os.WriteFile(manifest, append(keep, []byte("// a note of my own\n")...), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if werr := os.WriteFile(filepath.Join(rebuilt, "libfoo", "src", "libfoo.txt"), []byte("libfoo v5\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	mustGitStack(t, rebuilt, "commit", "-qm", "libfoo: v5", "--", "libfoo")
	head := strings.TrimSpace(mustGitStack(t, rebuilt, "rev-parse", "HEAD"))

	if err := runVerb(ctx, newStackSendCmd(), "libfoo", "mine"); err != nil {
		t.Fatalf("propose with a manifest the user has edited: %v", err)
	}
	if now := strings.TrimSpace(mustGitStack(t, rebuilt, "rev-parse", "HEAD")); now != head {
		t.Fatalf("propose committed while the manifest held the user's own edit: %q",
			strings.TrimSpace(mustGitStack(t, rebuilt, "log", "-1", "--format=%s")))
	}
	body, _ := os.ReadFile(manifest)
	if !strings.Contains(string(body), "a note of my own") {
		t.Error("propose discarded the user's manifest edit")
	}
	if !strings.Contains(string(body), "stack/mine") {
		t.Error("propose did not record the branch it pushed to")
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

// ---- dry runs ---------------------------------------------------------

// A dry run of a verb that writes to a remote has one job: to write nothing.
// Both tests below stand up a real (loopback) git server so that "nothing
// reached the remote" is a fact about a repository, not about a mock.

// stackDryRun flips the global --dry-run for one test.
func stackDryRun(t *testing.T) {
	t.Helper()
	prev := dryRun
	dryRun = true
	t.Cleanup(func() { dryRun = prev })
}

// TestStackPushDryRun needs the filter engine on disk: the dry run runs it,
// since the filtered history is what tells the user what would go. It is
// skipped, not downloaded, when the engine is missing — `rig stack doctor
// --fix` (or RIG_STACK_E2E=1 on TestEnsureJoshFilterDownloads) installs it.
func TestStackPushDryRun(t *testing.T) {
	for _, tool := range []string{toolFilter, toolProxy} {
		bin, err := stackJoshToolBin(stackJoshVersion, tool)
		if err != nil || stackJoshInstalled(bin) != nil {
			t.Skipf("no %s installed; run `rig stack doctor --fix` first", tool)
		}
	}
	filter, _ := stackJoshToolBin(stackJoshVersion, toolFilter)

	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "you/app", "app")
	upstream := srv.path("you/app")
	tip := strings.TrimSpace(mustGitStack(t, upstream, "rev-parse", "main"))

	// A stackspace shaped the way init makes one, without the proxy: the
	// upstream history run through the same :prefix filter, merged in under
	// the prefix with --no-ff, and the cursor recorded at the tip.
	ws := filepath.Join(work, "stackspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "init", "-q", "-b", "main")
	mustGitStack(t, ws, "config", "user.email", "t@t")
	mustGitStack(t, ws, "config", "user.name", "t")
	writeStackManifest(t, ws, fmt.Sprintf(`{
  "repos": { "app": { "upstream": %[1]q, "fork": %[1]q, "upstreamBranch": "main", "owned": true } },
  "lastSync": { "app": %[2]q }
}`, srv.spec("you/app"), tip))
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "manifest")
	mustGitStack(t, ws, "fetch", "-q", "--no-tags", upstream, "main")
	prefixed := exec.Command(filter, stackPrefixFilter("app"), "--update", "refs/rigsmith/test/import", "FETCH_HEAD")
	prefixed.Dir = ws
	if out, err := prefixed.CombinedOutput(); err != nil {
		t.Fatalf("josh-filter :prefix=app: %v\n%s", err, out)
	}
	mustGitStack(t, ws, "merge", "-q", "--allow-unrelated-histories", "--no-ff", "-m",
		"stack: import app @ "+short(tip), "refs/rigsmith/test/import")

	// Two stackspace commits, one of which touches app/ and would go.
	if err := os.WriteFile(filepath.Join(ws, "src-app.txt"), []byte("outside every prefix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "root: not app's business")
	if err := os.WriteFile(filepath.Join(ws, "app", "src", "app.txt"), []byte("app v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "commit", "-qam", "app: v2")

	chdir(t, ws)
	stackDryRun(t)
	headBefore := strings.TrimSpace(mustGitStack(t, ws, "rev-parse", "HEAD"))
	manifestBefore, err := os.ReadFile(filepath.Join(ws, "rig.stack.jsonc"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := runVerbOut(context.Background(), newStackPushCmd(), "app")
	if err != nil {
		t.Fatalf("push --dry-run: %v\n%s", err, out)
	}

	// Says what would go, in the future tense, and which commits.
	if !strings.Contains(out, "would push app to "+srv.spec("you/app")+":main (") {
		t.Fatalf("dry run did not name the target and branch:\n%s", out)
	}
	if strings.Contains(out, "pushed ") {
		t.Fatalf("dry run claims to have pushed:\n%s", out)
	}
	if !strings.Contains(out, " app: v2") {
		t.Fatalf("dry run did not list the commit that would go:\n%s", out)
	}
	if strings.Contains(out, "not app's business") || strings.Contains(out, "app: initial") {
		t.Fatalf("dry run lists commits a push would not carry:\n%s", out)
	}

	// The remote is where it was.
	if got := strings.TrimSpace(mustGitStack(t, upstream, "rev-parse", "main")); got != tip {
		t.Fatalf("dry run moved the remote: %s, was %s", short(got), short(tip))
	}
	// And nothing local records a push: no take-back merge, no cursor move.
	if got := strings.TrimSpace(mustGitStack(t, ws, "rev-parse", "HEAD")); got != headBefore {
		t.Fatalf("dry run committed to the stackspace: HEAD %s, was %s", short(got), short(headBefore))
	}
	if manifestAfter, _ := os.ReadFile(filepath.Join(ws, "rig.stack.jsonc")); string(manifestAfter) != string(manifestBefore) {
		t.Fatalf("dry run rewrote the manifest:\n%s", manifestAfter)
	}
	if dirty := strings.TrimSpace(mustGitStack(t, ws, "status", "--porcelain")); dirty != "" {
		t.Fatalf("dry run left the worktree dirty:\n%s", dirty)
	}
	// Nor the filtered ref behind: the preview is read off it, and then it goes.
	if refExists(t, ws, "refs/rigsmith/push/app") {
		t.Fatal("dry run left refs/rigsmith/push/app behind")
	}

	// A ref that was there before a dry run — a real push's leftover — is put
	// back to what it held, not deleted.
	mustGitStack(t, ws, "update-ref", "refs/rigsmith/push/app", headBefore)
	if out, err := runVerbOut(context.Background(), newStackPushCmd(), "app"); err != nil || !strings.Contains(out, "would push app") {
		t.Fatalf("second dry run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(mustGitStack(t, ws, "rev-parse", "refs/rigsmith/push/app")); got != headBefore {
		t.Fatalf("dry run left refs/rigsmith/push/app at %s, was %s", short(got), short(headBefore))
	}
}

// TestStackProposeDryRun needs no engine: propose is plain git, so the only
// requirement is the loopback server the harness provides.
func TestStackProposeDryRun(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	tip := strings.TrimSpace(mustGitStack(t, srv.path("acme/lib"), "rev-parse", "main"))

	// The prefix holds upstream's tree with one file changed; propose reads the
	// tree, not the history, so how it got there does not matter here.
	ws := filepath.Join(work, "stackspace")
	if err := os.MkdirAll(filepath.Join(ws, "lib", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "init", "-q", "-b", "main")
	mustGitStack(t, ws, "config", "user.email", "t@t")
	mustGitStack(t, ws, "config", "user.name", "t")
	writeStackManifest(t, ws, fmt.Sprintf(`{
  "branchPrefix": "stack/",
  "repos": { "lib": { "upstream": %q, "fork": %q, "upstreamBranch": "main" } },
  "lastSync": { "lib": %q }
}`, srv.spec("acme/lib"), srv.spec("you/lib"), tip))
	if err := os.WriteFile(filepath.Join(ws, "lib", "src", "lib.txt"), []byte("lib v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "lib: v2")

	chdir(t, ws)
	stackDryRun(t)
	manifestBefore, err := os.ReadFile(filepath.Join(ws, "rig.stack.jsonc"))
	if err != nil {
		t.Fatal(err)
	}

	out, err := runVerbOut(context.Background(), newStackSendCmd(), "lib", "fix")
	if err != nil {
		t.Fatalf("propose --dry-run: %v\n%s", err, out)
	}

	if !strings.Contains(out, "would push ") || !strings.Contains(out, " to "+srv.spec("you/lib")+":stack/fix (proposing to "+srv.spec("acme/lib")+")") {
		t.Fatalf("dry run did not say what it would push and where:\n%s", out)
	}
	if strings.Contains(out, "proposed ") || strings.Contains(out, "pushed ") {
		t.Fatalf("dry run claims to have proposed:\n%s", out)
	}

	// The fork never heard of the branch.
	if refExists(t, fork, "refs/heads/stack/fix") {
		t.Fatal("dry run pushed the branch to the fork")
	}
	// And nothing local claims it did: no propose ref, no remembered branch.
	if refExists(t, ws, "refs/rigsmith/propose/lib") {
		t.Fatal("dry run recorded the proposal under refs/rigsmith/propose")
	}
	if manifestAfter, _ := os.ReadFile(filepath.Join(ws, "rig.stack.jsonc")); string(manifestAfter) != string(manifestBefore) {
		t.Fatalf("dry run rewrote the manifest:\n%s", manifestAfter)
	}
	if dirty := strings.TrimSpace(mustGitStack(t, ws, "status", "--porcelain")); dirty != "" {
		t.Fatalf("dry run left the worktree dirty:\n%s", dirty)
	}
}

// twoFixStackspace builds the shape --from exists for: a prefix carrying two
// independent fixes, one of which is also a topic branch rooted on the import.
//
//	import ── fix A ───────── merge ── (main: both fixes, what you build and test)
//	   └───── stack-pr-fix-b ────┘      (topic: upstream plus fix B, nothing else)
//
// Returns the stackspace and the import commit, which is what a topic must be
// rooted on for its tree to be one upstream can take.
func twoFixStackspace(t *testing.T, work string, srv *gitServer, trackBranch string) (ws, importSHA string) {
	t.Helper()
	ws = filepath.Join(work, "stackspace")
	if err := os.MkdirAll(filepath.Join(ws, "lib", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "init", "-q", "-b", "main")
	mustGitStack(t, ws, "config", "user.email", "t@t")
	mustGitStack(t, ws, "config", "user.name", "t")

	tip := strings.TrimSpace(mustGitStack(t, srv.path("acme/lib"), "rev-parse", "main"))
	track := ""
	if trackBranch != "" {
		track = fmt.Sprintf(`, "trackBranch": %q`, trackBranch)
	}
	writeStackManifest(t, ws, fmt.Sprintf(`{
  "branchPrefix": "stack/",
  "repos": { "lib": { "upstream": %q, "fork": %q, "upstreamBranch": "main"%s } },
  "lastSync": { "lib": %q }
}`, srv.spec("acme/lib"), srv.spec("you/lib"), track, tip))

	// The import: the prefix holds exactly upstream's tree, which is what makes
	// a topic rooted here proposable as-is.
	if err := os.WriteFile(filepath.Join(ws, "lib", "src", "lib.txt"), []byte("lib v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "add", "-A")
	// The marker message matters: rig finds the commit a member was last imported
	// at by matching it, and that commit — NOT the cursor — is what local
	// ancestry is measured against. The cursor is a raw upstream commit, while a
	// real import merges josh-rewritten content, so upstream's own commit is an
	// ancestor of nothing in a stackspace. An earlier version of this fixture
	// merged the raw upstream commit to make it one, which made the fixture
	// disagree with every real stackspace and hid exactly that bug.
	mustGitStack(t, ws, "commit", "-qm", "stack: import lib @ "+tip[:8])
	importSHA = strings.TrimSpace(mustGitStack(t, ws, "rev-parse", "HEAD"))

	writeFix := func(file string) {
		if err := os.WriteFile(filepath.Join(ws, "lib", "src", file), []byte(file+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, ws, "add", "-A")
		mustGitStack(t, ws, "commit", "-qm", "lib: "+file)
	}

	writeFix("a.txt") // fix A, on main, still in review upstream
	mustGitStack(t, ws, "switch", "-q", "-c", "stack-pr-fix-b", importSHA)
	writeFix("b.txt") // fix B, a topic of its own
	mustGitStack(t, ws, "switch", "-q", "main")
	mustGitStack(t, ws, "merge", "-q", "--no-edit", "stack-pr-fix-b")
	return ws, importSHA
}

// propose runs the verb with --from set. runVerbOut calls RunE directly, which
// is cobra's parsing skipped, so a flag passed as an argument would arrive as a
// positional and be silently ignored — set it on the command instead.
func propose(t *testing.T, repo, branch, from string) (string, error) {
	t.Helper()
	cmd := newStackSendCmd()
	if from != "" {
		if err := cmd.Flags().Set("from", from); err != nil {
			t.Fatal(err)
		}
	}
	return runVerbOut(context.Background(), cmd, repo, branch)
}

// forkTree is the paths a branch of the bare fork holds.
func forkTree(t *testing.T, fork, branch string) string {
	t.Helper()
	return mustGitStack(t, fork, "ls-tree", "-r", "--name-only", branch)
}

// TestStackProposeFromTopic is the point of --from: a prefix carrying two fixes
// can propose one WITHOUT the other, which by default it cannot — propose sends
// the prefix's whole tree.
func TestStackProposeFromTopic(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	ws, _ := twoFixStackspace(t, work, srv, "stack/integration")

	chdir(t, ws)
	// "fix-b" is not a branch; stack-pr-fix-b is. The bare name resolving to the
	// conventional one is the whole ergonomic point of the convention.
	out, err := propose(t, "lib", "fix-b", "fix-b")
	if err != nil {
		t.Fatalf("propose --from: %v\n%s", err, out)
	}
	if !strings.Contains(out, "stack-pr-fix-b") {
		t.Fatalf("did not resolve the bare name to the conventional branch:\n%s", out)
	}
	// What the pull request will contain, said before it is opened. Asserted by
	// subject rather than by count: this fixture's stackspace is a fresh `git
	// init`, so the cursor is not an ancestor of anything here and the range
	// spans the synthetic import commit too. A real stackspace merges upstream
	// in, so the cursor IS an ancestor there — and the property worth pinning is
	// which fix is named, not how many commits the range happened to span.
	if !strings.Contains(out, "lib: b.txt") {
		t.Fatalf("did not name the fix being proposed:\n%s", out)
	}
	if strings.Contains(out, "lib: a.txt") {
		t.Fatalf("reported carrying the other unmerged fix, which this topic does not:\n%s", out)
	}

	// The pull request holds fix B and NOT fix A. Without --from both would be
	// here, and the reviewer would read a change nobody asked them to.
	proposed := forkTree(t, fork, "stack/fix-b")
	if !strings.Contains(proposed, "src/b.txt") {
		t.Fatalf("the proposed branch is missing the topic it was asked for:\n%s", proposed)
	}
	if strings.Contains(proposed, "src/a.txt") {
		t.Fatalf("the proposed branch carries the other unmerged fix too:\n%s", proposed)
	}
	if strings.Contains(proposed, "lib/src/") {
		t.Fatalf("the proposed branch kept the stackspace prefix:\n%s", proposed)
	}

	// And the whole divergence is on trackBranch, or a rebuild would build
	// without fix A while this machine still had it.
	integration := forkTree(t, fork, "stack/integration")
	for _, want := range []string{"src/a.txt", "src/b.txt"} {
		if !strings.Contains(integration, want) {
			t.Fatalf("trackBranch does not carry %s — a rebuild would be missing it:\n%s", want, integration)
		}
	}
	if !refExists(t, ws, "refs/rigsmith/integration/lib") {
		t.Fatal("the integration push was not recorded, so seed will call the unproposed work unsent")
	}
}

// TestStackProposeFromTopicOnUnmergedWork: a topic branched off a line that
// already carries another unmerged fix genuinely contains that fix, and rig
// cannot tell "that came along by accident" from "that is part of my change".
// So it does not refuse — it says what the pull request will contain, which is
// what lets the author notice a subject they did not expect.
func TestStackProposeFromTopicOnUnmergedWork(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	ws, _ := twoFixStackspace(t, work, srv, "stack/integration")

	// Rooted on main, which already carries fix A.
	mustGitStack(t, ws, "switch", "-q", "-c", "stack-pr-fix-c", "main")
	if err := os.WriteFile(filepath.Join(ws, "lib", "src", "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "lib: c.txt")
	mustGitStack(t, ws, "switch", "-q", "main")

	chdir(t, ws)
	out, err := propose(t, "lib", "fix-c", "fix-c")
	if err != nil {
		t.Fatalf("propose --from: %v\n%s", err, out)
	}
	// The report names the fix that came along, which is the whole point of it.
	if !strings.Contains(out, "lib: a.txt") {
		t.Fatalf("did not name the other fix this proposal carries:\n%s", out)
	}
	// And the branch really does carry it — the report is not a warning about
	// something that did not happen.
	proposed := forkTree(t, fork, "stack/fix-c")
	if !strings.Contains(proposed, "src/a.txt") {
		t.Fatalf("expected the topic to carry the other fix:\n%s", proposed)
	}
}

// TestStackProposeFromNeedsTrackBranch: without somewhere to keep the fixes it
// leaves out, a topic proposal makes every rebuild silently short. That is
// invisible from here — this worktree still has everything — so it refuses.
func TestStackProposeFromNeedsTrackBranch(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	ws, _ := twoFixStackspace(t, work, srv, "")

	chdir(t, ws)
	out, err := propose(t, "lib", "fix-b", "fix-b")
	if err == nil {
		t.Fatalf("proposed a topic with nowhere to keep the rest:\n%s", out)
	}
	if !strings.Contains(err.Error(), "trackBranch") {
		t.Fatalf("the error does not name what to set: %v", err)
	}
	if refExists(t, fork, "refs/heads/stack/fix-b") {
		t.Fatal("it pushed before refusing")
	}
}

// TestStackProposeWholePrefixByDefault pins the default that --from exists to
// escape: no topic means the branch carries everything the prefix has.
func TestStackProposeWholePrefixByDefault(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	ws, _ := twoFixStackspace(t, work, srv, "")

	chdir(t, ws)
	if out, err := propose(t, "lib", "everything", ""); err != nil {
		t.Fatalf("propose: %v\n%s", err, out)
	}
	proposed := forkTree(t, fork, "stack/everything")
	for _, want := range []string{"src/a.txt", "src/b.txt"} {
		if !strings.Contains(proposed, want) {
			t.Fatalf("an unselected propose left out %s:\n%s", want, proposed)
		}
	}
}

// TestStackProposeFromUnconventionalBranch pins that the naming convention is a
// recommendation. A branch the user named themselves is taken as given — an
// exact match wins, and nothing requires the stack-pr- prefix.
func TestStackProposeFromUnconventionalBranch(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	ws, importSHA := twoFixStackspace(t, work, srv, "stack/integration")

	// No prefix, and rooted on the import like any other topic.
	mustGitStack(t, ws, "switch", "-q", "-c", "my-own-name", importSHA)
	if err := os.WriteFile(filepath.Join(ws, "lib", "src", "d.txt"), []byte("d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, ws, "add", "-A")
	mustGitStack(t, ws, "commit", "-qm", "lib: d.txt")
	mustGitStack(t, ws, "switch", "-q", "main")
	mustGitStack(t, ws, "merge", "-q", "--no-edit", "my-own-name")

	chdir(t, ws)
	if out, err := propose(t, "lib", "fix-d", "my-own-name"); err != nil {
		t.Fatalf("propose --from an unprefixed branch: %v\n%s", err, out)
	}
	proposed := forkTree(t, fork, "stack/fix-d")
	if !strings.Contains(proposed, "src/d.txt") {
		t.Fatalf("the proposed branch is missing the topic:\n%s", proposed)
	}
	for _, unwanted := range []string{"src/a.txt", "src/b.txt"} {
		if strings.Contains(proposed, unwanted) {
			t.Fatalf("the proposed branch carries %s, which is another unmerged fix:\n%s", unwanted, proposed)
		}
	}
}

// TestStackProposeFromStaleTopic: a topic rooted before upstream moved holds the
// prefix as upstream USED to be. Committing that onto the current tip presents
// everything upstream landed since as though the branch had reverted it — the
// hazard the whole-prefix path is protected from by the stale-cursor guard, and
// which --from cannot see there because HEAD gets pulled and a topic does not.
//
// The pull is simulated rather than run: advancing the cursor and merging
// upstream is exactly what an import does to HEAD, and doing it by hand keeps
// this out of the engine-gated tests.
func TestStackProposeFromStaleTopic(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	ws, _ := twoFixStackspace(t, work, srv, "stack/integration")

	// Upstream lands something, and the stackspace takes it — but the topic,
	// like any branch, does not come along.
	srv.commit(t, "acme/lib", "src/upstream.txt", "new upstream work\n", "lib: upstream moved on")
	newTip := strings.TrimSpace(mustGitStack(t, srv.path("acme/lib"), "rev-parse", "main"))
	writeStackManifest(t, ws, fmt.Sprintf(`{
  "branchPrefix": "stack/",
  "repos": { "lib": { "upstream": %q, "fork": %q, "upstreamBranch": "main", "trackBranch": "stack/integration" } },
  "lastSync": { "lib": %q }
}`, srv.spec("acme/lib"), srv.spec("you/lib"), newTip))
	mustGitStack(t, ws, "add", "-A")
	// A new import marker on main that the topic does not contain — which is
	// precisely what "rooted before the last pull" means.
	mustGitStack(t, ws, "commit", "-qm", "stack: pull lib @ "+newTip[:8])

	chdir(t, ws)
	out, err := propose(t, "lib", "fix-b", "fix-b")
	if err == nil {
		t.Fatalf("proposed a topic that predates the pull — it would revert upstream's new work:\n%s", out)
	}
	// Matched on the parts that carry the meaning, not the whole sentence: this
	// assertion already went stale once when the guard moved from the cursor to
	// the import marker and the wording moved with it.
	for _, want := range []string{"was rooted before", "revert the commits"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error does not say why it refused (missing %q): %v", want, err)
		}
	}
	if refExists(t, fork, "refs/heads/stack/fix-b") {
		t.Fatal("it pushed before refusing")
	}

	// And status says so unprompted, since the pull that caused it may have been
	// days ago.
	statusOut, serr := runVerbOut(context.Background(), newStackStatusCmd())
	if serr != nil {
		t.Fatalf("status: %v\n%s", serr, statusOut)
	}
	if !strings.Contains(statusOut, "rooted before the last pull") || !strings.Contains(statusOut, "stack-pr-fix-b") {
		t.Fatalf("status did not name the stale topic:\n%s", statusOut)
	}
}

// TestStackProposeFromOntoTrackBranch: proposing onto trackBranch would have the
// integration push overwrite the pull request with the whole divergence — a
// reviewer opening a topic's PR and finding every carried fix in it. The two
// branches serve opposite purposes, so they cannot be the same one.
func TestStackProposeFromOntoTrackBranch(t *testing.T) {
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/lib", "lib")
	fork := srv.bare(t, "you/lib")
	// trackBranch is stack/integration, so proposing "integration" collides.
	ws, _ := twoFixStackspace(t, work, srv, "stack/integration")

	chdir(t, ws)
	out, err := propose(t, "lib", "integration", "fix-b")
	if err == nil {
		t.Fatalf("proposed onto trackBranch — the PR would be overwritten:\n%s", out)
	}
	if !strings.Contains(err.Error(), "trackBranch") {
		t.Fatalf("the error does not say what collided: %v", err)
	}
	if refExists(t, fork, "refs/heads/stack/integration") {
		t.Fatal("it pushed before refusing")
	}
}
