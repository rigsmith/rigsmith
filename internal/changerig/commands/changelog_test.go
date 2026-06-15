package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

func TestChangelogEntry(t *testing.T) {
	tests := []struct {
		name              string
		version, typ, msg string
		want              string
	}{
		{"defaults to Unreleased", "", "", "Dropped Node 16", "## Unreleased\n\n- Dropped Node 16\n"},
		{"explicit version", "1.2.0", "", "Backfilled history", "## 1.2.0\n\n- Backfilled history\n"},
		{"type label", "", "fix", "CVE-2026-0001 patched", "## Unreleased\n\n- **fix:** CVE-2026-0001 patched\n"},
		{"trims whitespace", "  1.0.0  ", "  ", "  hi  ", "## 1.0.0\n\n- hi\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := changelogEntry(tc.version, tc.typ, tc.msg); got != tc.want {
				t.Errorf("changelogEntry(%q,%q,%q) =\n%q\nwant\n%q", tc.version, tc.typ, tc.msg, got, tc.want)
			}
		})
	}
}

func TestFormatChangelogFileNativeFallback(t *testing.T) {
	// With no `format` configured, the native markdown formatter still tidies the
	// file (the "stays formatted" guarantee for hand-edits).
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# demo\n\n\n##   1.0.0\n*  a\n+ b\n\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: dir, Config: config.Default()}

	formatChangelogFile(&cobra.Command{}, ws, path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "##   1.0.0") || strings.Contains(s, "*  a") || strings.Contains(s, "+ b") {
		t.Errorf("changelog not normalized by native formatter:\n%s", s)
	}
}

func TestResolveChangelogPackage(t *testing.T) {
	a := plugin.Package{Name: "a"}
	b := plugin.Package{Name: "b"}

	// Named package is found.
	if got, err := resolveChangelogPackage([]plugin.Package{a, b}, []string{"b"}); err != nil || got.Name != "b" {
		t.Errorf("named = %+v, %v; want package b", got, err)
	}
	// Unknown name errors.
	if _, err := resolveChangelogPackage([]plugin.Package{a}, []string{"z"}); err == nil {
		t.Error("expected error for unknown package")
	}
	// Sole package is the default.
	if got, err := resolveChangelogPackage([]plugin.Package{a}, nil); err != nil || got.Name != "a" {
		t.Errorf("sole = %+v, %v; want package a", got, err)
	}
	// Ambiguous (multiple, none named) errors.
	if _, err := resolveChangelogPackage([]plugin.Package{a, b}, nil); err == nil {
		t.Error("expected error when multiple packages and none named")
	}
}
