package changeset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStandard(t *testing.T) {
	content := `---
"PackageA": major
"PackageB": minor
---

Add a telemetry endpoint

With a longer body.`
	cs, err := Parse(content, "happy-lions-jump")
	if err != nil {
		t.Fatal(err)
	}
	if cs.ID != "happy-lions-jump" {
		t.Errorf("ID = %q", cs.ID)
	}
	if len(cs.Releases) != 2 {
		t.Fatalf("releases = %d, want 2", len(cs.Releases))
	}
	if cs.Releases[0] != (Release{"PackageA", BumpMajor}) {
		t.Errorf("release[0] = %+v", cs.Releases[0])
	}
	if cs.Releases[1] != (Release{"PackageB", BumpMinor}) {
		t.Errorf("release[1] = %+v", cs.Releases[1])
	}
	want := "Add a telemetry endpoint\n\nWith a longer body."
	if cs.Summary != want {
		t.Errorf("summary = %q, want %q", cs.Summary, want)
	}
}

func TestParseEmpty(t *testing.T) {
	content := "---\n---\n\nforce a release\n"
	cs, err := Parse(content, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Releases) != 0 {
		t.Fatalf("releases = %d, want 0", len(cs.Releases))
	}
	if cs.Summary != "force a release\n" {
		t.Errorf("summary = %q", cs.Summary)
	}
}

func TestParseCRLF(t *testing.T) {
	content := "---\r\n\"Pkg\": patch\r\n---\r\n\r\nfix it"
	cs, err := Parse(content, "crlf")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Releases) != 1 || cs.Releases[0].Bump != BumpPatch {
		t.Fatalf("releases = %+v", cs.Releases)
	}
	if cs.Summary != "fix it" {
		t.Errorf("summary = %q", cs.Summary)
	}
}

// TestParseSummaryNoBlankSeparator pins the fix where a summary beginning on the
// line IMMEDIATELY after the closing `---` (no blank separator) was dropped to
// "". The canonical form (with a blank line) and an empty changeset are asserted
// alongside to document intent.
func TestParseSummaryNoBlankSeparator(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "no blank separator",
			content: "---\n\"Pkg\": patch\n---\nfix it",
			want:    "fix it",
		},
		{
			name:    "canonical blank separator",
			content: "---\n\"Pkg\": patch\n---\n\nfix it",
			want:    "fix it",
		},
		{
			name:    "empty changeset",
			content: "---\n---\n",
			want:    "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cs, err := Parse(c.content, "x")
			if err != nil {
				t.Fatal(err)
			}
			if cs.Summary != c.want {
				t.Errorf("summary = %q, want %q", cs.Summary, c.want)
			}
		})
	}
}

func TestRenderRoundTrip(t *testing.T) {
	releases := []Release{{"PackageA", BumpMajor}, {"PackageB", BumpMinor}}
	out := Render(releases, "a change", "", false)
	cs, err := Parse(out, "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Releases) != 2 || cs.Releases[0].Bump != BumpMajor || cs.Releases[1].Bump != BumpMinor {
		t.Errorf("round-trip releases = %+v", cs.Releases)
	}
	if cs.Summary != "a change" {
		t.Errorf("round-trip summary = %q", cs.Summary)
	}
}

func TestTypeDrivenChangeset(t *testing.T) {
	// type: feat with bumpless package lines (the type-driven form).
	out := Render([]Release{{"Core", BumpNone}}, "add a feature", "feat", false)
	cs, err := Parse(out, "x")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Type != "feat" || cs.Breaking {
		t.Errorf("type = %q breaking = %v", cs.Type, cs.Breaking)
	}
	if len(cs.Releases) != 1 || cs.Releases[0].Bump != BumpNone {
		t.Errorf("releases = %+v (want one bumpless)", cs.Releases)
	}
	typ, breaking, ok := cs.EffectiveType()
	if !ok || typ != "feat" || breaking {
		t.Errorf("EffectiveType = %q %v %v", typ, breaking, ok)
	}
}

func TestBreakingType(t *testing.T) {
	cs, err := Parse("---\ntype: feat!\n\"Core\"\n---\n\ndrop legacy API", "x")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Type != "feat" || !cs.Breaking {
		t.Errorf("type=%q breaking=%v", cs.Type, cs.Breaking)
	}
}

func TestConventionalFromSummary(t *testing.T) {
	cs, err := Parse("---\n\"Core\"\n---\n\nfix!: correct the thing", "x")
	if err != nil {
		t.Fatal(err)
	}
	typ, breaking, ok := cs.EffectiveType()
	if !ok || typ != "fix" || !breaking {
		t.Errorf("EffectiveType from summary = %q %v %v", typ, breaking, ok)
	}
}

func TestBumpMax(t *testing.T) {
	if BumpPatch.Max(BumpMajor) != BumpMajor {
		t.Error("patch.Max(major) should be major")
	}
	if BumpMinor.Max(BumpNone) != BumpMinor {
		t.Error("minor.Max(none) should be minor")
	}
}

func TestParseConventionalScope(t *testing.T) {
	cases := []struct {
		in           string
		typ, scope   string
		breaking, ok bool
	}{
		{"feat(rig): a thing", "feat", "rig", false, true},
		{"fix(clauderig)!: a break", "fix", "clauderig", true, true},
		{"feat: unscoped", "feat", "", false, true},
		{"feat!: unscoped break", "feat", "", true, true},
		{"no prefix at all", "", "", false, false},
		{"feat(rig): first\nsecond line", "feat", "rig", false, true},
	}
	for _, c := range cases {
		typ, scope, breaking, ok := ParseConventionalScope(c.in)
		if typ != c.typ || scope != c.scope || breaking != c.breaking || ok != c.ok {
			t.Errorf("ParseConventionalScope(%q) = (%q,%q,%v,%v), want (%q,%q,%v,%v)",
				c.in, typ, scope, breaking, ok, c.typ, c.scope, c.breaking, c.ok)
		}
	}
}

func TestStripConventional(t *testing.T) {
	cases := [][2]string{
		{"feat(rig): a thing", "a thing"},
		{"fix!: a thing", "a thing"},
		{"no prefix at all", "no prefix at all"},
		{"feat(rig): first\n\nsecond para", "first\n\nsecond para"},
	}
	for _, c := range cases {
		if got := StripConventional(c[0]); got != c[1] {
			t.Errorf("StripConventional(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// An explicit `scope:` line beats one parsed from the summary, matching how
// `type:` already behaves.
func TestEffectiveScope(t *testing.T) {
	if got := (&Changeset{Summary: "feat(rig): x"}).EffectiveScope(); got != "rig" {
		t.Errorf("from summary = %q", got)
	}
	if got := (&Changeset{Scope: "clauderig", Summary: "feat(rig): x"}).EffectiveScope(); got != "clauderig" {
		t.Errorf("explicit should win, got %q", got)
	}
	if got := (&Changeset{Summary: "plain"}).EffectiveScope(); got != "" {
		t.Errorf("unscoped = %q", got)
	}
}

// The prefix has to be lifted out of the summary at parse time, not at render
// time: `version` prepends a commit or PR reference before anything renders,
// and a prefix is only recognisable while it is still at the start of the line.
func TestParseLiftsTheConventionalPrefix(t *testing.T) {
	cs, err := Parse("---\n\"pkg\": minor\n---\n\nfeat(rig): add a thing\n", "id")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Type != "feat" || cs.Scope != "rig" {
		t.Fatalf("type/scope = %q/%q, want feat/rig", cs.Type, cs.Scope)
	}
	if strings.TrimSpace(cs.Summary) != "add a thing" {
		t.Fatalf("summary = %q, want the prefix removed", cs.Summary)
	}
	// Decoration prepended afterwards must not resurrect it.
	cs.Summary = "abc1234: " + strings.TrimSpace(cs.Summary)
	if got := (&Changeset{Summary: cs.Summary}).EffectiveScope(); got == "rig" {
		t.Fatal("a decorated summary should carry no parseable scope of its own")
	}
	if cs.EffectiveScope() != "rig" {
		t.Fatal("the scope should still come from the field")
	}
}

// Explicit frontmatter still wins over the prefix.
func TestParseKeepsExplicitTypeAndScope(t *testing.T) {
	cs, err := Parse("---\ntype: fix\nscope: clauderig\n\"pkg\": minor\n---\n\nfeat(rig): x\n", "id")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Type != "fix" || cs.Scope != "clauderig" {
		t.Fatalf("type/scope = %q/%q, want fix/clauderig", cs.Type, cs.Scope)
	}
}

// Frontmatter is YAML to @changesets, so a package name may be single-quoted
// (with ” for a quote), double-quoted or plain, a bump may be quoted, and
// blank lines and comments are allowed.
func TestParseYAMLShapedReleaseLines(t *testing.T) {
	for _, tc := range []struct {
		line, name string
		bump       Bump
	}{
		{`'@jcamp/rig': patch`, "@jcamp/rig", BumpPatch},
		{`"@jcamp/rig": patch`, "@jcamp/rig", BumpPatch},
		{`lib: minor`, "lib", BumpMinor},
		{`"lib": "major"`, "lib", BumpMajor},
		{`'lib': 'patch'`, "lib", BumpPatch},
		{`'it''s': patch`, "it's", BumpPatch},
		{`"lib": patch # a comment`, "lib", BumpPatch},
		{`  'lib'  :  minor  `, "lib", BumpMinor},
		{`'lib'`, "lib", BumpNone},
		{`lib`, "lib", BumpNone},
		{`github.com/acme/mod: patch`, "github.com/acme/mod", BumpPatch},
		{`lib # a note`, "lib", BumpNone},
		{`'lib' # a note`, "lib", BumpNone},
		{"lib: patch\t# a tab, then a note", "lib", BumpPatch},
		{"'lib':\tpatch", "lib", BumpPatch},
		{`"a#b": patch`, "a#b", BumpPatch},
		{`lib:`, "lib", BumpNone},
		{"lib:\tpatch", "lib", BumpPatch},
	} {
		cs, err := Parse("---\n# which packages\n\n"+tc.line+"\n---\n\nA change\n", "x")
		if err != nil {
			t.Errorf("%q: %v", tc.line, err)
			continue
		}
		if len(cs.Releases) != 1 || cs.Releases[0].Name != tc.name || cs.Releases[0].Bump != tc.bump {
			t.Errorf("%q: releases = %+v, want %s %v", tc.line, cs.Releases, tc.name, tc.bump)
		}
	}
	for _, line := range []string{`'unclosed: patch`, `"lib": patch extra`, `@scope/lib: patch`, `"lib" patch`, `'': patch`, `"lib": 'patch"`, `lib:patch`, `"lib":patch`, `lib: ""`, `'lib': ''`} {
		if _, err := Parse("---\n"+line+"\n---\n\nA change\n", "x"); err == nil {
			t.Errorf("%q parsed; want it refused", line)
		}
	}
}

// TestDirLenient_KeepsGoingPastBadFiles: DirLenient returns the changesets that
// parse and every file that doesn't, by path; Dir still fails, on the first.
func TestDirLenient_KeepsGoingPastBadFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a-bad.md":  "---\nlib: \"\"\n---\n\nbad one",
		"b-good.md": "---\n\"pkg\": patch\n---\n\ngood",
		"c-bad.md":  "---\n\"pkg\": sideways\n---\n\nbad two",
		"README.md": "not a changeset",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	css, bad, err := DirLenient(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(css) != 1 || css[0].ID != "b-good" {
		t.Fatalf("parsed = %+v, want just b-good", css)
	}
	if len(bad) != 2 || filepath.Base(bad[0].Path) != "a-bad.md" || filepath.Base(bad[1].Path) != "c-bad.md" {
		t.Fatalf("bad = %v, want a-bad.md then c-bad.md", bad)
	}
	if !strings.Contains(bad[0].Error(), "a-bad.md: ") {
		t.Errorf("FileError %q should lead with its path", bad[0].Error())
	}

	_, derr := Dir(dir, "")
	if derr == nil || derr.Error() != bad[0].Err.Error() {
		t.Fatalf("Dir err = %v, want the first file's error %v", derr, bad[0].Err)
	}
	if _, _, err := DirLenient(filepath.Join(dir, "missing"), ""); !os.IsNotExist(err) {
		t.Fatalf("missing dir: err = %v, want not-exist", err)
	}
}
