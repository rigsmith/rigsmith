package desktop

import (
	"errors"
	"os"
	"path/filepath"
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
	// The session of a logged-in account lives here in full.
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Fatalf("data dir mode = %v, want 0700", perm)
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
	if !IsRunning(app, work) {
		t.Fatal("launched profile reports closed")
	}
	// The whole point of the model: one profile open says nothing about another.
	if IsRunning(app, personal) {
		t.Fatal("a second profile reports open because the first is")
	}
	if err := app.Quit(work, time.Second); err != nil {
		t.Fatal(err)
	}
	if IsRunning(app, work) {
		t.Fatal("profile still reports open after quit")
	}
}

func TestWaitGoneReturnsFalseWhenTheAppStaysUp(t *testing.T) {
	app := newFakeApp()
	_ = app.Launch("/p/work/data")
	if waitGone(app, "/p/work/data", time.Now().Add(300*time.Millisecond)) {
		t.Fatal("waitGone reported a still-running instance as gone")
	}
}
