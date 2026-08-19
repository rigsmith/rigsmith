package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "desktop"))
}

func TestCreateMakesAnEmptyProfileTheAppCanOwn(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Create("work", "john@example.com", "john-example-com")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p.DataDir())
	if err != nil || !fi.IsDir() {
		t.Fatalf("data dir not created: %v", err)
	}
	// The session of a logged-in account lives here in full. Windows has no Unix
	// permission bits (Go's Chmod there only toggles read-only), so asserting the
	// mode would only be asserting the platform — the containment guarantee on
	// Windows comes from the parent directory's ACL instead.
	if runtime.GOOS != "windows" {
		if perm := fi.Mode().Perm(); perm != 0o700 {
			t.Fatalf("data dir mode = %v, want 0700", perm)
		}
	}
	entries, err := os.ReadDir(p.DataDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("data dir is not empty (%d entries) — Claude Desktop must populate it itself", len(entries))
	}
}

func TestCreateRefusesADuplicate(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("work", "", ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create("work", "", "")
	if !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
}

// The name becomes a directory name under the store root, so a name that walks
// out of it would let `add` create — and `rm` delete — arbitrary directories.
func TestValidNameRejectsPathEscapes(t *testing.T) {
	for _, bad := range []string{"..", ".", "../evil", "a/b", `a\b`, "", strings.Repeat("x", 65), ".hidden", "-lead"} {
		if err := ValidName(bad); err == nil {
			t.Errorf("ValidName(%q) = nil, want an error", bad)
		}
	}
	for _, good := range []string{"work", "personal", "client-x", "acct.2", "a_b", "x"} {
		if err := ValidName(good); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", good, err)
		}
	}
}

func TestCreateRejectsAnEscapingName(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("../escape", "", ""); err == nil {
		t.Fatal("created a profile whose name escapes the store root")
	}
}

func TestResolveFindsByNameOrEmail(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("work", "John@Example.com", ""); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"work", "John@Example.com", "john@example.com"} {
		p, err := s.Resolve(ref)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", ref, err)
		}
		if p.Name != "work" {
			t.Fatalf("Resolve(%q) = %q", ref, p.Name)
		}
	}
	if _, err := s.Resolve("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListSkipsDirectoriesThatAreNotProfiles(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("work", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "junk"), 0o700); err != nil {
		t.Fatal(err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "work" {
		t.Fatalf("List() = %+v, want just the work profile", all)
	}
}

func TestRemoveDeletesEverything(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Create("work", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.DataDir(), "Cookies"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("work"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Dir()); !os.IsNotExist(err) {
		t.Fatal("profile directory survived removal")
	}
	if err := s.Remove("work"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound removing twice, got %v", err)
	}
}

// Two profiles are siblings under one root, so one path is a prefix of nothing
// else only if the match includes the full flag token. This is the property that
// stops `quit work` from killing `work-2`.
func TestUserDataFlagIsAnExactToken(t *testing.T) {
	a := userDataFlag("/root/work/data")
	b := userDataFlag("/root/work-2/data")
	if a == b || strings.HasPrefix(b, a) {
		t.Fatalf("flag for a sibling profile (%q) collides with %q", b, a)
	}
}

// fakeApp stands in for Claude Desktop.
type fakeApp struct {
	mu       sync.Mutex
	launched []string
	running  map[string]bool
	quitAt   map[string]time.Duration
	scanErr  error // set to simulate a failed process scan
}

func newFakeApp() *fakeApp {
	return &fakeApp{running: map[string]bool{}, quitAt: map[string]time.Duration{}}
}

func (f *fakeApp) Installed() (string, bool) { return "/Applications/Claude.app", true }
func (f *fakeApp) Launch(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launched = append(f.launched, dir)
	f.running[dir] = true
	return nil
}
func (f *fakeApp) Running(dir string) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	if f.running[dir] {
		return []int{4242}, nil
	}
	return nil, nil
}
func (f *fakeApp) Focus(string) error { return nil }
func (f *fakeApp) Quit(dir string, grace time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, dir)
	f.quitAt[dir] = grace
	return nil
}

func TestIsRunningTracksTheProfileNotTheApp(t *testing.T) {
	app := newFakeApp()
	work, personal := "/p/work/data", "/p/personal/data"
	if err := app.Launch(work); err != nil {
		t.Fatal(err)
	}
	if running, err := IsRunning(app, work); err != nil || !running {
		t.Fatalf("launched profile reports closed (%v, %v)", running, err)
	}
	// The whole point of the model: one profile open says nothing about another.
	if running, err := IsRunning(app, personal); err != nil || running {
		t.Fatalf("a second profile reports open because the first is (%v, %v)", running, err)
	}
	if err := app.Quit(work, time.Second); err != nil {
		t.Fatal(err)
	}
	if running, err := IsRunning(app, work); err != nil || running {
		t.Fatalf("profile still reports open after quit (%v, %v)", running, err)
	}
}

// A failed scan must be reported, not reported as "closed": `rm` deletes the
// profile directory, and doing that under a live Electron leaves the app writing
// into unlinked files.
func TestIsRunningReportsAFailedScanRatherThanGuessing(t *testing.T) {
	app := newFakeApp()
	app.scanErr = errors.New("pgrep exploded")
	running, err := IsRunning(app, "/p/work/data")
	if err == nil {
		t.Fatal("a failed process scan was reported as an answer")
	}
	if running {
		t.Fatal("a failed scan should not claim the profile is open")
	}
}

func TestWaitGoneReturnsFalseWhenTheAppStaysUp(t *testing.T) {
	app := newFakeApp()
	_ = app.Launch("/p/work/data")
	if waitGone(app, "/p/work/data", time.Now().Add(300*time.Millisecond)) {
		t.Fatal("waitGone reported a still-running instance as gone")
	}
}

// Every command path takes a name from the command line, so the traversal guard
// has to sit on the lookup, not only on creation.
func TestLookupsRejectAnEscapingName(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("work", "", ""); err != nil {
		t.Fatal(err)
	}
	// A real profile.json exists one level up from the store root's child dir;
	// without the guard, "../work" would resolve to it.
	for _, bad := range []string{"../work", "..", "a/b"} {
		if _, err := s.Get(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", bad, err)
		}
		if err := s.Remove(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Remove(%q) = %v, want ErrNotFound", bad, err)
		}
	}
	// The legitimate profile still resolves.
	if _, err := s.Get("work"); err != nil {
		t.Fatalf("Get(work): %v", err)
	}
}

// The directory is the identity. If profile.json names something else — hand
// edited, or copied from another profile — then DataDir would point at the
// directory asked for while Touch and Remove acted on the name inside it.
func TestGetTreatsTheDirectoryNameAsAuthoritative(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("work", "", ""); err != nil {
		t.Fatal(err)
	}
	// Rewrite the metadata to claim a different profile's name.
	body := []byte(`{"name":"personal","email":"x@y.z","createdAt":"2026-01-01T00:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(s.Root, "work", "profile.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := s.Get("work")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "work" {
		t.Fatalf("Name = %q, want work — a mismatched metadata name must not win", p.Name)
	}
	if filepath.Base(p.Dir()) != "work" {
		t.Fatalf("Dir = %q, want the work directory", p.Dir())
	}
}

// Email labels are never verified and are not unique, so an ambiguous one must
// be refused — `quit` and `rm` act on whatever this returns.
func TestResolveRefusesAnAmbiguousEmail(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"work", "work2"} {
		if _, err := s.Create(name, "john@example.com", ""); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Resolve("john@example.com")
	if err == nil {
		t.Fatal("an ambiguous email resolved to one profile silently")
	}
	if !strings.Contains(err.Error(), "work") || !strings.Contains(err.Error(), "work2") {
		t.Fatalf("error should name the candidates: %v", err)
	}
	// Naming the profile is still unambiguous.
	if p, gerr := s.Resolve("work2"); gerr != nil || p.Name != "work2" {
		t.Fatalf("Resolve(work2) = %v, %v", p.Name, gerr)
	}
}

// A corrupt profile must not read as "no such profile": a later `open` would
// look like the profile had been deleted.
func TestResolveSurfacesACorruptProfileInsteadOfReportingItMissing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("work", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "work", "profile.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Resolve("work")
	if err == nil {
		t.Fatal("a corrupt profile resolved successfully")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("a corrupt profile was reported as missing: %v", err)
	}
}

// Only a genuine miss means the name is free — otherwise `add` would overwrite
// an existing profile's metadata and open its logged-in data dir as if new.
func TestCreateRefusesWhenExistenceCannotBeDetermined(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create("work", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "work", "profile.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("work", "", ""); err == nil {
		t.Fatal("created over a profile whose metadata could not be read")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Create("work", "a@b.c", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(p); err != nil {
		t.Fatal(err)
	}
	// No temp files left behind, and the profile still parses.
	entries, err := os.ReadDir(p.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
	if _, gerr := s.Get("work"); gerr != nil {
		t.Fatalf("profile unreadable after Touch: %v", gerr)
	}
}
