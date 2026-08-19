package commands

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/dirmap"
)

// stubApp reports a fixed set of profiles as open.
type stubApp struct{ open map[string]bool }

func (s stubApp) Installed() (string, bool) { return "/Applications/Claude.app", true }
func (s stubApp) Launch(string) error       { return nil }
func (s stubApp) Focus(string) error        { return nil }
func (s stubApp) Quit(string, time.Duration) error {
	return nil
}
func (s stubApp) Running(dir string) ([]int, error) {
	if s.open[dir] {
		return []int{1}, nil
	}
	return nil, nil
}

func targetStore(t *testing.T) *desktop.Store {
	t.Helper()
	st := desktop.NewStore(filepath.Join(t.TempDir(), "desktop"))
	for _, n := range []string{"work", "personal"} {
		if _, err := st.Create(n, n+"@example.com", ""); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// A named profile is used as given — no mapping, no picker.
func TestResolveDesktopTargetPrefersTheNamedProfile(t *testing.T) {
	st := targetStore(t)
	p, err := resolveDesktopTarget(st, stubApp{}, []string{"work"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "work" {
		t.Fatalf("resolved %q, want work", p.Name)
	}
}

// A directory binding is an answer the user already gave, so it beats asking.
func TestResolveDesktopTargetUsesTheDirectoryBinding(t *testing.T) {
	st := targetStore(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	dir := t.TempDir()
	t.Chdir(dir)

	dm, err := dirmapStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := dm.Set(dir, func(e *dirmap.Entry) { e.Desktop = "personal" }); serr != nil {
		t.Fatal(serr)
	}
	p, err := resolveDesktopTarget(st, stubApp{}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "personal" {
		t.Fatalf("resolved %q, want the mapped personal", p.Name)
	}
}

// Off a terminal, with nothing named and nothing mapped, it must say how to
// choose rather than pick one.
func TestResolveDesktopTargetRefusesToGuessOffATerminal(t *testing.T) {
	st := targetStore(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())

	_, err := resolveDesktopTarget(st, stubApp{}, nil, false)
	if err == nil {
		t.Fatal("a profile was chosen with no name, no mapping and no terminal")
	}
	if errors.Is(err, errCancelled) || errors.Is(err, errNoOpenProfiles) {
		t.Fatalf("unexpected sentinel: %v", err)
	}
}

// An unknown name is reported as such, with the two ways forward.
func TestResolveDesktopTargetReportsAnUnknownName(t *testing.T) {
	st := targetStore(t)
	_, err := resolveDesktopTarget(st, stubApp{}, []string{"nope"}, false)
	if err == nil {
		t.Fatal("an unknown profile resolved")
	}
	if !errors.Is(err, desktop.ErrNotFound) && !strings.Contains(err.Error(), "no Desktop profile") {
		t.Fatalf("unhelpful error: %v", err)
	}
}
