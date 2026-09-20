package inventory

import (
	"bytes"
	"testing"
	"time"
)

func TestRefRoundTrips(t *testing.T) {
	for _, r := range []Ref{{Formula, "gh"}, {Cask, "kitty"}, {Formula, "depot/tap/depot"}} {
		got, err := ParseRef(r.String())
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", r.String(), err)
		}
		if got != r {
			t.Errorf("round trip of %v gave %v", r, got)
		}
	}
}

// A malformed key must not be guessed at: treating it as a formula could make
// brewrig propose uninstalling a package that merely shares the name.
func TestParseRefRejectsRatherThanGuesses(t *testing.T) {
	for _, s := range []string{"gh", "", "widget:gh", "formula:"} {
		if _, err := ParseRef(s); err == nil {
			t.Errorf("ParseRef(%q) succeeded, want an error", s)
		}
	}
}

func TestCaskLabelIsMarked(t *testing.T) {
	if got := (Ref{Kind: Cask, Name: "docker"}).Label(); got != "docker (cask)" {
		t.Errorf("Label = %q, want the cask marked — the name is ambiguous", got)
	}
	if got := (Ref{Kind: Formula, Name: "docker"}).Label(); got != "docker" {
		t.Errorf("Label = %q, want the bare name", got)
	}
}

// Two machines committing reordered-but-identical files at each other is pure
// noise, so an unchanged inventory has to marshal byte-identically.
func TestMarshalIsStableRegardlessOfInputOrder(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	a := &Machine{Name: "pro", SyncedAt: now,
		Formulae: []Package{{Name: "jq"}, {Name: "gh"}},
		Casks:    []Package{{Name: "kitty"}},
		Taps:     []string{"z/tap", "a/tap"}}
	b := &Machine{Name: "pro", SyncedAt: now,
		Formulae: []Package{{Name: "gh"}, {Name: "jq"}},
		Casks:    []Package{{Name: "kitty"}},
		Taps:     []string{"a/tap", "z/tap"}}

	ab, err := Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ab, bb) {
		t.Fatalf("same inventory marshalled differently:\n%s\n---\n%s", ab, bb)
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	m := &Machine{Name: "pro", OS: "macos", SyncedAt: time.Now(),
		Formulae: []Package{{Name: "gh", Version: "2.45.0", InstalledAt: 100, Tap: "homebrew/core"}},
		Casks:    []Package{{Name: "kitty", Version: "0.35"}}}
	m.Retire(Ref{Kind: Formula, Name: "wget"}, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	m.OptOut.Casks = []string{"libreoffice"}

	b, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}

	if !got.Has(Ref{Kind: Formula, Name: "gh"}) || !got.Has(Ref{Kind: Cask, Name: "kitty"}) {
		t.Error("packages lost in the round trip")
	}
	if _, ok := got.RetiredAt(Ref{Kind: Formula, Name: "wget"}); !ok {
		t.Error("retirement lost in the round trip — an uninstall intent would vanish")
	}
	if !got.OptedOut(Ref{Kind: Cask, Name: "libreoffice"}) {
		t.Error("opt-out lost in the round trip")
	}
}

func TestUnmarshalRefusesAFutureSchema(t *testing.T) {
	if _, err := Unmarshal([]byte(`{"schema":99,"machine":"pro"}`)); err == nil {
		t.Fatal("accepted a newer schema; an old binary must not half-read a future format")
	}
}

func TestUnmarshalRequiresAMachineName(t *testing.T) {
	if _, err := Unmarshal([]byte(`{"schema":1,"formulae":[]}`)); err == nil {
		t.Fatal("accepted a file with no machine name")
	}
}

func TestRetireKeepsTheEarliestObservation(t *testing.T) {
	m := &Machine{Name: "pro"}
	first := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	m.Retire(Ref{Kind: Formula, Name: "wget"}, first)
	m.Retire(Ref{Kind: Formula, Name: "wget"}, first.Add(48*time.Hour))

	at, _ := m.RetiredAt(Ref{Kind: Formula, Name: "wget"})
	if !at.Equal(first) {
		t.Fatalf("retire stamp = %v, want the first observation %v", at, first)
	}
}

func TestOptOutIsPerNamespace(t *testing.T) {
	m := &Machine{Name: "pro"}
	m.OptOut.Casks = []string{"docker"}

	if !m.OptedOut(Ref{Kind: Cask, Name: "docker"}) {
		t.Error("the cask opt-out did not apply to the cask")
	}
	if m.OptedOut(Ref{Kind: Formula, Name: "docker"}) {
		t.Error("a cask opt-out leaked onto the formula of the same name")
	}
}
