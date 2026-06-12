package config

import (
	"testing"

	"github.com/rigsmith/core/pathmap"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Retention.HistoryDays != 30 || c.Retention.SquashFactor != 2.0 || c.Retention.FloorBytes != 500<<20 {
		t.Errorf("retention defaults wrong: %+v", c.Retention)
	}
	if len(c.Roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(c.Roots))
	}
}

func TestRootLocation_PerOS(t *testing.T) {
	c := Default()
	mac := Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	win := Machine{Name: "pc", OS: pathmap.OSWindows, Home: `C:\Users\John`}
	lin := Machine{Name: "box", OS: pathmap.OSLinux, Home: "/home/john"}

	// CLI root identical everywhere
	if got, st := c.RootLocation("cli", mac); st != pathmap.StatusResolved || got != "/Users/john/.claude" {
		t.Errorf("cli mac = %q (%v)", got, st)
	}
	if got, _ := c.RootLocation("cli", win); got != `C:\Users\John\.claude` {
		t.Errorf("cli win = %q", got)
	}

	// Desktop root differs per OS
	if got, _ := c.RootLocation("desktop", mac); got != "/Users/john/Library/Application Support/Claude" {
		t.Errorf("desktop mac = %q", got)
	}
	if got, _ := c.RootLocation("desktop", win); got != `C:\Users\John\AppData\Roaming\Claude` {
		t.Errorf("desktop win = %q", got)
	}
	if got, _ := c.RootLocation("desktop", lin); got != "/home/john/.config/Claude" {
		t.Errorf("desktop linux = %q", got)
	}
}

func TestRootLocation_Unknown(t *testing.T) {
	if _, st := Default().RootLocation("nope", Machine{OS: pathmap.OSMacOS, Home: "/x"}); st != pathmap.StatusInvalid {
		t.Error("unknown root should be invalid")
	}
}

func TestMachineResolverAndFolders(t *testing.T) {
	m := Machine{OS: pathmap.OSMacOS, Home: "/Users/john", Tokens: map[string]string{"DROPBOX": "/Users/john/Dropbox"}}
	if got := m.Resolver().Resolve("$DROPBOX/x"); got.Path != "/Users/john/Dropbox/x" {
		t.Errorf("custom token resolve = %+v", got)
	}
	if m.Folders()["HOME"] != "/Users/john" {
		t.Error("HOME missing from folders")
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	c.Remote = "git@github.com:john/claude-sync.git"
	c.Machines["mbp"] = Detect("mbp")
	if err := Save(c, dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Remote != c.Remote || len(got.Machines) != 1 || len(got.Roots) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	// the per-OS desktop cascade survives the round-trip
	if got.Roots[1].Location.PerOS[pathmap.OSWindows] != `$HOME/AppData/Roaming/Claude` {
		t.Errorf("desktop cascade lost: %+v", got.Roots[1].Location)
	}
}
