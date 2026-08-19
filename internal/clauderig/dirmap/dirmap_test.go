package dirmap

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "dir-map.json"))
}

func setAccount(t *testing.T, s *Store, dir, id string) {
	t.Helper()
	if _, err := s.Set(dir, func(e *Entry) { e.Account = id }); err != nil {
		t.Fatal(err)
	}
}

func TestLookupPrefersTheNearestMappedAncestor(t *testing.T) {
	s := newTestStore(t)
	root := t.TempDir()
	inner := filepath.Join(root, "client", "app")
	if err := os.MkdirAll(filepath.Join(inner, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	setAccount(t, s, root, "personal")
	setAccount(t, s, inner, "work")

	// Deep inside the inner mapping: the nearer one wins, not the first found.
	got, err := s.Lookup(filepath.Join(inner, "src"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Account != "work" {
		t.Fatalf("Account = %q, want work — the nearest mapped ancestor must win", got.Account)
	}
	// Outside the inner mapping but inside the outer one.
	got, err = s.Lookup(filepath.Join(root, "elsewhere"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Account != "personal" {
		t.Fatalf("Account = %q, want personal", got.Account)
	}
}

// /a/foo must not govern /a/foobar. Without a separator-aware prefix check it
// would, and the wrong account would launch in a neighbouring repo.
func TestLookupDoesNotMatchASiblingWithASharedPrefix(t *testing.T) {
	s := newTestStore(t)
	root := t.TempDir()
	foo := filepath.Join(root, "foo")
	foobar := filepath.Join(root, "foobar")
	for _, d := range []string{foo, foobar} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setAccount(t, s, foo, "work")
	if _, err := s.Lookup(foobar); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("Lookup(foobar) = %v, want ErrNoMapping — /a/foo must not cover /a/foobar", err)
	}
}

func TestLookupReportsNoMappingOutsideEverything(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Lookup(t.TempDir()); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("want ErrNoMapping, got %v", err)
	}
}

func TestSetIsIdempotentForOneDirectory(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	setAccount(t, s, dir, "work")
	setAccount(t, s, dir, "personal")
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d mappings, want 1 — re-mapping a directory must replace, not append", len(all))
	}
	if all[0].Account != "personal" {
		t.Fatalf("Account = %q, want personal", all[0].Account)
	}
}

// The two bindings are independent: mapping a Desktop profile must not disturb
// the CLI account already bound to the same directory.
func TestAccountAndDesktopBindingsCoexist(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	setAccount(t, s, dir, "work-cli")
	if _, err := s.Set(dir, func(e *Entry) { e.Desktop = "work-app" }); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Account != "work-cli" || got.Desktop != "work-app" {
		t.Fatalf("entry = %+v, want both bindings kept", got)
	}
}

func TestClearingTheLastBindingRemovesTheEntry(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	setAccount(t, s, dir, "work")
	if _, err := s.Set(dir, func(e *Entry) { e.Account = "" }); err != nil {
		t.Fatal(err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("got %+v, want the empty entry dropped", all)
	}
	// An empty table leaves no file behind.
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Fatal("empty mapping file was left on disk")
	}
}

// A mapping that names a removed account would do nothing at exactly the moment
// it was most expected to work, so removal has to reach the table.
func TestPruneAccountDropsBindingsAndEmptyEntries(t *testing.T) {
	s := newTestStore(t)
	soloDir, sharedDir := t.TempDir(), t.TempDir()
	setAccount(t, s, soloDir, "gone")
	if _, err := s.Set(sharedDir, func(e *Entry) { e.Account = "gone"; e.Desktop = "kept" }); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneAccount("gone"); err != nil {
		t.Fatal(err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("got %+v, want only the entry that still binds a Desktop profile", all)
	}
	if all[0].Account != "" || all[0].Desktop != "kept" {
		t.Fatalf("entry = %+v, want the account binding cleared and the Desktop one kept", all[0])
	}
}

func TestPruneDesktopDropsBindings(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	if _, err := s.Set(dir, func(e *Entry) { e.Desktop = "gone" }); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneDesktop("gone"); err != nil {
		t.Fatal(err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("got %+v, want the entry dropped", all)
	}
}

func TestRemoveTargetsOneDirectoryNotItsAncestors(t *testing.T) {
	s := newTestStore(t)
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	setAccount(t, s, root, "personal")
	setAccount(t, s, inner, "work")
	if err := s.Remove(inner); err != nil {
		t.Fatal(err)
	}
	got, err := s.Lookup(inner)
	if err != nil {
		t.Fatal(err)
	}
	if got.Account != "personal" {
		t.Fatalf("Account = %q, want personal — removing the inner mapping should fall back to the outer", got.Account)
	}
	if err := s.Remove(inner); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("removing twice = %v, want ErrNoMapping", err)
	}
}

func TestFileIsNotWorldReadable(t *testing.T) {
	// Windows has no Unix permission bits — Go's Chmod there only toggles the
	// read-only flag, so a 0600 file reports 0666 and this would assert the
	// platform rather than the code. Containment there rests on the ACL the
	// file inherits from %USERPROFILE%.
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permission bits on Windows")
	}
	s := newTestStore(t)
	setAccount(t, s, t.TempDir(), "work")
	fi, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode = %v, want 0600", perm)
	}
}
