package engine

import (
	"context"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/brewrig/brew"
	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
	"github.com/rigsmith/rigsmith/internal/brewrig/plan"
)

// stub is a brew Runner that reports a fixed set of requested formulae, and
// records the mutating calls it was asked to make.
type stub struct {
	formulae []string
	fail     map[string]error
	calls    []string
}

func (s *stub) Run(_ context.Context, args ...string) ([]byte, error) {
	switch args[0] {
	case "info":
		out := `{"formulae":[`
		for i, n := range s.formulae {
			if i > 0 {
				out += ","
			}
			out += `{"name":"` + n + `","full_name":"` + n + `","tap":"homebrew/core","installed":[{"version":"1.0","installed_on_request":true,"time":100}]}`
		}
		return []byte(out + `],"casks":[]}`), nil
	case "tap":
		if len(args) == 1 {
			return []byte("homebrew/core\n"), nil
		}
	case "--version":
		return []byte("Homebrew 7.0.4\n"), nil
	case "--prefix":
		return []byte("/opt/homebrew\n"), nil
	}
	call := args[0]
	for _, a := range args[1:] {
		call += " " + a
	}
	s.calls = append(s.calls, call)
	if err, ok := s.fail[call]; ok {
		return nil, err
	}
	return nil, nil
}

func client(formulae ...string) (*brew.Client, *stub) {
	s := &stub{formulae: formulae, fail: map[string]error{}}
	return &brew.Client{R: s}, s
}

func ref(n string) inventory.Ref { return inventory.Ref{Kind: inventory.Formula, Name: n} }

// The core of sync: a package that was published last time and is gone now was
// deliberately uninstalled, and that intent has to be recorded or it is lost.
func TestSnapshotRecordsADeliberateUninstall(t *testing.T) {
	c, _ := client("gh")
	prev := &inventory.Machine{Name: "pro", Formulae: []inventory.Package{{Name: "gh"}, {Name: "wget"}}}
	prev.Normalize()
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

	cur, retired, err := Snapshot(context.Background(), c, "pro", "macos", prev, now)
	if err != nil {
		t.Fatal(err)
	}

	if len(retired) != 1 || retired[0].Name != "wget" {
		t.Fatalf("retired = %v, want [wget]", retired)
	}
	at, ok := cur.RetiredAt(ref("wget"))
	if !ok {
		t.Fatal("the retirement was not written to the published inventory")
	}
	if !at.Equal(now) {
		t.Errorf("retired at %v, want %v", at, now)
	}
}

func TestFirstSnapshotRetiresNothing(t *testing.T) {
	c, _ := client("gh")

	cur, retired, err := Snapshot(context.Background(), c, "pro", "macos", nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 0 {
		t.Fatalf("retired = %v, want none on a first sync — there is nothing to compare against", retired)
	}
	if len(cur.Retired) != 0 {
		t.Errorf("Retired = %v, want empty", cur.Retired)
	}
}

// Reinstalling something this machine dropped is a change of mind, and it has
// to clear the machine's own stale record or the package can never come back.
func TestReinstallClearsThisMachinesOwnRetirement(t *testing.T) {
	c, _ := client("gh", "wget")
	prev := &inventory.Machine{Name: "pro", Formulae: []inventory.Package{{Name: "gh"}}}
	prev.Retire(ref("wget"), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	prev.Normalize()

	cur, _, err := Snapshot(context.Background(), c, "pro", "macos", prev, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cur.RetiredAt(ref("wget")); ok {
		t.Fatal("wget is installed again but still marked retired")
	}
}

// Re-observing the same absence must not push the stamp forward, or a retire
// would keep overtaking an older reinstall on the other machine.
func TestRetireStampDoesNotDriftForward(t *testing.T) {
	c, _ := client("gh")
	first := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	prev := &inventory.Machine{Name: "pro", Formulae: []inventory.Package{{Name: "gh"}}}
	prev.Retire(ref("wget"), first)
	prev.Normalize()

	later := first.Add(72 * time.Hour)
	cur, retired, err := Snapshot(context.Background(), c, "pro", "macos", prev, later)
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 0 {
		t.Errorf("retired = %v, want none — it was already retired, not newly so", retired)
	}
	at, _ := cur.RetiredAt(ref("wget"))
	if !at.Equal(first) {
		t.Fatalf("retire stamp moved to %v, want it pinned at %v", at, first)
	}
}

func TestSnapshotCarriesOptOutsForward(t *testing.T) {
	c, _ := client("gh")
	prev := &inventory.Machine{Name: "pro", Formulae: []inventory.Package{{Name: "gh"}}}
	prev.OptOut.Formulae = []string{"libreoffice"}
	prev.Normalize()

	cur, _, err := Snapshot(context.Background(), c, "pro", "macos", prev, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(cur.OptOut.Formulae) != 1 || cur.OptOut.Formulae[0] != "libreoffice" {
		t.Fatalf("OptOut = %v, want it preserved across syncs", cur.OptOut.Formulae)
	}
}

// Installing something you previously skipped is the more recent, more explicit
// act, so the stale opt-out has to go with it.
func TestInstallingAnOptedOutPackageClearsTheOptOut(t *testing.T) {
	c, _ := client("gh", "libreoffice")
	prev := &inventory.Machine{Name: "pro", Formulae: []inventory.Package{{Name: "gh"}}}
	prev.OptOut.Formulae = []string{"libreoffice"}
	prev.Normalize()

	cur, _, err := Snapshot(context.Background(), c, "pro", "macos", prev, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(cur.OptOut.Formulae) != 0 {
		t.Fatalf("OptOut = %v, want cleared once the package is installed", cur.OptOut.Formulae)
	}
}

func TestApplyInstallsAndTapsFirst(t *testing.T) {
	c, s := client()
	p := &plan.Plan{
		Taps:    []string{"depot/tap"},
		Install: []plan.Install{{Ref: ref("depot/tap/depot"), Tap: "depot/tap"}},
	}

	res := Apply(context.Background(), c, p, nil)

	if len(res.Failed) != 0 {
		t.Fatalf("failed = %v", res.Failed)
	}
	if len(s.calls) != 2 || s.calls[0] != "tap depot/tap" {
		t.Fatalf("calls = %v, want the tap before the install", s.calls)
	}
}

// brew cannot roll back, so one bad package must not silently abandon the rest
// of a long run.
func TestApplyContinuesPastAFailureAndReportsIt(t *testing.T) {
	c, s := client()
	s.fail["install --formula bad"] = errNope
	p := &plan.Plan{Install: []plan.Install{
		{Ref: ref("bad")}, {Ref: ref("good")},
	}}

	res := Apply(context.Background(), c, p, nil)

	if len(res.Installed) != 1 || res.Installed[0].Name != "good" {
		t.Fatalf("installed = %v, want the later package to still be attempted", res.Installed)
	}
	if len(res.Failed) != 1 || res.Failed[0].Ref.Name != "bad" {
		t.Fatalf("failed = %v, want the failure reported", res.Failed)
	}
}

// A failed tap explains every install that depended on it; attempting them
// anyway produces a wall of identical, misleading "no such formula" errors.
func TestPackagesFromAFailedTapAreSkippedWithThatReason(t *testing.T) {
	c, s := client()
	s.fail["tap depot/tap"] = errNope
	p := &plan.Plan{
		Taps:    []string{"depot/tap"},
		Install: []plan.Install{{Ref: ref("depot/tap/depot"), Tap: "depot/tap"}},
	}

	res := Apply(context.Background(), c, p, nil)

	for _, call := range s.calls {
		if call == "install --formula depot/tap/depot" {
			t.Fatal("installed from a tap that could not be added")
		}
	}
	if len(res.Failed) != 2 {
		t.Fatalf("failed = %v, want both the tap and the package it blocked", res.Failed)
	}
}

var errNope = &stubErr{}

type stubErr struct{}

func (e *stubErr) Error() string { return "nope" }
