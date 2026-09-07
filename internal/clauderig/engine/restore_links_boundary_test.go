package engine

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

type swappingRestoreTarget struct {
	restoreLinkFS
	swap       func() error
	cleanupErr error
}

func (r swappingRestoreTarget) Symlink(target, name string) error {
	if err := r.Root.Symlink(target, name); err != nil {
		return err
	}
	return r.swap()
}

func (r swappingRestoreTarget) Remove(name string) error {
	if r.cleanupErr != nil {
		return r.cleanupErr
	}
	return r.Root.Remove(name)
}

func TestRestoreLinkRechecksTargetAfterCreation(t *testing.T) {
	requireRestoreSymlink(t)
	for _, failCleanup := range []bool{false, true} {
		dir, outside := t.TempDir(), t.TempDir()
		write(t, dir, "data/keep.txt", "inside")
		if err := os.Symlink("data", filepath.Join(dir, "alias")); err != nil {
			t.Fatal(err)
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { root.Close() })
		// Model the initial successful target check in restoreLinks.
		if _, err := root.Stat("alias"); err != nil {
			t.Fatal(err)
		}
		fs := swappingRestoreTarget{restoreLinkFS: restoreLinkFS{root}, swap: func() error {
			if err := root.Remove("alias"); err != nil {
				return err
			}
			return root.Symlink(outside, "alias")
		}}
		if failCleanup {
			fs.cleanupErr = errors.New("fixture cleanup denied")
		}
		created, err := createRestoreLink(fs, "alias", "memory")
		if created || !errors.Is(err, fs.cleanupErr) {
			t.Fatalf("unsafe link counted or cleanup failure hidden: %v %v", created, err)
		}
		if !failCleanup {
			if _, err := root.Lstat("memory"); !os.IsNotExist(err) {
				t.Fatal("unverified link remained", err)
			}
		}
	}
}

func TestValidRestoreLinkPath(t *testing.T) {
	for _, p := range []string{"", ".", "..", "../outside", "a/../b", "/absolute", "//server/share", `C:\outside`, "C:outside", `a\b`, "a//b", "a/./b", "a/", "a:stream", "a\x00b", string([]byte{0xff})} {
		if validRestoreLinkPath(p) {
			t.Errorf("accepted nonportable endpoint %q", p)
		}
	}
	for _, p := range []string{"projects/main/memory", "..memory/notes", "shared notes", "projects/é/memory"} {
		if !validRestoreLinkPath(p) {
			t.Errorf("rejected local endpoint %q", p)
		}
	}
}

func requireRestoreSymlink(t *testing.T) {
	t.Helper()
	probe := t.TempDir()
	if err := os.Symlink(probe, filepath.Join(probe, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
}

func TestRestoreLinksRejectsEscapingEndpoints(t *testing.T) {
	requireRestoreSymlink(t)
	for _, tc := range []struct {
		name, link, dest string
		slugs            map[string]string
	}{
		{"link traversal", "../outside/new/memory", "data", nil},
		{"target traversal", "new/memory", "../outside", nil},
		{"raw link before rewrite", "projects/../memory", "data", map[string]string{"..": "safe"}},
		{"raw target before rewrite", "new/memory", "projects/../data", map[string]string{"..": "safe"}},
		{"rewritten link", "projects/p/memory", "data", map[string]string{"p": "../../outside/new"}},
		{"rewritten target", "new/memory", "projects/p", map[string]string{"p": "../../outside"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root, outside := filepath.Join(parent, "root"), filepath.Join(parent, "outside")
			write(t, root, "data/keep.txt", "inside")
			write(t, root, "projects/safe/data/keep.txt", "inside")
			write(t, outside, "keep.txt", "outside")
			if got, err := restoreLinks(root, map[string]string{tc.link: tc.dest}, tc.slugs); err != nil || got != 0 {
				t.Errorf("restored escaping link: count=%d err=%v", got, err)
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
				t.Fatalf("outside directory changed: %v %v", entries, err)
			}
			if _, err := os.Lstat(filepath.Join(root, "new")); !os.IsNotExist(err) {
				t.Error("created a parent for a rejected link", err)
			}
		})
	}
}

func TestRestoreLinksRejectsExternalTargetSymlinks(t *testing.T) {
	requireRestoreSymlink(t)
	for _, suffix := range []string{"", "/nested"} {
		t.Run(suffix, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			write(t, outside, "nested/keep.txt", "outside")
			if err := os.Symlink(outside, filepath.Join(root, "alias")); err != nil {
				t.Fatal(err)
			}
			if got, err := restoreLinks(root, map[string]string{"new/memory": "alias" + suffix}, nil); err != nil || got != 0 {
				t.Error("restored link to external directory", got, err)
			}
			if _, err := os.Lstat(filepath.Join(root, "new")); !os.IsNotExist(err) {
				t.Error("created a parent for an external target", err)
			}
		})
	}
}

func TestRestoreLinksAcceptsChosenRootAndInternalTarget(t *testing.T) {
	requireRestoreSymlink(t)
	parent := t.TempDir()
	root, chosen := filepath.Join(parent, "actual"), filepath.Join(parent, "chosen")
	write(t, root, "data/keep.txt", "inside")
	if err := os.Symlink(root, chosen); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("data", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if got, err := restoreLinks(chosen, map[string]string{"projects/p/memory": "alias"}, nil); err != nil || got != 1 {
		t.Fatal("valid internal link rejected", got, err)
	}
	if got := read(t, filepath.Join(chosen, "projects/p/memory/keep.txt")); got != "inside" {
		t.Fatal("restored link does not reach its target", got)
	}
}

func TestRestoreSkipsUnsafeSavedLinksAndRestoresSafeOnes(t *testing.T) {
	requireRestoreSymlink(t)
	staging, parent := t.TempDir(), t.TempDir()
	root, outside := filepath.Join(parent, "root"), filepath.Join(parent, "outside")
	write(t, staging, "cli/projects/main/s.jsonl", "transcript\n")
	write(t, staging, "cli/projects/main/memory/MEMORY.md", "facts")
	write(t, outside, "keep.txt", "outside")
	m := &manifest.Manifest{
		Schema: 1, SourceOS: pathmap.OSMacOS,
		Projects: map[string]manifest.Project{"main": {Cwd: "/main"}},
		Links: map[string]string{
			"projects/worktree/memory": "projects/main/memory",
			"../outside/new/memory":    "projects/main/memory",
			"projects/unsafe/memory":   "../outside",
		},
	}
	if err := m.Save(staging); err != nil {
		t.Fatal(err)
	}
	loaded, err := manifest.Load(staging)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(root), Manifest: loaded,
		Machine: config.Machine{Name: "fixture", OS: pathmap.OSMacOS, Home: "/Users/fixture"}, TargetOverride: override("cli", root)})
	if err != nil || len(rep.Roots) != 1 || rep.Roots[0].Links != 1 {
		t.Fatalf("restore did not skip only unsafe links: %+v %v", rep, err)
	}
	if got := read(t, filepath.Join(root, "projects/worktree/memory/MEMORY.md")); got != "facts" {
		t.Fatal(got)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
		t.Fatalf("restore escaped its root: %v %v", entries, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "projects/unsafe")); !os.IsNotExist(err) {
		t.Fatal("created an unsafe link parent", err)
	}
}

// A writer can claim the public name before installation, or replace our link
// after it. Neither case permits cleanup to remove the writer's entry.
type concurrentRestoreWriter struct {
	restoreLinkFS
	before, after func()
}

func (r concurrentRestoreWriter) Install(temp, name string) error {
	if r.before != nil {
		r.before()
	}
	err := r.restoreLinkFS.Install(temp, name)
	if r.after != nil {
		r.after()
	}
	return err
}

func TestRestoreLinkPreservesConcurrentDestination(t *testing.T) {
	requireRestoreSymlink(t)
	for _, phase := range []string{"before install", "after install"} {
		for _, kind := range []string{"file", "directory", "symlink"} {
			t.Run(phase+"/"+kind, func(t *testing.T) {
				dir := t.TempDir()
				write(t, dir, "data/keep.txt", "inside")
				root, err := os.OpenRoot(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()
				writer := concurrentRestoreWriter{restoreLinkFS: restoreLinkFS{root}}
				var owned os.FileInfo
				replace := func() {
					if phase == "after install" {
						if err := root.Remove("memory"); err != nil {
							t.Fatal(err)
						}
					}
					switch kind {
					case "file":
						err = root.WriteFile("memory", []byte("writer"), 0o600)
					case "directory":
						err = root.Mkdir("memory", 0o700)
					case "symlink":
						err = root.Symlink("data", "memory")
					}
					if err != nil {
						t.Fatal(err)
					}
					owned, err = root.Lstat("memory")
					if err != nil {
						t.Fatal(err)
					}
				}
				if phase == "before install" {
					writer.before = replace
				} else {
					writer.after = replace
				}
				created, err := createRestoreLink(writer, "data", "memory")
				if err != nil || created != (phase == "after install") {
					t.Fatalf("create result: %v %v", created, err)
				}
				current, err := root.Lstat("memory")
				if err != nil || !os.SameFile(owned, current) {
					t.Fatalf("concurrent destination replaced or removed: %v", err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 2 {
					t.Fatalf("temporary link left behind: %v %v", entries, err)
				}
			})
		}
	}
}

func TestRestoreLinksReportsRootOpenFailure(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "file", "not a directory")
	for _, target := range []string{filepath.Join(dir, "missing"), filepath.Join(dir, "file")} {
		if n, err := restoreLinks(target, map[string]string{"memory": "data"}, nil); n != 0 || err == nil {
			t.Fatalf("root failure hidden: count=%d err=%v", n, err)
		}
		if n, err := restoreLinks(target, nil, nil); n != 0 || err != nil {
			t.Fatalf("empty link map should not need a root: count=%d err=%v", n, err)
		}
	}
}

func TestRestoreLinkFailureRetainsPartialReport(t *testing.T) {
	staging, dir := t.TempDir(), t.TempDir()
	first, target := filepath.Join(dir, "first"), filepath.Join(dir, "file")
	write(t, staging, "other/notes.md", "restored")
	if err := os.MkdirAll(filepath.Join(staging, "cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "file", "not a directory")
	cfg := targetRootConfig(target)
	cfg.Roots = append([]config.Root{{ID: "other", Enabled: true, Location: pathmap.Cascade{Portable: first}}}, cfg.Roots...)
	rep, err := Restore(RestoreOptions{StagingDir: staging, Config: cfg, Manifest: &manifest.Manifest{Links: map[string]string{"memory": "data"}}, TargetOverride: map[string]string{"other": first, "cli": target}})
	if err == nil || rep == nil || len(rep.Roots) != 2 || rep.Roots[0].Files != 1 || rep.Roots[1].ID != "cli" {
		t.Fatalf("partial report lost: %+v %v", rep, err)
	}
	if got := read(t, filepath.Join(first, "notes.md")); got != "restored" {
		t.Fatal(got)
	}
}

func TestRestoreLinkPathUsesDestinationPlatform(t *testing.T) {
	// A Unix backup may legitimately contain CON. Restoring it on Windows
	// skips that unrepresentable name; the saved manifest remains unchanged.
	if got := validRestoreLinkPath("projects/CON/memory"); got != (runtime.GOOS != "windows") {
		t.Fatalf("reserved name accepted=%v on %s", got, runtime.GOOS)
	}
}
