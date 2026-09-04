package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackSeed(t *testing.T) {
	root := rmStackspace(t, rmManifest)
	// Root files the stackspace owns: kept out of every prefix on purpose.
	for _, f := range []string{"Directory.Build.rsp", "packaging/pack.sh"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("# "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGitStack(t, root, "add", "-A")
	mustGitStack(t, root, "commit", "-qm", "root files")

	dest := filepath.Join(t.TempDir(), "seed")
	var buf bytes.Buffer
	cmd := newStackSeedCmd()
	cmd.SetContext(context.Background())
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{dest})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}

	for _, want := range []string{"rig.stack.jsonc", "Directory.Build.rsp", "packaging/pack.sh", "Directory.Build.targets"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
			t.Errorf("seed is missing %s", want)
		}
	}
	for _, member := range []string{"pty-core", "term-core"} {
		if _, err := os.Stat(filepath.Join(dest, member)); !os.IsNotExist(err) {
			t.Errorf("seed carries the member %s", member)
		}
	}
	if st := mustGitStack(t, dest, "status", "--porcelain"); strings.TrimSpace(st) != "" {
		t.Errorf("seed not committed:\n%s", st)
	}
	if n := strings.TrimSpace(mustGitStack(t, dest, "rev-list", "--count", "HEAD")); n != "1" {
		t.Errorf("seed has %s commits, want one", n)
	}
	// The manifest travels intact, cursors included — that is what init rebuilds from.
	m, _, err := loadStackManifest(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Repos) != 2 {
		t.Errorf("seed manifest names %v", m.names())
	}

	t.Run("refuses a non-empty destination", func(t *testing.T) {
		cmd := newStackSeedCmd()
		cmd.SetContext(context.Background())
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{dest})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not empty") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestStackForkRefFor(t *testing.T) {
	m := &stackManifest{
		Repos: map[string]*stackRepo{
			"lib":     {Upstream: "h/acme/lib", Fork: "h/you/lib"},
			"tracked": {Upstream: "h/acme/t", Fork: "h/you/t", TrackBranch: "wip"},
		},
		LastPropose: map[string]string{"lib": "read-timeout"},
	}
	exists := map[string]string{"stack/read-timeout": "aaa", "wip": "bbb"}
	resolve := func(branch string) (string, bool) { sha, ok := exists[branch]; return sha, ok }

	t.Run("a first import follows upstream unless trackBranch says otherwise", func(t *testing.T) {
		if ref, err := stackForkRefFor(m, "lib", false, resolve); err != nil || ref != nil {
			t.Fatalf("lib: %v, %v", ref, err)
		}
		ref, err := stackForkRefFor(m, "tracked", false, resolve)
		if err != nil || ref == nil || ref.Branch != "wip" || ref.Commit != "bbb" {
			t.Fatalf("tracked: %+v, %v", ref, err)
		}
	})
	t.Run("a rebuild uses the branch last proposed to, while it exists", func(t *testing.T) {
		ref, err := stackForkRefFor(m, "lib", true, resolve)
		if err != nil || ref == nil || ref.Branch != "stack/read-timeout" || ref.Commit != "aaa" {
			t.Fatalf("%+v, %v", ref, err)
		}
		gone := func(string) (string, bool) { return "", false }
		if ref, err := stackForkRefFor(m, "lib", true, gone); err != nil || ref != nil {
			t.Fatalf("merged-and-deleted branch: %+v, %v — want upstream at the cursor", ref, err)
		}
	})
	t.Run("a trackBranch that is not on the fork is an error, not a fallback", func(t *testing.T) {
		gone := func(string) (string, bool) { return "", false }
		if _, err := stackForkRefFor(m, "tracked", false, gone); err == nil || !strings.Contains(err.Error(), `trackBranch "wip"`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("trackBranch cannot ride on a pin", func(t *testing.T) {
		root := t.TempDir()
		writeStackManifest(t, root, `{"repos": {"t": {"upstream": "h/a/t", "fork": "h/y/t", "trackBranch": "wip", "upstreamTag": "v1"}}}`)
		if _, _, err := loadStackManifest(root); err == nil || !strings.Contains(err.Error(), "trackBranch") {
			t.Fatalf("err = %v", err)
		}
	})
}
