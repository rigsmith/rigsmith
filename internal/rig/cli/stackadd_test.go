package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runAdd runs `stack add` in a fresh stackspace whose manifest starts empty —
// the state `stack init` scaffolds, and the one add exists to fill.
func runAdd(t *testing.T, args ...string) (*stackManifest, error) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	writeStackManifest(t, root, "{\n  \"repos\": {}\n}\n")
	for _, a := range [][]string{{"init", "-q", "-b", "main", root}, {"-C", root, "add", "-A"}} {
		c := exec.CommandContext(ctx, "git", a...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", a, err, out)
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(dir) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	cmd := newStackAddCmd()
	cmd.SetContext(ctx)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return nil, err
	}
	m, _, err := loadStackManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	return m, nil
}

func TestStackAdd(t *testing.T) {
	t.Run("a pasted URL becomes a spec", func(t *testing.T) {
		m, err := runAdd(t, "https://github.com/acme/pty-core.git",
			"--fork", "git@github.com:you/pty-core.git", "--no-import")
		if err != nil {
			t.Fatal(err)
		}
		r := m.Repos["pty-core"]
		if r == nil {
			t.Fatalf("nothing added: %v", m.names())
		}
		if r.Upstream != "github.com/acme/pty-core" || r.Fork != "github.com/you/pty-core" {
			t.Fatalf("got %+v, want both reduced to host/owner/name", r)
		}
	})

	t.Run("the directory defaults to the repo name", func(t *testing.T) {
		m, err := runAdd(t, "github.com/acme/pty-core", "--fork", "github.com/you/pty-core", "--no-import")
		if err != nil {
			t.Fatal(err)
		}
		if m.Repos["pty-core"] == nil {
			t.Fatalf("want a pty-core entry, got %v", m.names())
		}
	})

	t.Run("--as overrides it", func(t *testing.T) {
		m, err := runAdd(t, "github.com/acme/pty-core", "--fork", "github.com/you/pty-core", "--as", "pty", "--no-import")
		if err != nil {
			t.Fatal(err)
		}
		if m.Repos["pty"] == nil {
			t.Fatalf("want a pty entry, got %v", m.names())
		}
	})

	t.Run("a repo of your own needs no fork", func(t *testing.T) {
		// There is no separate place to propose changes to: the place work goes
		// is the place it came from.
		m, err := runAdd(t, "github.com/you/term-app", "--owned", "--no-import")
		if err != nil {
			t.Fatal(err)
		}
		r := m.Repos["term-app"]
		if r == nil || !r.Owned || r.Fork != r.Upstream {
			t.Fatalf("got %+v, want owned with the fork defaulted to upstream", r)
		}
	})

	t.Run("without --owned a fork is required, non-interactively", func(t *testing.T) {
		_, err := runAdd(t, "github.com/acme/pty-core", "--no-import")
		if err == nil || !strings.Contains(err.Error(), "--owned") {
			t.Fatalf("got %v, want a refusal naming the way out", err)
		}
	})

	t.Run("a spec that is no repo is refused before it is written", func(t *testing.T) {
		_, err := runAdd(t, "https://github.com/acme/pty-core/tree/main", "--owned", "--no-import")
		if err == nil {
			t.Fatal("accepted a tree URL")
		}
	})
}
