package commands

import (
	"errors"
	"os"
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

// A binding the user set is an explicit target: if it cannot be honoured, say
// so. Falling through would ask which profile they meant when they have already
// said — and a picker could then act on a different one.
func TestResolveDesktopTargetReportsABrokenDirectoryBinding(t *testing.T) {
	st := targetStore(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	t.Chdir(dir)

	dm, err := dirmapStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := dm.Set(dir, func(e *dirmap.Entry) { e.Desktop = "work" }); serr != nil {
		t.Fatal(serr)
	}
	// Corrupt the bound profile's metadata.
	if werr := os.WriteFile(filepath.Join(st.Root, "work", "profile.json"), []byte("{not json"), 0o600); werr != nil {
		t.Fatal(werr)
	}

	_, err = resolveDesktopTarget(st, stubApp{}, nil, false)
	if err == nil {
		t.Fatal("a broken binding fell through instead of being reported")
	}
	for _, want := range []string{"mapped", "work", "unmap"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// scanFailApp cannot determine whether anything is running.
type scanFailApp struct{ stubApp }

func (scanFailApp) Running(string) ([]int, error) { return nil, errors.New("pgrep exploded") }

// "We could not look" must never be reported as "nothing is open" — that would
// let `quit` exit zero on an unknown state, contradicting IsRunning's contract
// and the named-profile path.
func TestPickProfileDoesNotTreatAFailedScanAsClosed(t *testing.T) {
	st := targetStore(t)
	// profileOptions, not pickProfile: reaching the form would block forever
	// waiting for input CI will never send.
	opts, _, err := profileOptions(st, scanFailApp{}, true)
	if errors.Is(err, errNoOpenProfiles) {
		t.Fatal("a failed scan was reported as 'no profile windows are open'")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) == 0 {
		t.Fatal("a profile whose state could not be determined was hidden from the picker")
	}
}

// A profile genuinely determined to be closed is still hidden from `quit`.
func TestPickProfileHidesProfilesKnownToBeClosed(t *testing.T) {
	st := targetStore(t)
	_, _, err := profileOptions(st, stubApp{open: map[string]bool{}}, true)
	if !errors.Is(err, errNoOpenProfiles) {
		t.Fatalf("err = %v, want errNoOpenProfiles when every profile is known closed", err)
	}
}
