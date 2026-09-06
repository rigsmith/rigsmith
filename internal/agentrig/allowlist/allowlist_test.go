package allowlist_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/allowlist"
)

func TestExplicitRulesAndHardPruning(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"selected/z", "selected/a", "selected/private/key", "selected/private/public", "selected/cache/deep/keep", "elsewhere/file"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rules := allowlist.List{Rules: []allowlist.Rule{
		{Pattern: "selected", Action: allowlist.Include},
		{Pattern: "selected/private", Action: allowlist.Exclude},
		{Pattern: "selected/private/public", Action: allowlist.Include},
		{Pattern: "**/cache", Action: allowlist.Exclude},
		{Pattern: "selected/cache/deep/keep", Action: allowlist.Include},
	}}
	paths, links, err := allowlist.Walk(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"selected/a", "selected/private/public", "selected/z"}
	if !reflect.DeepEqual(paths, want) || len(links) != 0 {
		t.Fatalf("walk = %v, %v", paths, links)
	}
	if rules.Match("elsewhere/file") || rules.Descend("selected/cache") {
		t.Fatal("default deny or hard prune lost")
	}
}

func TestDirectoryLinksStayInsideSelectedRoot(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	for _, rel := range []string{"selected/data", "excluded"} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "selected/data/file"), []byte("once"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"a": filepath.Join(root, "selected/data"), "b": external, "c": filepath.Join(root, "excluded")} {
		if err := os.Symlink(target, filepath.Join(root, "selected", name)); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlinks unavailable: %v", err)
			}
			t.Fatal(err)
		}
	}
	paths, links, err := allowlist.Walk(root, allowlist.List{Rules: []allowlist.Rule{{Pattern: "selected", Action: allowlist.Include}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"selected/data/file"}) || !reflect.DeepEqual(links, []allowlist.Link{{Rel: "selected/a", Target: "selected/data"}}) {
		t.Fatalf("walk = %v, %v", paths, links)
	}
}

func TestDirectoryLinksDistinguishDotNamesFromParentTraversal(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	for _, dir := range []string{filepath.Join(root, "selected"), filepath.Join(root, "..named", "nested"), filepath.Join(parent, "outside")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{
		"inside":  filepath.Join(root, "..named"),
		"nested":  filepath.Join(root, "..named", "nested"),
		"parent":  parent,
		"outside": filepath.Join(parent, "outside"),
		"root":    root,
	} {
		if err := os.Symlink(target, filepath.Join(root, "selected", name)); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlinks unavailable: %v", err)
			}
			t.Fatal(err)
		}
	}
	rules := allowlist.List{Rules: []allowlist.Rule{
		{Pattern: "selected", Action: allowlist.Include},
		{Pattern: "..named", Action: allowlist.Include},
	}}
	paths, links, err := allowlist.Walk(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	want := []allowlist.Link{
		{Rel: "selected/inside", Target: "..named"},
		{Rel: "selected/nested", Target: "..named/nested"},
	}
	if len(paths) != 0 || !reflect.DeepEqual(links, want) {
		t.Fatalf("walk = %v, %v; want no files and links %v", paths, links, want)
	}
}
