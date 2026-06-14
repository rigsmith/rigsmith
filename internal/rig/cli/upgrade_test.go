package cli

import (
	"reflect"
	"testing"
)

func TestFilterDepsByName(t *testing.T) {
	deps := []outdatedDep{
		{name: "Newtonsoft.Json", latest: "13.0.3"},
		{name: "Serilog", latest: "3.1.1"},
		{name: "xunit", latest: "2.6.0"},
	}
	// Case-insensitive, keeps only the named packages, drops unknowns.
	got := filterDepsByName(deps, []string{"serilog", "XUNIT", "missing"})
	want := []outdatedDep{
		{name: "Serilog", latest: "3.1.1"},
		{name: "xunit", latest: "2.6.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got := filterDepsByName(deps, nil); got != nil {
		t.Fatalf("no names should match nothing, got %+v", got)
	}
}

func TestNameWidth(t *testing.T) {
	if w := nameWidth(nil); w != 0 {
		t.Fatalf("empty = %d, want 0", w)
	}
	if w := nameWidth([]outdatedDep{{name: "ab"}, {name: "abcd"}, {name: "abc"}}); w != 4 {
		t.Fatalf("longest = %d, want 4", w)
	}
	// Capped at 40 so a pathological name doesn't blow out the column.
	long := outdatedDep{name: "github.com/some/really/long/module/path/that/exceeds/forty"}
	if w := nameWidth([]outdatedDep{long}); w != 40 {
		t.Fatalf("capped = %d, want 40", w)
	}
}

func TestUpgradeTarget(t *testing.T) {
	// wanted (in-range) wins when present...
	if got := upgradeTarget(outdatedDep{current: "1.0.0", wanted: "1.2.0", latest: "2.0.0"}); got != "1.2.0" {
		t.Fatalf("with wanted = %q, want 1.2.0", got)
	}
	// ...else fall back to latest (go's in-major tag, .NET's latest).
	if got := upgradeTarget(outdatedDep{current: "1.0.0", latest: "1.3.0"}); got != "1.3.0" {
		t.Fatalf("no wanted = %q, want 1.3.0", got)
	}
}

func TestParseCargoUpdateDryRun(t *testing.T) {
	// `cargo update --dry-run` output: index line + upgrade/add/remove lines.
	text := "    Updating crates.io index\n" +
		"    Updating serde v1.0.190 -> v1.0.195\n" +
		"   Upgrading tokio v1.32.0 -> v1.35.1\n" +
		"      Adding once_cell v1.19.0\n" +
		"    Removing old-dep v0.1.0\n"
	got := parseCargoUpdateDryRun(text)
	want := []outdatedDep{
		{name: "serde", current: "1.0.190", wanted: "1.0.195", latest: "1.0.195"},
		{name: "tokio", current: "1.32.0", wanted: "1.35.1", latest: "1.35.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if d := parseCargoUpdateDryRun("    Updating crates.io index\n"); d != nil {
		t.Fatalf("no upgrades = %+v, want nil", d)
	}
}
