package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

func writeStackManifest(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const stackTestManifest = `{
  // comments must survive: the manifest is jsonc
  "repos": {
    "pty-core": {
      "upstream": "github.com/acme/pty-core",
      "fork":     "github.com/you/pty-core",
      "branch":   "main"
    },
    "term-core": {
      "upstream": "github.com/acme/term-core",
      "fork":     "github.com/you/term-core"
    }
  }
}
`

func TestLoadWsManifest(t *testing.T) {
	t.Run("dedicated jsonc file with comments", func(t *testing.T) {
		root := t.TempDir()
		writeStackManifest(t, root, stackTestManifest)
		m, src, err := loadStackManifest(root)
		if err != nil {
			t.Fatal(err)
		}
		if src == nil || src.Path == "" {
			t.Fatalf("expected a dedicated-file source, got %+v", src)
		}
		if got := m.branch("pty-core"); got != "main" {
			t.Fatalf("branch = %q", got)
		}
		if got := m.branch("term-core"); got != "main" {
			t.Fatalf("default branch = %q, want main", got)
		}
		if names := m.names(); strings.Join(names, ",") != "pty-core,term-core" {
			t.Fatalf("names = %v, want sorted stable order", names)
		}
	})

	t.Run("no manifest -> nil, nil", func(t *testing.T) {
		m, src, err := loadStackManifest(t.TempDir())
		if err != nil || m != nil || src != nil {
			t.Fatalf("got %v %v %v, want all nil", m, src, err)
		}
	})

	t.Run("a scheme-carrying spec is reduced, not rejected", func(t *testing.T) {
		root := t.TempDir()
		writeStackManifest(t, root, `{"repos":{"x":{"upstream":"https://github.com/a/b.git","fork":"github.com/c/d"}}}`)
		m, _, err := loadStackManifest(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Repos["x"].Upstream; got != "github.com/a/b" {
			t.Fatalf("upstream = %q, want it reduced to host/owner/name", got)
		}
	})

	t.Run("a spec that is no repo at all is still refused", func(t *testing.T) {
		root := t.TempDir()
		writeStackManifest(t, root, `{"repos":{"x":{"upstream":"https://github.com/a/b/tree/main","fork":"github.com/c/d"}}}`)
		if _, _, err := loadStackManifest(root); err == nil {
			t.Fatal("expected a validation error for a spec with extra path segments")
		}
	})

	t.Run("inline stack key in .rig.json is a source too", func(t *testing.T) {
		root := t.TempDir()
		body := `{"stack": {"repos": {"a": {"upstream": "github.com/u/a", "fork": "github.com/f/a"}}}}`
		if err := os.WriteFile(filepath.Join(root, ".rig.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		m, src, err := loadStackManifest(root)
		if err != nil {
			t.Fatal(err)
		}
		if src.Path != "" {
			t.Fatalf("inline source should have empty Path, got %q", src.Path)
		}
		if m.Repos["a"] == nil {
			t.Fatal("inline manifest not parsed")
		}
	})

	t.Run("both file and inline key is a loud error", func(t *testing.T) {
		root := t.TempDir()
		writeStackManifest(t, root, stackTestManifest)
		if err := os.WriteFile(filepath.Join(root, ".rig.json"), []byte(`{"stack": {"repos": {}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadStackManifest(root); err == nil {
			t.Fatal("expected ambiguity error")
		}
	})
}

func TestWsSetCursor_PreservesComments(t *testing.T) {
	root := t.TempDir()
	writeStackManifest(t, root, stackTestManifest)
	m, src, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := stackSetCursor(src, m, "pty-core", "abc123def456"); err != nil {
		t.Fatal(err)
	}
	m2, src2, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.cursor("pty-core"); got != "abc123def456" {
		t.Fatalf("lastSync = %q", got)
	}
	// A second cursor lands beside the first, not over it.
	if err := stackSetCursor(src2, m2, "term-core", "fedcba"); err != nil {
		t.Fatal(err)
	}
	m3, _, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if m3.cursor("pty-core") != "abc123def456" || m3.cursor("term-core") != "fedcba" {
		t.Fatalf("cursors = %v", m3.LastSync)
	}
	data, _ := os.ReadFile(filepath.Join(root, "rig.stack.jsonc"))
	if !strings.Contains(string(data), "comments must survive") {
		t.Fatal("cursor write dropped the manifest's comments")
	}
}

func TestStackUpstreamBranch(t *testing.T) {
	repo := func(m *stackManifest) string { return m.branch("x") }

	t.Run("defaults to main", func(t *testing.T) {
		m := &stackManifest{Repos: map[string]*stackRepo{"x": {}}}
		if got := repo(m); got != "main" {
			t.Fatalf("branch = %q", got)
		}
	})

	t.Run("upstreamBranch wins", func(t *testing.T) {
		m := &stackManifest{Repos: map[string]*stackRepo{"x": {UpstreamBranch: "trunk"}}}
		if got := repo(m); got != "trunk" {
			t.Fatalf("branch = %q", got)
		}
	})

	t.Run("the old branch key still works", func(t *testing.T) {
		m := &stackManifest{Repos: map[string]*stackRepo{"x": {Branch: "develop"}}}
		if got := repo(m); got != "develop" {
			t.Fatalf("branch = %q", got)
		}
	})

	t.Run("disagreeing spellings are refused", func(t *testing.T) {
		m := &stackManifest{Repos: map[string]*stackRepo{
			"x": {Upstream: "h/o/n", Fork: "h/me/n", UpstreamBranch: "trunk", Branch: "develop"},
		}}
		err := m.validate()
		if err == nil || !strings.Contains(err.Error(), "old name") {
			t.Fatalf("expected a refusal naming the old key, got %v", err)
		}
	})
}

func TestStackSendBranch(t *testing.T) {
	ptr := func(s string) *string { return &s }
	base := func(repo *stackRepo, workspace *string) *stackManifest {
		return &stackManifest{BranchPrefix: workspace, Repos: map[string]*stackRepo{"x": repo}}
	}

	cases := []struct {
		name  string
		m     *stackManifest
		given string
		want  string
	}{
		{"defaults to stack/", base(&stackRepo{}, nil), "read-timeout", "stack/read-timeout"},
		{"workspace override", base(&stackRepo{}, ptr("jc-")), "read-timeout", "jc-read-timeout"},
		{"workspace opt-out", base(&stackRepo{}, ptr("")), "read-timeout", "read-timeout"},
		{"repo beats workspace", base(&stackRepo{BranchPrefix: ptr("pr/")}, ptr("jc-")), "x", "pr/x"},
		{"repo opts out alone", base(&stackRepo{BranchPrefix: ptr("")}, ptr("jc-")), "x", "x"},
		{"already prefixed is left alone", base(&stackRepo{}, nil), "stack/read-timeout", "stack/read-timeout"},
		{"a prefix without a slash still concatenates", base(&stackRepo{}, ptr("jc-")), "fix/x", "jc-fix/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.m.sendBranch("x", c.given); got != c.want {
				t.Fatalf("sendBranch(%q) = %q, want %q", c.given, got, c.want)
			}
		})
	}

	t.Run("a prefix git would reject is refused", func(t *testing.T) {
		for _, bad := range []string{
			"/lead", "-lead", "has space", "dot..dot", "double//slash",
			".review/", "stack/@{", "a/.hidden/", "x.lock/", "tilde~", "colon:",
		} {
			m := &stackManifest{
				BranchPrefix: ptr(bad),
				Repos:        map[string]*stackRepo{"x": {Upstream: "h/o/n", Fork: "h/me/n"}},
			}
			if err := m.validate(); err == nil {
				t.Fatalf("accepted branch prefix %q", bad)
			}
		}
	})
}

func TestJoshURL(t *testing.T) {
	p := &joshProxy{port: 4242}
	got := p.url("acme/pty-core", "abc123", stackPrefixFilter("pty-core"))
	// ':' and '=' are legal in a path segment and stay literal, matching the
	// filter syntax josh documents; only a separator like '/' is escaped.
	want := "http://127.0.0.1:4242/acme/pty-core.git@abc123:prefix=pty-core.git"
	if got != want {
		t.Fatalf("url:\n got %s\nwant %s", got, want)
	}
	if got := p.url("a/b", "", ":/x"); got != "http://127.0.0.1:4242/a/b.git:%2Fx.git" {
		t.Fatalf("no-commit url = %s", got)
	}
}

func TestWsSplitHost(t *testing.T) {
	host, path := stackSplitHost("github.com/acme/pty-core")
	if host != "github.com" || path != "acme/pty-core" {
		t.Fatalf("got %q %q", host, path)
	}
}

// fakeProxySource is a stand-in for josh-proxy: it binds the --port it is given
// and holds it. Built with the toolchain already running the tests rather than
// scripted around `nc`, whose -l flag takes a host on BSD/openbsd netcat and
// refuses one on netcat-traditional — LookPath proves the binary exists, not
// that it speaks the syntax. Compiling also means the lifecycle (including the
// Windows stop path) is covered on every platform, not just the shell ones.
const fakeProxySource = `package main

import ("net";"os";"strings";"time")

func main() {
	port := ""
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "--port=") {
			port = strings.TrimPrefix(a, "--port=")
		}
	}
	if os.Getenv("FAKE_PROXY_EXIT") != "" {
		os.Exit(0)
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		os.Exit(1)
	}
	defer l.Close()
	time.Sleep(2 * time.Minute)
}
`

// buildFakeProxy compiles fakeProxySource and returns the binary's path.
func buildFakeProxy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fakeProxySource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fakeproxy\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "josh-proxy"+stackExeSuffix())
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("could not build the fake proxy: %v\n%s", err, out)
	}
	return bin
}

// TestStartJoshProxy_FakeBinary exercises the spawn/poll/stop lifecycle with a
// stand-in that listens like the real proxy, so the lifecycle is covered
// without a Rust toolchain anywhere near CI.
func TestStartJoshProxy_FakeBinary(t *testing.T) {
	p, err := startJoshProxy(context.Background(), buildFakeProxy(t), "github.com")
	if err != nil {
		t.Fatal(err)
	}
	// The log has to exist while the proxy runs — it is the only account of a
	// filter or fetch failure — and be gone once it stops, or every operation
	// leaves one behind.
	log := p.log
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("no engine log while the proxy is running: %v", err)
	}
	p.stop() // must terminate promptly and not leak the process
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("stop left the engine log behind at %s", log)
	}
}

func TestStartJoshProxy_NeverReady(t *testing.T) {
	bin := buildFakeProxy(t)
	t.Setenv("FAKE_PROXY_EXIT", "1")
	_, err := startJoshProxy(context.Background(), bin, "github.com")
	if err == nil {
		t.Fatal("expected readiness failure for a proxy that exits immediately")
	}
	if !strings.Contains(err.Error(), "exited before becoming ready") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGitrepoWsAdditions(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	repo, err := gitrepo.Init(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, src, "config", "user.email", "t@t")
	mustGitStack(t, src, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, src, "add", ".")
	mustGitStack(t, src, "commit", "-m", "one")
	head, err := repo.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("LsRemote resolves a local repo's branch", func(t *testing.T) {
		branch, err := repo.CurrentBranch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sha, err := repo.LsRemote(ctx, src, "refs/heads/"+branch)
		if err != nil {
			t.Fatal(err)
		}
		if sha != head {
			t.Fatalf("ls-remote = %s, head = %s", sha, head)
		}
		if _, err := repo.LsRemote(ctx, src, "refs/heads/nope"); err == nil {
			t.Fatal("missing ref should error, not return empty")
		}
	})

	t.Run("CommitAmendNoEdit folds staged changes in", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(src, "g.txt"), []byte("2"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, src, "add", ".")
		newHead, err := repo.CommitAmendNoEdit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if newHead == head {
			t.Fatal("amend did not move HEAD")
		}
		out := mustGitStack(t, src, "rev-list", "--count", "HEAD")
		if strings.TrimSpace(out) != "1" {
			t.Fatalf("amend created a second commit: count=%s", out)
		}
	})

	t.Run("FetchMergeUnrelated fuses a foreign history", func(t *testing.T) {
		other := t.TempDir()
		if _, err := gitrepo.Init(ctx, other); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, other, "config", "user.email", "t@t")
		mustGitStack(t, other, "config", "user.name", "t")
		if err := os.WriteFile(filepath.Join(other, "lib.txt"), []byte("lib"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, other, "add", ".")
		mustGitStack(t, other, "commit", "-m", "import me")
		otherBranch := strings.TrimSpace(mustGitStack(t, other, "branch", "--show-current"))

		conflicted, err := repo.FetchMergeUnrelated(ctx, other, otherBranch, "stack: import other")
		if err != nil {
			t.Fatal(err)
		}
		if conflicted {
			t.Fatal("unexpected conflicts")
		}
		if _, err := os.Stat(filepath.Join(src, "lib.txt")); err != nil {
			t.Fatal("imported file missing after merge")
		}
	})
}

func mustGitStack(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestStackMenuAndCompletion(t *testing.T) {
	// Both read the manifest through the working directory, so the tests run
	// from inside a temp workspace rather than passing a root around.
	inWorkspace := func(t *testing.T, manifest string) {
		t.Helper()
		dir := t.TempDir()
		// The workspace root is the git top level, so the fixture has to be a
		// repository — outside one there is no stack workspace to find.
		mustGitStack(t, dir, "init", "-q", "-b", "main")
		if manifest != "" {
			writeStackManifest(t, dir, manifest)
		}
		prev, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(prev) })
	}

	t.Run("only init is offered before a manifest exists", func(t *testing.T) {
		inWorkspace(t, "")
		items := stackMenuItems()
		if len(items) != 1 || items[0].label != "init" {
			t.Fatalf("expected just init, got %v", items)
		}
	})

	t.Run("a scaffold that will not load still offers init", func(t *testing.T) {
		// What `stack init` writes: a manifest whose repos block is still empty.
		inWorkspace(t, "{\n  \"repos\": {}\n}\n")
		items := stackMenuItems()
		if len(items) != 1 || items[0].label != "init" {
			t.Fatalf("expected init to stay reachable, got %v", items)
		}
	})

	t.Run("no menu group outside a git repo", func(t *testing.T) {
		prev, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		// t.TempDir under /var on macOS is not inside any repository.
		if err := os.Chdir(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(prev) })
		if items := stackMenuItems(); items != nil {
			t.Fatalf("expected no items, got %d", len(items))
		}
	})

	t.Run("menu offers every stack verb inside a workspace", func(t *testing.T) {
		inWorkspace(t, stackTestManifest)
		var labels []string
		for _, it := range stackMenuItems() {
			labels = append(labels, it.label)
		}
		want := "init,status,pull,send,doctor"
		if got := strings.Join(labels, ","); got != want {
			t.Fatalf("menu = %q, want %q", got, want)
		}
	})

	t.Run("completion offers the workspace's repos", func(t *testing.T) {
		inWorkspace(t, stackTestManifest)
		got, _ := stackRepoCompletion(nil, nil, "")
		if strings.Join(got, ",") != "pty-core,term-core" {
			t.Fatalf("completion = %v", got)
		}
	})

	t.Run("completion stops after the repo argument", func(t *testing.T) {
		inWorkspace(t, stackTestManifest)
		if got, _ := stackRepoCompletion(nil, []string{"pty-core"}, ""); got != nil {
			t.Fatalf("expected no completions for the second argument, got %v", got)
		}
	})
}

func TestStackNormalizeSpec(t *testing.T) {
	const want = "github.com/acme/pty-core"
	for _, in := range []string{
		"github.com/acme/pty-core",
		"github.com/acme/pty-core.git",
		"github.com/acme/pty-core/",
		"  github.com/acme/pty-core  ",
		"https://github.com/acme/pty-core",
		"https://github.com/acme/pty-core.git",
		"https://github.com/acme/pty-core/",
		"http://github.com/acme/pty-core",
		"git@github.com:acme/pty-core.git",
		"ssh://git@github.com/acme/pty-core.git",
		"git://github.com/acme/pty-core.git",
	} {
		if got := stackNormalizeSpec(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}

	t.Run("an IPv6 host survives every form", func(t *testing.T) {
		// The scp branch cuts at a colon; an IPv6 literal is made of them, so
		// only one after the closing bracket can be the host/path separator.
		for in, want := range map[string]string{
			"[::1]/acme/pty-core":                "[::1]/acme/pty-core",
			"https://[::1]/acme/pty-core.git":    "[::1]/acme/pty-core",
			"git@[::1]:acme/pty-core.git":        "[::1]/acme/pty-core",
			"ssh://git@[::1]:2222/acme/pty-core": "[::1]:2222/acme/pty-core",
		} {
			if got := stackNormalizeSpec(in); got != want {
				t.Errorf("%q -> %q, want %q", in, got, want)
			}
		}
	})

	t.Run("a query or fragment is not part of the repo", func(t *testing.T) {
		// Left attached it also defeats the .git trim, so the spec keeps one
		// suffix and gains another when the URL is rebuilt.
		for _, in := range []string{
			"https://github.com/acme/pty-core.git?tab=readme",
			"https://github.com/acme/pty-core#readme",
			"https://github.com/acme/pty-core/?foo=bar",
		} {
			if got := stackNormalizeSpec(in); got != "github.com/acme/pty-core" {
				t.Errorf("%q -> %q", in, got)
			}
		}
	})

	t.Run("a host with a port survives", func(t *testing.T) {
		// The scp-style branch keys off "@", so a port's colon must not be
		// mistaken for the host/path separator.
		for in, want := range map[string]string{
			"localhost:8080/acme/pty-core":         "localhost:8080/acme/pty-core",
			"https://localhost:8080/acme/pty-core": "localhost:8080/acme/pty-core",
		} {
			if got := stackNormalizeSpec(in); got != want {
				t.Errorf("%q -> %q, want %q", in, got, want)
			}
		}
	})

	t.Run("what it cannot read it leaves alone", func(t *testing.T) {
		// So validate reports the real problem rather than one this introduced.
		for _, in := range []string{"", "nonsense", "acme/pty-core"} {
			if got := stackNormalizeSpec(in); got != strings.TrimSpace(in) {
				t.Errorf("%q -> %q, want it unchanged", in, got)
			}
		}
	})

	t.Run("a pasted URL loads and validates", func(t *testing.T) {
		root := t.TempDir()
		writeStackManifest(t, root, `{
  "repos": {
    "pty-core": {
      "upstream": "https://github.com/acme/pty-core.git",
      "fork": "git@github.com:you/pty-core.git"
    }
  }
}`)
		m, _, err := loadStackManifest(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Repos["pty-core"].Upstream; got != "github.com/acme/pty-core" {
			t.Errorf("upstream = %q", got)
		}
		if got := m.Repos["pty-core"].Fork; got != "github.com/you/pty-core" {
			t.Errorf("fork = %q", got)
		}
	})
}

func TestStackPin(t *testing.T) {
	pin := func(r *stackRepo) stackPin {
		return (&stackManifest{Repos: map[string]*stackRepo{"x": r}}).pin("x")
	}

	t.Run("a branch is what a prefix follows by default", func(t *testing.T) {
		if got := pin(&stackRepo{}); got.Kind != "branch" || got.Value != "main" || got.pinned() {
			t.Fatalf("pin = %+v, want the main branch, unpinned", got)
		}
	})

	t.Run("a tag pins it", func(t *testing.T) {
		got := pin(&stackRepo{UpstreamTag: "v1.4.2"})
		if got.Kind != "tag" || got.Value != "v1.4.2" || !got.pinned() {
			t.Fatalf("pin = %+v, want a pinned tag", got)
		}
		if got.describe() != "tag v1.4.2" {
			t.Fatalf("describe = %q", got.describe())
		}
	})

	t.Run("a commit pins it", func(t *testing.T) {
		sha := strings.Repeat("a", 40)
		if got := pin(&stackRepo{UpstreamCommit: sha}); got.Kind != "commit" || !got.pinned() {
			t.Fatalf("pin = %+v, want a pinned commit", got)
		}
	})

	t.Run("two upstream points are refused", func(t *testing.T) {
		for _, r := range []*stackRepo{
			{Upstream: "h/o/n", Fork: "h/me/n", UpstreamBranch: "main", UpstreamTag: "v1"},
			{Upstream: "h/o/n", Fork: "h/me/n", UpstreamTag: "v1", UpstreamCommit: strings.Repeat("a", 40)},
			{Upstream: "h/o/n", Fork: "h/me/n", Branch: "old", UpstreamCommit: strings.Repeat("a", 40)},
		} {
			m := &stackManifest{Repos: map[string]*stackRepo{"x": r}}
			if err := m.validate(); err == nil || !strings.Contains(err.Error(), "one upstream point") {
				t.Errorf("expected a refusal naming both keys, got %v", err)
			}
		}
	})

	t.Run("an abbreviated commit is refused where it is written", func(t *testing.T) {
		m := &stackManifest{Repos: map[string]*stackRepo{
			"x": {Upstream: "h/o/n", Fork: "h/me/n", UpstreamCommit: "abc1234"},
		}}
		// Resolving an abbreviation needs the object, which is the very thing the
		// pin decides whether to fetch — so it has to be rejected up front.
		if err := m.validate(); err == nil || !strings.Contains(err.Error(), "40-character") {
			t.Fatalf("expected a refusal naming the length, got %v", err)
		}
	})
}

func TestStackResolveUpstream(t *testing.T) {
	ctx := context.Background()
	git := func(dir string, args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	upstream := t.TempDir()
	git(upstream, "init", "-q", "-b", "main", upstream)
	os.WriteFile(filepath.Join(upstream, "a.txt"), []byte("one"), 0o644)
	git(upstream, "add", "-A")
	git(upstream, "commit", "-qm", "one")
	first := git(upstream, "rev-parse", "HEAD")
	git(upstream, "tag", "light")                             // lightweight: points at the commit
	git(upstream, "tag", "-a", "heavy", "-m", "an annotated") // annotated: points at a tag object
	os.WriteFile(filepath.Join(upstream, "a.txt"), []byte("two"), 0o644)
	git(upstream, "commit", "-qam", "two")
	tip := git(upstream, "rev-parse", "HEAD")

	here, err := gitrepo.Open(ctx, upstream)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		pin  stackPin
		want string
	}{
		{"a branch resolves to its tip", stackPin{Kind: "branch", Value: "main"}, tip},
		{"a lightweight tag resolves to its commit", stackPin{Kind: "tag", Value: "light"}, first},
		// The peeled entry is the point: an annotated tag's own SHA is a tag
		// object, which josh cannot serve and the cursor must never record.
		{"an annotated tag is peeled to its commit", stackPin{Kind: "tag", Value: "heavy"}, first},
		{"a commit is already the answer", stackPin{Kind: "commit", Value: first}, first},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stackResolveUpstream(ctx, here, upstream, tc.pin)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %s, want %s", short(got), short(tc.want))
			}
		})
	}

	t.Run("a tag that does not exist says so", func(t *testing.T) {
		_, err := stackResolveUpstream(ctx, here, upstream, stackPin{Kind: "tag", Value: "nope"})
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("got %v, want a not-found error", err)
		}
	})
}

func TestStackPinnedCursor(t *testing.T) {
	const sha = "6a78155eee4a0100c5cfb664dd7fc2782cd1c24c"
	manifest := func(r *stackRepo, pin string) *stackManifest {
		m := &stackManifest{
			Repos:    map[string]*stackRepo{"lib": r},
			LastSync: map[string]string{"lib": sha},
		}
		if pin != "" {
			m.LastPin = map[string]string{"lib": pin}
		}
		return m
	}

	t.Run("a settled pin is not resolved again", func(t *testing.T) {
		// The point of the whole thing: upstream may have moved the tag since,
		// and the workspace must not move with it.
		got, ok := stackPinnedCursor(manifest(&stackRepo{UpstreamTag: "v1"}, "tag v1"), "lib")
		if !ok || got != sha {
			t.Fatalf("got (%q, %v), want the recorded cursor", got, ok)
		}
	})

	t.Run("editing the pin resolves again", func(t *testing.T) {
		// The recorded selector no longer matches, which is how a deliberate
		// repin is told apart from a tag that moved underneath one.
		if _, ok := stackPinnedCursor(manifest(&stackRepo{UpstreamTag: "v2"}, "tag v1"), "lib"); ok {
			t.Fatal("reused a cursor resolved under a different pin")
		}
	})

	t.Run("a branch always resolves", func(t *testing.T) {
		if _, ok := stackPinnedCursor(manifest(&stackRepo{UpstreamBranch: "main"}, ""), "lib"); ok {
			t.Fatal("a branch is meant to move")
		}
	})

	t.Run("a pin with nothing recorded resolves", func(t *testing.T) {
		// A manifest written before this existed, or a first import.
		if _, ok := stackPinnedCursor(manifest(&stackRepo{UpstreamTag: "v1"}, ""), "lib"); ok {
			t.Fatal("reused a cursor with no recorded pin")
		}
	})

	t.Run("an unimported pin resolves", func(t *testing.T) {
		m := manifest(&stackRepo{UpstreamTag: "v1"}, "tag v1")
		m.LastSync = nil
		if _, ok := stackPinnedCursor(m, "lib"); ok {
			t.Fatal("reused a cursor that does not exist")
		}
	})

	t.Run("a commit pin records too, so its selector can change", func(t *testing.T) {
		other := strings.Repeat("b", 40)
		got, ok := stackPinnedCursor(manifest(&stackRepo{UpstreamCommit: sha}, "commit "+sha), "lib")
		if !ok || got != sha {
			t.Fatalf("got (%q, %v), want the recorded cursor", got, ok)
		}
		if _, ok := stackPinnedCursor(manifest(&stackRepo{UpstreamCommit: other}, "commit "+sha), "lib"); ok {
			t.Fatal("reused a cursor after the commit pin changed")
		}
	})
}
