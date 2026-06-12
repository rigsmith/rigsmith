// Ported from net-changesets Shared/ConfigurationServiceTests.cs +
// Shared/ChangelogConfigTests.cs, adapted to the Go config shape (the C# legacy
// flat-key migration has no Go equivalent; the single `dotnet` block is
// generalized into per-ecosystem blocks).
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if c.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main", c.BaseBranch)
	}
	if c.Access != "restricted" {
		t.Errorf("Access = %q, want restricted", c.Access)
	}
	if c.UpdateInternalDependencies != UpdatePatch {
		t.Errorf("UpdateInternalDependencies = %q, want patch", c.UpdateInternalDependencies)
	}
	if len(c.Ignore) != 0 || len(c.Fixed) != 0 || len(c.Linked) != 0 {
		t.Error("ignore/fixed/linked should default empty")
	}
	if c.Snapshot.UseCalculatedVersion || c.Snapshot.PrereleaseTemplate != "" {
		t.Error("snapshot should default to zero values")
	}
	if c.ChangelogSpec() != "default" {
		t.Errorf("ChangelogSpec() = %q, want default", c.ChangelogSpec())
	}
}

func TestParseSharedKeysAndEcosystemBlocks(t *testing.T) {
	c, err := Parse([]byte(`{
		"$schema": "https://unpkg.com/@changesets/config/schema.json",
		"baseBranch": "develop",
		"access": "public",
		"ignore": ["pkg-internal-*"],
		"fixed": [["pkg-a", "pkg-b"]],
		"linked": [["pkg-c", "pkg-d"]],
		"updateInternalDependencies": "minor",
		"snapshot": { "useCalculatedVersion": true, "prereleaseTemplate": "{tag}-{commit}" },
		"dotnet": { "versionStrategy": "lockstep" },
		"go": { "tagPrefix": "v" }
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseBranch != "develop" || c.Access != "public" {
		t.Errorf("shared keys: baseBranch=%q access=%q", c.BaseBranch, c.Access)
	}
	if c.UpdateInternalDependencies != UpdateMinor {
		t.Errorf("updateInternalDependencies = %q, want minor", c.UpdateInternalDependencies)
	}
	if len(c.Ignore) != 1 || c.Ignore[0] != "pkg-internal-*" {
		t.Errorf("ignore = %v", c.Ignore)
	}
	if len(c.Fixed) != 1 || len(c.Fixed[0]) != 2 || c.Fixed[0][0] != "pkg-a" {
		t.Errorf("fixed = %v", c.Fixed)
	}
	if len(c.Linked) != 1 || c.Linked[0][1] != "pkg-d" {
		t.Errorf("linked = %v", c.Linked)
	}
	if !c.Snapshot.UseCalculatedVersion || c.Snapshot.PrereleaseTemplate != "{tag}-{commit}" {
		t.Errorf("snapshot = %+v", c.Snapshot)
	}

	// Nested ecosystem blocks decode on demand; $schema is not a block.
	var dn struct {
		VersionStrategy string `json:"versionStrategy"`
	}
	if ok, err := c.Ecosystem("dotnet", &dn); err != nil || !ok {
		t.Fatalf("Ecosystem(dotnet) = %v, %v", ok, err)
	}
	if dn.VersionStrategy != "lockstep" {
		t.Errorf("dotnet.versionStrategy = %q", dn.VersionStrategy)
	}
	if _, ok := c.Ecosystems["$schema"]; ok {
		t.Error("$schema must not be bucketed as an ecosystem block")
	}
	var none struct{}
	if ok, err := c.Ecosystem("cargo", &none); err != nil || ok {
		t.Errorf("absent ecosystem: got ok=%v err=%v, want false,nil", ok, err)
	}
}

func TestParseUnknownAndMissingKeysTolerated(t *testing.T) {
	// Unknown keys are tolerated (bucketed as foreign ecosystem blocks); missing
	// keys keep their defaults.
	c, err := Parse([]byte(`{ "someFutureKey": { "x": 1 }, "access": "public" }`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Access != "public" {
		t.Errorf("access = %q", c.Access)
	}
	if c.BaseBranch != "main" || c.UpdateInternalDependencies != UpdatePatch {
		t.Error("missing keys should fall back to defaults")
	}
	if _, ok := c.Ecosystems["someFutureKey"]; !ok {
		t.Error("unknown key should be preserved in Ecosystems")
	}

	if _, err := Parse([]byte(`{ not json`)); err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestParseFormatBoolOrString(t *testing.T) {
	// `format` is polymorphic (false | string); the raw value must survive parse.
	for in, want := range map[string]string{
		`{ "format": false }`:    "false",
		`{ "format": "native" }`: `"native"`,
		`{ "format": true }`:     "true",
	} {
		c, err := Parse([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if string(c.Format) != want {
			t.Errorf("Format raw = %s, want %s", c.Format, want)
		}
	}
	c, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Format) != 0 {
		t.Errorf("absent format should stay empty, got %s", c.Format)
	}

	// FormatSpec resolves the raw value to a formatter name.
	for in, want := range map[string]string{
		`{}`:                       "",
		`{ "format": false }`:      "",
		`{ "format": null }`:       "",
		`{ "format": "native" }`:   "native",
		`{ "format": "auto" }`:     "auto",
		`{ "format": "prettier" }`: "prettier",
		`{ "format": true }`:       "true", // lands on the unknown-formatter warning path
	} {
		c, err := Parse([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if got := c.FormatSpec(); got != want {
			t.Errorf("FormatSpec(%s) = %q, want %q", in, got, want)
		}
	}
}

// Mirrors ChangelogConfigTests: the polymorphic `changelog` value resolves to a
// generator id.
func TestChangelogSpec(t *testing.T) {
	cases := map[string]string{
		`{}`:                     "default",
		`{ "changelog": false }`: "default",
		`{ "changelog": null }`:  "default",
		`{ "changelog": "" }`:    "default",
		`{ "changelog": "@changesets/cli/changelog" }`:                         "@changesets/cli/changelog",
		`{ "changelog": "@changesets/changelog-git" }`:                         "@changesets/changelog-git",
		`{ "changelog": ["@changesets/changelog-github", { "repo": "o/r" }] }`: "@changesets/changelog-github",
		`{ "changelog": [] }`: "default",
	}
	for in, want := range cases {
		c, err := Parse([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if got := c.ChangelogSpec(); got != want {
			t.Errorf("ChangelogSpec(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestGroupsOverride(t *testing.T) {
	c, err := Parse([]byte(`{ "changelogGroups": [
		{ "type": "feat", "section": "Features", "bump": "minor" }
	] }`))
	if err != nil {
		t.Fatal(err)
	}
	g := c.Groups()
	if len(g) != 1 || g[0].Section != "Features" {
		t.Errorf("configured groups should override defaults, got %+v", g)
	}
	if got := Default().Groups(); len(got) != len(DefaultChangelogGroups) {
		t.Errorf("unconfigured Groups() should be the defaults, got %d groups", len(got))
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{ "baseBranch": "trunk" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseBranch != "trunk" {
		t.Errorf("baseBranch = %q", c.BaseBranch)
	}
	if _, err := Load(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing config.json should error")
	}
}
