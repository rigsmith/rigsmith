// Ported from net-changesets Shared/PreStateRepositoryTests.cs.
package prestate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNoFileReturnsNil(t *testing.T) {
	ps, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ps != nil {
		t.Errorf("Read on missing pre.json = %+v, want nil", ps)
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	in := &PreState{
		Mode:            ModePre,
		Tag:             "next",
		InitialVersions: map[string]string{"pkg-a": "1.0.0", "pkg-b": "2.3.0"},
		Changesets:      []string{"brave-pandas-smile"},
	}
	if err := Write(dir, in); err != nil {
		t.Fatal(err)
	}
	if !Has(dir) {
		t.Error("Has should be true after Write")
	}

	out, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != ModePre || out.Tag != "next" {
		t.Errorf("round trip: mode=%q tag=%q", out.Mode, out.Tag)
	}
	if out.InitialVersions["pkg-b"] != "2.3.0" {
		t.Errorf("initialVersions = %v", out.InitialVersions)
	}
	if len(out.Changesets) != 1 || out.Changesets[0] != "brave-pandas-smile" {
		t.Errorf("changesets = %v", out.Changesets)
	}
	if !out.Contains("brave-pandas-smile") || out.Contains("other") {
		t.Error("Contains misreports the consumed list")
	}

	// The on-disk shape is shared with the JS tool: two-space indent, lowercase
	// keys, trailing newline.
	raw, err := os.ReadFile(filepath.Join(dir, "pre.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.HasSuffix(s, "}\n") {
		t.Error("pre.json should end with a single trailing newline")
	}
	for _, key := range []string{`"mode"`, `"tag"`, `"initialVersions"`, `"changesets"`} {
		if !strings.Contains(s, "  "+key) {
			t.Errorf("pre.json should contain two-space-indented %s:\n%s", key, s)
		}
	}
}

func TestRemoveDeletesFile(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, &PreState{Mode: ModePre, Tag: "next"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	if Has(dir) {
		t.Error("pre.json should be gone after Remove")
	}
	// Removing again is a no-op, not an error.
	if err := Remove(dir); err != nil {
		t.Errorf("Remove on absent file = %v, want nil", err)
	}
}
