package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStackSeed(t *testing.T) {
	root := rmStackspace(t, rmManifest)
	// Root files the stackspace owns: kept out of every prefix on purpose.
	for _, f := range []string{"Directory.Build.rsp", "packaging/pack.sh", "Release Notes.md"} {
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

	for _, want := range []string{"rig.stack.jsonc", "Directory.Build.rsp", "packaging/pack.sh", "Directory.Build.targets", "Release Notes.md"} {
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

	t.Run("refuses a dirty stackspace", func(t *testing.T) {
		// The manifest is read from the working tree and the root files
		// from HEAD; a seed taken while they disagree is no revision at all.
		if err := os.WriteFile(filepath.Join(root, "Release Notes.md"), []byte("# edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer mustGitStack(t, root, "checkout", "--", "Release Notes.md")
		other := filepath.Join(t.TempDir(), "seed")
		cmd := newStackSeedCmd()
		cmd.SetContext(context.Background())
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs([]string{other})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "uncommitted") {
			t.Fatalf("dirty stackspace: err = %v, want a refusal", err)
		}
	})

	t.Run("refuses a member with commits that have not left", func(t *testing.T) {
		// A seed carries no member: a rebuilt one holds its cursor or its
		// proposed branch, and a commit that reached neither is in no seed.
		if err := os.WriteFile(filepath.Join(root, "pty-core", "src", "lib.cs"), []byte("// unsent\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, root, "add", "-A")
		mustGitStack(t, root, "commit", "-qm", "local change to pty-core")
		defer func() {
			mustGitStack(t, root, "reset", "-q", "--hard", "HEAD~1")
		}()
		other := filepath.Join(t.TempDir(), "seed")
		run := func(args ...string) error {
			cmd := newStackSeedCmd()
			cmd.SetContext(context.Background())
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(args)
			return cmd.Execute()
		}
		if err := run(other); err == nil || !strings.Contains(err.Error(), "pty-core has commits that have not left") {
			t.Fatalf("unsent commits: err = %v, want a refusal naming the member", err)
		}
		if _, err := os.Stat(other); !os.IsNotExist(err) {
			t.Error("a refused seed still wrote the destination")
		}
		if err := run(other, "--force"); err != nil {
			t.Fatalf("--force: %v", err)
		}
	})

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
	resolve := func(branch string) (string, bool, error) { sha, ok := exists[branch]; return sha, ok, nil }

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
		// The record is the branch as pushed; a prefix changed since the
		// proposal does not send the lookup to a branch that never existed.
		feature := "feature/"
		moved := &stackManifest{
			BranchPrefix: &feature,
			Repos:        map[string]*stackRepo{"lib": {Upstream: "h/acme/lib", Fork: "h/you/lib"}},
			LastPropose:  map[string]string{"lib": "stack/read-timeout"},
		}
		ref, err = stackForkRefFor(moved, "lib", true, resolve)
		if err != nil || ref == nil || ref.Branch != "stack/read-timeout" || ref.Commit != "aaa" {
			t.Fatalf("prefix changed after the proposal: %+v, %v", ref, err)
		}
		// Proposing again to the offered-back branch goes to that branch,
		// not to one with the new prefix stacked on; a fresh name takes the
		// new prefix.
		if got := moved.proposeBranch("lib", "stack/read-timeout"); got != "stack/read-timeout" {
			t.Errorf("reused record: branch = %q", got)
		}
		if got := moved.proposeBranch("lib", "other"); got != "feature/other" {
			t.Errorf("fresh name: branch = %q", got)
		}
		// An older manifest recorded the bare name; that one still takes
		// the prefix, since the branch it names was pushed with one.
		if got := m.proposeBranch("lib", "read-timeout"); got != "stack/read-timeout" {
			t.Errorf("bare record: branch = %q", got)
		}
		gone := func(string) (string, bool, error) { return "", false, nil }
		if ref, err := stackForkRefFor(m, "lib", true, gone); err != nil || ref != nil {
			t.Fatalf("merged-and-deleted branch: %+v, %v — want upstream at the cursor", ref, err)
		}
	})
	t.Run("a trackBranch that is not on the fork is an error, not a fallback", func(t *testing.T) {
		gone := func(string) (string, bool, error) { return "", false, nil }
		if _, err := stackForkRefFor(m, "tracked", false, gone); err == nil || !strings.Contains(err.Error(), `trackBranch "wip"`) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a fork that cannot be asked is an error, never a silent fallback", func(t *testing.T) {
		down := func(string) (string, bool, error) { return "", false, errors.New("403") }
		if _, err := stackForkRefFor(m, "lib", true, down); err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("rebuild with the fork down: err = %v", err)
		}
		if _, err := stackForkRefFor(m, "tracked", false, down); err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("trackBranch with the fork down: err = %v", err)
		}
		// A first import with nothing to look up never asks.
		if ref, err := stackForkRefFor(m, "lib", false, down); err != nil || ref != nil {
			t.Fatalf("first import asked the fork: %v, %v", ref, err)
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

// A link's target is judged through the links already written. Once `a -> .`
// exists, `a/../outside` is dest's sibling, not a child, though a lexical
// clean of the path would say otherwise.
func TestUntarRefusesALinkThatEscapesThroughAnotherLink(t *testing.T) {
	entries := func(links ...[2]string) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, l := range links {
			if err := tw.WriteHeader(&tar.Header{Name: l[0], Typeflag: tar.TypeSymlink, Linkname: l[1], Mode: 0o777}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	dest := filepath.Join(t.TempDir(), "seed")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(dest, "probe")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	err := untar(bytes.NewReader(entries([2]string{"a", "."}, [2]string{"b", "a/../outside"})), dest)
	if err == nil || !strings.Contains(err.Error(), "outside the seed") {
		t.Fatalf("err = %v, want the escaping link refused", err)
	}
	if _, serr := os.Lstat(filepath.Join(dest, "b")); !os.IsNotExist(serr) {
		t.Error("the escaping link was written")
	}
	// A link that stays inside, through another link, is fine.
	if err := untar(bytes.NewReader(entries([2]string{"c", "a/README.md"})), dest); err != nil {
		t.Fatalf("in-tree link through a link: %v", err)
	}
}

// The documented destination is relative (`../my-stack-seed`); containment is
// judged from the volume root, so the destination has to be made absolute
// before anything is compared to it.
func TestUntarAcceptsARelativeDestination(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "README.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("# ")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("seed", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := untar(bytes.NewReader(buf.Bytes()), filepath.Join("..", filepath.Base(mustAbs(t, ".")), "seed")); err != nil {
		t.Fatalf("relative dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join("seed", "README.md")); err != nil {
		t.Error("nothing written under the relative destination")
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
