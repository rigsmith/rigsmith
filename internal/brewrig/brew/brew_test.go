package brew

import (
	"context"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
)

// fake answers brew invocations from a canned table, and records what it was
// asked, so a test can assert on the arguments as well as the parse.
type fake struct {
	out  map[string]string
	errs map[string]error
	got  [][]string
}

func (f *fake) Run(_ context.Context, args ...string) ([]byte, error) {
	f.got = append(f.got, args)
	key := strings.Join(args, " ")
	if err, ok := f.errs[key]; ok {
		return nil, err
	}
	return []byte(f.out[key]), nil
}

const infoJSON = `{
  "formulae": [
    {"name":"gh","full_name":"gh","tap":"homebrew/core",
     "installed":[{"version":"2.45.0","installed_on_request":true,"time":1700000000}]},
    {"name":"oniguruma","full_name":"oniguruma","tap":"homebrew/core",
     "installed":[{"version":"6.9","installed_on_request":false,"time":1700000001}]},
    {"name":"depot","full_name":"depot/tap/depot","tap":"depot/tap",
     "installed":[{"version":"2.0","installed_on_request":true,"time":1700000002}]},
    {"name":"ghost","full_name":"ghost","tap":"homebrew/core","installed":[]}
  ],
  "casks": [
    {"token":"kitty","full_token":"kitty","tap":"homebrew/cask","version":"0.35","installed":"0.35","installed_time":1700000003},
    {"token":"never","full_token":"never","tap":"homebrew/cask","version":"1.0","installed":""}
  ]
}`

func newFake() *fake {
	return &fake{out: map[string]string{
		"info --json=v2 --installed": infoJSON,
		"tap":                        "homebrew/core\ndepot/tap\n",
		"--version":                  "Homebrew 7.0.4\n",
		"--prefix":                   "/opt/homebrew\n",
	}}
}

func TestInventoryPublishesOnlyWhatWasAskedFor(t *testing.T) {
	c := &Client{R: newFake()}

	m, err := c.Inventory(context.Background(), "pro", "macos")
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, p := range m.Formulae {
		names = append(names, p.Name)
	}
	want := []string{"depot/tap/depot", "gh"}
	if len(names) != len(want) {
		t.Fatalf("formulae = %v, want %v — dependencies and uninstalled entries must be excluded", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("formulae = %v, want %v", names, want)
		}
	}
}

// oniguruma is installed but was pulled in as a dependency. Publishing it would
// turn every upstream dependency change into drift between the machines.
func TestDependencyIsNotPublished(t *testing.T) {
	c := &Client{R: newFake()}
	m, _ := c.Inventory(context.Background(), "pro", "macos")

	if m.Has(inventory.Ref{Kind: inventory.Formula, Name: "oniguruma"}) {
		t.Fatal("a dependency was published; only installed_on_request should be")
	}
}

func TestNotInstalledEntriesAreSkipped(t *testing.T) {
	c := &Client{R: newFake()}
	m, _ := c.Inventory(context.Background(), "pro", "macos")

	if m.Has(inventory.Ref{Kind: inventory.Formula, Name: "ghost"}) {
		t.Error("a formula with no installed versions was published")
	}
	if m.Has(inventory.Ref{Kind: inventory.Cask, Name: "never"}) {
		t.Error("a cask that is not installed was published")
	}
}

// `brew install depot` and `brew install depot/tap/depot` are not the same
// request on a machine that has not tapped it, so the qualified name is what
// gets published.
func TestThirdPartyPackageKeepsItsTapQualifiedName(t *testing.T) {
	c := &Client{R: newFake()}
	m, _ := c.Inventory(context.Background(), "pro", "macos")

	p, ok := m.Lookup(inventory.Ref{Kind: inventory.Formula, Name: "depot/tap/depot"})
	if !ok {
		t.Fatal("third-party formula missing or published under its short name")
	}
	if p.Tap != "depot/tap" {
		t.Errorf("Tap = %q, want depot/tap", p.Tap)
	}
}

func TestCoreFormulaUsesItsShortName(t *testing.T) {
	c := &Client{R: newFake()}
	m, _ := c.Inventory(context.Background(), "pro", "macos")

	if !m.Has(inventory.Ref{Kind: inventory.Formula, Name: "gh"}) {
		t.Error("a homebrew/core formula should publish under its short name")
	}
}

func TestInventoryCarriesVersionsAndInstallTimes(t *testing.T) {
	c := &Client{R: newFake()}
	m, _ := c.Inventory(context.Background(), "pro", "macos")

	gh, _ := m.Lookup(inventory.Ref{Kind: inventory.Formula, Name: "gh"})
	if gh.Version != "2.45.0" {
		t.Errorf("version = %q, want 2.45.0 — skew reporting needs it", gh.Version)
	}
	if gh.InstalledAt != 1700000000 {
		t.Errorf("installedAt = %d, want 1700000000 — the retire race needs it", gh.InstalledAt)
	}
	k, _ := m.Lookup(inventory.Ref{Kind: inventory.Cask, Name: "kitty"})
	if k.Version != "0.35" || k.InstalledAt != 1700000003 {
		t.Errorf("cask = %+v, want version 0.35 at 1700000003", k)
	}
}

func TestInventoryRecordsBrewVersionAndPrefix(t *testing.T) {
	c := &Client{R: newFake()}
	m, _ := c.Inventory(context.Background(), "pro", "macos")

	if m.BrewVersion != "7.0.4" {
		t.Errorf("BrewVersion = %q, want 7.0.4", m.BrewVersion)
	}
	if m.Prefix != "/opt/homebrew" {
		t.Errorf("Prefix = %q, want /opt/homebrew", m.Prefix)
	}
}

// A formula and a cask can share a name, so every mutating call has to say
// which namespace it means or brew picks for us.
func TestInstallAndUninstallDisambiguateTheNamespace(t *testing.T) {
	f := newFake()
	c := &Client{R: f}
	ctx := context.Background()

	if err := c.Install(ctx, inventory.Ref{Kind: inventory.Cask, Name: "docker"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Uninstall(ctx, inventory.Ref{Kind: inventory.Formula, Name: "docker"}); err != nil {
		t.Fatal(err)
	}

	got := f.got
	if len(got) != 2 {
		t.Fatalf("calls = %v", got)
	}
	if strings.Join(got[0], " ") != "install --cask docker" {
		t.Errorf("install call = %v, want install --cask docker", got[0])
	}
	if strings.Join(got[1], " ") != "uninstall --formula docker" {
		t.Errorf("uninstall call = %v, want uninstall --formula docker", got[1])
	}
}

func TestOutdatedCoversBothNamespaces(t *testing.T) {
	f := newFake()
	f.out["outdated --formula --quiet"] = "gh\njq\n"
	f.out["outdated --cask --quiet"] = "kitty\n"

	refs, err := (&Client{R: f}).Outdated(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 {
		t.Fatalf("Outdated = %v, want 3 across both namespaces", refs)
	}
	var casks int
	for _, r := range refs {
		if r.Kind == inventory.Cask {
			casks++
		}
	}
	if casks != 1 {
		t.Errorf("cask count = %d, want 1", casks)
	}
}

func TestUpgradeGreedyIsOptIn(t *testing.T) {
	f := newFake()
	c := &Client{R: f}
	ctx := context.Background()

	_ = c.Upgrade(ctx, false)
	_ = c.Upgrade(ctx, true)

	if strings.Join(f.got[0], " ") != "upgrade" {
		t.Errorf("plain upgrade = %v, want no --greedy", f.got[0])
	}
	if strings.Join(f.got[1], " ") != "upgrade --greedy" {
		t.Errorf("greedy upgrade = %v, want --greedy", f.got[1])
	}
}
