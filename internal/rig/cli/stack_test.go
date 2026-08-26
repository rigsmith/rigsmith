package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
    "porta-pty": {
      "upstream": "github.com/tomlm/Porta.Pty",
      "fork":     "github.com/JohnCampionJr/Porta.Pty",
      "branch":   "main"
    },
    "xterm-net": {
      "upstream": "github.com/tomlm/XTerm.NET",
      "fork":     "github.com/JohnCampionJr/XTerm.NET"
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
		if got := m.branch("porta-pty"); got != "main" {
			t.Fatalf("branch = %q", got)
		}
		if got := m.branch("xterm-net"); got != "main" {
			t.Fatalf("default branch = %q, want main", got)
		}
		if names := m.names(); strings.Join(names, ",") != "porta-pty,xterm-net" {
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

	t.Run("inline ws key in .rig.json is a source too", func(t *testing.T) {
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
	if err := stackSetCursor(src, m, "porta-pty", "abc123def456"); err != nil {
		t.Fatal(err)
	}
	m2, src2, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.cursor("porta-pty"); got != "abc123def456" {
		t.Fatalf("lastSync = %q", got)
	}
	// A second cursor lands beside the first, not over it.
	if err := stackSetCursor(src2, m2, "xterm-net", "fedcba"); err != nil {
		t.Fatal(err)
	}
	m3, _, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if m3.cursor("porta-pty") != "abc123def456" || m3.cursor("xterm-net") != "fedcba" {
		t.Fatalf("cursors = %v", m3.LastSync)
	}
	data, _ := os.ReadFile(filepath.Join(root, "rig.stack.jsonc"))
	if !strings.Contains(string(data), "comments must survive") {
		t.Fatal("cursor write dropped the manifest's comments")
	}
}

func TestJoshURL(t *testing.T) {
	p := &joshProxy{port: 4242}
	got := p.url("tomlm/Porta.Pty", "abc123", stackPrefixFilter("porta-pty"))
	want := "http://127.0.0.1:4242/tomlm/Porta.Pty.git@abc123%3Aprefix%3Dporta-pty.git"
	if got != want {
		t.Fatalf("url:\n got %s\nwant %s", got, want)
	}
	if got := p.url("a/b", "", ":/x"); got != "http://127.0.0.1:4242/a/b.git%3A%2Fx.git" {
		t.Fatalf("no-commit url = %s", got)
	}
}

func TestWsSplitHost(t *testing.T) {
	host, path := stackSplitHost("github.com/tomlm/Porta.Pty")
	if host != "github.com" || path != "tomlm/Porta.Pty" {
		t.Fatalf("got %q %q", host, path)
	}
}

// TestStartJoshProxy_FakeBinary exercises the spawn/poll/stop lifecycle with a
// stand-in that listens like the real proxy, so the lifecycle is covered
// without a Rust toolchain anywhere near CI.
func TestStartJoshProxy_FakeBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake proxy is a shell script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "josh-proxy")
	// nc -l occupies the port the harness passes; enough for the TCP poll.
	script := `#!/bin/sh
for a in "$@"; do case "$a" in --port=*) port="${a#--port=}";; esac; done
exec nc -l 127.0.0.1 "$port"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc not available")
	}
	p, err := startJoshProxy(context.Background(), fake, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	p.stop() // must terminate promptly and not leak the process
}

func TestStartJoshProxy_NeverReady(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake proxy is a shell script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "josh-proxy")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := startJoshProxy(context.Background(), fake, "github.com"); err == nil {
		t.Fatal("expected readiness failure for a proxy that exits immediately")
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

	t.Run("no menu group outside a workspace", func(t *testing.T) {
		inWorkspace(t, "")
		if items := stackMenuItems(); items != nil {
			t.Fatalf("expected no items, got %d", len(items))
		}
	})

	t.Run("menu offers the verbs a pick can supply arguments for", func(t *testing.T) {
		inWorkspace(t, stackTestManifest)
		var labels []string
		for _, it := range stackMenuItems() {
			labels = append(labels, it.label)
		}
		want := "status,pull,doctor"
		if got := strings.Join(labels, ","); got != want {
			t.Fatalf("menu = %q, want %q", got, want)
		}
	})

	t.Run("completion offers the workspace's repos", func(t *testing.T) {
		inWorkspace(t, stackTestManifest)
		got, _ := stackRepoCompletion(nil, nil, "")
		if strings.Join(got, ",") != "porta-pty,xterm-net" {
			t.Fatalf("completion = %v", got)
		}
	})

	t.Run("completion stops after the repo argument", func(t *testing.T) {
		inWorkspace(t, stackTestManifest)
		if got, _ := stackRepoCompletion(nil, []string{"porta-pty"}, ""); got != nil {
			t.Fatalf("expected no completions for the second argument, got %v", got)
		}
	})
}
