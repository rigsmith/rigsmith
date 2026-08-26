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

	t.Run("scheme or .git in a spec is rejected", func(t *testing.T) {
		root := t.TempDir()
		writeStackManifest(t, root, `{"repos":{"x":{"upstream":"https://github.com/a/b","fork":"github.com/c/d"}}}`)
		if _, _, err := loadStackManifest(root); err == nil {
			t.Fatal("expected validation error for scheme-carrying spec")
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

		conflicted, err := repo.FetchMergeUnrelated(ctx, other, otherBranch, "stack: import other", nil)
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
