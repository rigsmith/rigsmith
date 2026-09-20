package plan

import (
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
)

func mach(name string, pkgs ...inventory.Package) *inventory.Machine {
	m := &inventory.Machine{Schema: 1, Name: name, OS: "macos", Formulae: pkgs}
	m.Normalize()
	return m
}

func cask(name string) inventory.Package { return inventory.Package{Name: name} }
func pkg(name string) inventory.Package  { return inventory.Package{Name: name} }
func at(name string, t int64) inventory.Package {
	return inventory.Package{Name: name, InstalledAt: t}
}

func refs(ins []Install) []string {
	out := make([]string, len(ins))
	for i, in := range ins {
		out[i] = in.Ref.String()
	}
	return out
}

func TestInstallsWhatTheOtherMachineHas(t *testing.T) {
	a := mach("air", pkg("gh"), pkg("jq"))
	b := mach("pro", pkg("gh"))

	p := Build(b, []*inventory.Machine{a, b})

	if got := refs(p.Install); len(got) != 1 || got[0] != "formula:jq" {
		t.Fatalf("Install = %v, want [formula:jq]", got)
	}
	if len(p.Remove) != 0 {
		t.Fatalf("Remove = %v, want none — a union never uninstalls to match", p.Remove)
	}
}

// The additive rule, stated as a test: a package only this machine has is not
// drift to correct, it is something the other machine has not caught up on.
func TestNeverRemovesToMatchTheUnion(t *testing.T) {
	a := mach("air")
	b := mach("pro", pkg("only-here"))

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Remove) != 0 {
		t.Fatalf("Remove = %v, want none", p.Remove)
	}
	if len(p.Install) != 0 {
		t.Fatalf("Install = %v, want none", refs(p.Install))
	}
}

func TestRetiredPackageIsOfferedForRemovalAndNotReinstalled(t *testing.T) {
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	a := mach("air")
	a.Retire(inventory.Ref{Kind: inventory.Formula, Name: "wget"}, when)
	b := mach("pro", pkg("wget"))

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Install) != 0 {
		t.Fatalf("Install = %v, want none — a retired package must not be reinstalled", refs(p.Install))
	}
	if len(p.Remove) != 1 || p.Remove[0].Ref.Name != "wget" {
		t.Fatalf("Remove = %v, want wget", p.Remove)
	}
	if p.Remove[0].By != "air" {
		t.Errorf("Remove.By = %q, want air — the prompt has to name who removed it", p.Remove[0].By)
	}
}

// The deadlock this whole mechanism exists to break: without comparing
// timestamps, A retires, B reinstalls, A retires again, forever.
func TestReinstallLaterThanRetireWins(t *testing.T) {
	retired := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	reinstalled := retired.Add(24 * time.Hour).Unix()

	a := mach("air")
	a.Retire(inventory.Ref{Kind: inventory.Formula, Name: "wget"}, retired)
	b := mach("pro", at("wget", reinstalled))

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Remove) != 0 {
		t.Fatalf("Remove = %v, want none — the reinstall is newer than the retirement", p.Remove)
	}
	// And the machine that retired it should be offered it back.
	pa := Build(a, []*inventory.Machine{a, b})
	if got := refs(pa.Install); len(got) != 1 || got[0] != "formula:wget" {
		t.Fatalf("Install on the retiring machine = %v, want [formula:wget]", got)
	}
}

func TestRetireLaterThanReinstallStillRetires(t *testing.T) {
	installed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	retired := installed.Add(24 * time.Hour)

	a := mach("air")
	a.Retire(inventory.Ref{Kind: inventory.Formula, Name: "wget"}, retired)
	b := mach("pro", at("wget", installed.Unix()))

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Remove) != 1 {
		t.Fatalf("Remove = %v, want wget offered for removal", p.Remove)
	}
}

// A tie must not silently reinstall software someone removed. brew records
// install time in whole seconds, so a tie is coarse resolution far more often
// than it is a genuine same-instant reinstall.
func TestEqualTimestampsKeepTheRetirement(t *testing.T) {
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	a := mach("air")
	a.Retire(inventory.Ref{Kind: inventory.Formula, Name: "wget"}, when)
	b := mach("pro", at("wget", when.Unix()))

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Remove) != 1 {
		t.Fatalf("Remove = %v, want the retirement to stand on a tie", p.Remove)
	}
}

func TestMachineNeverOffersToRemoveItsOwnRetirement(t *testing.T) {
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	// A retired it but somehow still lists it — a torn state we should not
	// act on by prompting the user to remove something they removed.
	a := mach("air", pkg("wget"))
	a.Retire(inventory.Ref{Kind: inventory.Formula, Name: "wget"}, when)

	p := Build(a, []*inventory.Machine{a})

	for _, rm := range p.Remove {
		if rm.By == a.Name {
			t.Fatalf("machine offered to remove its own retirement: %v", rm)
		}
	}
}

func TestOptOutSuppressesTheInstall(t *testing.T) {
	a := mach("air", pkg("libreoffice"))
	b := mach("pro")
	b.OptOut.Formulae = []string{"libreoffice"}

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Install) != 0 {
		t.Fatalf("Install = %v, want none — it is opted out here", refs(p.Install))
	}
	if len(p.OptedOut) != 1 {
		t.Fatalf("OptedOut = %v, want it reported rather than silently missing", p.OptedOut)
	}
}

func TestFormulaAndCaskAreDifferentPackages(t *testing.T) {
	a := &inventory.Machine{Name: "air", Casks: []inventory.Package{cask("docker")}}
	a.Normalize()
	b := mach("pro", pkg("docker"))

	p := Build(b, []*inventory.Machine{a, b})

	if got := refs(p.Install); len(got) != 1 || got[0] != "cask:docker" {
		t.Fatalf("Install = %v, want [cask:docker] — the cask is a different package from the formula", got)
	}
}

func TestThirdPartyTapIsCollected(t *testing.T) {
	a := mach("air", inventory.Package{Name: "depot/tap/depot", Tap: "depot/tap"})
	b := mach("pro")

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Taps) != 1 || p.Taps[0] != "depot/tap" {
		t.Fatalf("Taps = %v, want [depot/tap]", p.Taps)
	}
}

func TestDefaultTapsAreNotCollected(t *testing.T) {
	a := mach("air", inventory.Package{Name: "jq", Tap: "homebrew/core"})
	b := mach("pro")

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Taps) != 0 {
		t.Fatalf("Taps = %v, want none — homebrew/core needs no `brew tap`", p.Taps)
	}
}

func TestSkewReportsBothVersionsAndWhoIsBehind(t *testing.T) {
	a := mach("air", inventory.Package{Name: "gh", Version: "2.40.0"})
	b := mach("pro", inventory.Package{Name: "gh", Version: "2.45.0"})

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Skew) != 1 {
		t.Fatalf("Skew = %v, want one entry", p.Skew)
	}
	s := p.Skew[0]
	if s.Versions["air"] != "2.40.0" || s.Versions["pro"] != "2.45.0" {
		t.Errorf("Versions = %v, want both machines' versions", s.Versions)
	}
	if len(s.Behind) != 1 || s.Behind[0] != "air" {
		t.Errorf("Behind = %v, want [air]", s.Behind)
	}
}

func TestMatchingVersionsAreNotSkew(t *testing.T) {
	a := mach("air", inventory.Package{Name: "gh", Version: "2.45.0"})
	b := mach("pro", inventory.Package{Name: "gh", Version: "2.45.0"})

	if p := Build(b, []*inventory.Machine{a, b}); len(p.Skew) != 0 {
		t.Fatalf("Skew = %v, want none", p.Skew)
	}
}

// Skew is resolved by `update`, not `apply`; a plan that is only skew has no
// work for apply to do, and reporting otherwise would make apply prompt for
// nothing.
func TestSkewAloneIsNotWork(t *testing.T) {
	a := mach("air", inventory.Package{Name: "gh", Version: "2.40.0"})
	b := mach("pro", inventory.Package{Name: "gh", Version: "2.45.0"})

	if p := Build(b, []*inventory.Machine{a, b}); !p.Empty() {
		t.Fatalf("Empty() = false, want true when only versions differ")
	}
}

func TestMalformedRetiredKeyIsIgnoredNotMisread(t *testing.T) {
	a := mach("air")
	a.Retired = map[string]time.Time{"garbage-no-kind": time.Now().UTC()}
	b := mach("pro", pkg("garbage-no-kind"))

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Remove) != 0 {
		t.Fatalf("Remove = %v, want none — an unparseable key must not uninstall anything", p.Remove)
	}
}

func TestFreshMachineWithNoPublishedFileInstallsEverything(t *testing.T) {
	a := mach("air", pkg("gh"), pkg("jq"))
	fresh := mach("new")

	p := Build(fresh, []*inventory.Machine{a, fresh})

	if len(p.Install) != 2 {
		t.Fatalf("Install = %v, want both packages", refs(p.Install))
	}
}

// Declining a removal has to stick. `apply` tells the user "keeping <x> —
// noted, it won't be offered again", and a prompt that reappears every run is
// how someone learns to dismiss the one destructive prompt without reading it.
func TestADeclinedRemovalIsNotOfferedAgain(t *testing.T) {
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	a := mach("air")
	a.Retire(inventory.Ref{Kind: inventory.Formula, Name: "cloc"}, when)
	b := mach("pro", pkg("cloc"))
	b.Acknowledge(inventory.Ref{Kind: inventory.Formula, Name: "cloc"}, when)

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Remove) != 0 {
		t.Fatalf("Remove = %v, want none — this removal was already declined here", p.Remove)
	}
	if len(p.Install) != 0 {
		t.Fatalf("Install = %v, want none — declining a removal is not a request to reinstall", refs(p.Install))
	}
}

// Declining one retirement must not silence a later, separate decision to
// remove the same package. The acknowledgement is tied to the retirement it
// answered, not to the package forever.
func TestADeclinedRemovalIsOfferedAgainWhenItIsRetiredAfresh(t *testing.T) {
	first := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	again := first.Add(30 * 24 * time.Hour)

	a := mach("air")
	a.Retire(inventory.Ref{Kind: inventory.Formula, Name: "cloc"}, again)
	b := mach("pro", pkg("cloc"))
	b.Acknowledge(inventory.Ref{Kind: inventory.Formula, Name: "cloc"}, first)

	p := Build(b, []*inventory.Machine{a, b})

	if len(p.Remove) != 1 {
		t.Fatalf("Remove = %v, want cloc offered again — this is a newer retirement "+
			"than the one that was declined", p.Remove)
	}
}
