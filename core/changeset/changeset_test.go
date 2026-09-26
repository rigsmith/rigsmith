package changeset

import (
	"os"
	"path/filepath"
	"reflect"
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
		{"lib:\tpatch", "lib", BumpPatch},
	} {
		// The type lets a bare line (no bump) take one; an explicit bump wins.
		cs, err := Parse("---\n# which packages\ntype: fix\n\n"+tc.line+"\n---\n\nA change\n", "x")
		if err != nil {
			t.Errorf("%q: %v", tc.line, err)
			continue
		}
		if len(cs.Releases) != 1 || cs.Releases[0].Name != tc.name || cs.Releases[0].Bump != tc.bump {
			t.Errorf("%q: releases = %+v, want %s %v", tc.line, cs.Releases, tc.name, tc.bump)
		}
	}
	for _, line := range []string{`'unclosed: patch`, `"lib": patch extra`, `@scope/lib: patch`, `"lib" patch`, `'': patch`, `"lib": 'patch"`, `lib:patch`, `"lib":patch`, `lib: ""`, `'lib': ''`, `lib:`} {
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

// Where @changesets' YAML parser refuses a frontmatter, so does Parse: a
// repeated package, a colon with no bump (YAML's null), and a tab in the
// indentation. Each error names the changeset and says what is wrong.
func TestParseRefusesWhatCanonRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, frontmatter, want string
	}{
		{"repeated package", "lib: patch\nlib: minor", `"lib" is listed more than once in the frontmatter; keep one line for it (quoted or not, it is the same package)`},
		{"repeated with the same bump", "lib: patch\nlib: patch", `"lib" is listed more than once in the frontmatter; keep one line for it (quoted or not, it is the same package)`},
		{"repeated, quoted one way then another", "'lib': patch\n\"lib\": minor", `"lib" is listed more than once in the frontmatter; keep one line for it (quoted or not, it is the same package)`},
		{"repeated, plain then quoted", "lib: patch\n\"lib\"", `"lib" is listed more than once in the frontmatter; keep one line for it (quoted or not, it is the same package)`},
		{"repeated among others", "a: patch\nlib: minor\nb: patch\nlib: major", `"lib" is listed more than once in the frontmatter; keep one line for it (quoted or not, it is the same package)`},
		{"repeated, one spelled with an escape", `"l\u0069b": patch` + "\nlib: minor", `"lib" is listed more than once in the frontmatter; keep one line for it (quoted or not, it is the same package)`},
		{"an escape YAML doesn't have", `"l\qb": patch`, "malformed frontmatter line"},
		{"a \\u escape cut short", `"l\u006": patch`, "malformed frontmatter line"},
		{"colon, no bump", "lib:", `"lib" has a colon but no bump`},
		{"quoted, colon, no bump", `"@acme/lib":`, `"@acme/lib" has a colon but no bump`},
		{"single-quoted, colon, no bump", `'lib':`, `"lib" has a colon but no bump`},
		{"colon, spaces, no bump", "lib:   ", `"lib" has a colon but no bump`},
		{"colon, tab, no bump", "\"lib\":\t", `"lib" has a colon but no bump`},
		{"colon, then only a comment", "lib: # later", `"lib" has a colon but no bump`},
		{"colon, no bump, under a type", "type: feat\nlib:", `"lib" has a colon but no bump`},
		{"tab-indented package", "\tlib: patch", "indented with a tab"},
		{"space then tab", " \tlib: patch", "indented with a tab"},
		{"tab-indented second package", "a: patch\n\tlib: patch", "indented with a tab"},
		{"tab-indented bare package", "type: fix\n\t\"lib\"", "indented with a tab"},
		{"tab-indented type", "\ttype: fix\n\"lib\"", "indented with a tab"},
		{"bare package, no type", `"lib"`, `"lib" has no bump`},
		{"bare plain package, no type", "lib", `"lib" has no bump`},
		{"bare package with a comment, no type", "'lib' # a note", `"lib" has no bump`},
		{"bare package, a scope but no type", "scope: rig\n\"lib\"", `"lib" has no bump`},
		{"bare package beside a bumped one", "a: patch\n\"lib\"", `"lib" has no bump`},
		{"bare package after a bumped one of the same name", "lib: patch\n\"lib\"", `"lib" is listed more than once in the frontmatter; keep one line for it (quoted or not, it is the same package)`},
	} {
		_, err := Parse("---\n"+tc.frontmatter+"\n---\n\nA change\n", "neg")
		if err == nil {
			t.Errorf("%s: %q parsed; want it refused", tc.name, tc.frontmatter)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `changeset "neg"`) {
			t.Errorf("%s: error = %q, want it to name the changeset and say %q", tc.name, err, tc.want)
		}
	}
}

// What canon accepts still parses, including the forms #494 opened up and the
// whitespace YAML allows: a tab after the colon or before a comment, a
// tab-only blank line, a tab-indented comment line, and a mapping indented
// with spaces throughout.
func TestParseStillAcceptsCanonForms(t *testing.T) {
	for _, tc := range []struct {
		name, frontmatter string
		want              []Release
	}{
		{"none is a bump", "lib: none", []Release{{"lib", BumpNone}}},
		{"double-quoted escapes decode", `"l\u0069b": patch` + "\n" + `"\x40acme/\U00000078": minor`, []Release{{"lib", BumpPatch}, {"@acme/x", BumpMinor}}},
		{"an escaped quote stays in the name", `"a\"b": patch`, []Release{{`a"b`, BumpPatch}}},
		{"quoted none", `'lib': "none"`, []Release{{"lib", BumpNone}}},
		{"#494 forms together", "# a comment line\n'@x/y': patch\n\nlib: 'minor'   # trailing comment\n'it''s': \"patch\"",
			[]Release{{"@x/y", BumpPatch}, {"lib", BumpMinor}, {"it's", BumpPatch}}},
		{"bare name, no colon, derives from the type", "type: feat\n\"lib\"", []Release{{"lib", BumpNone}}},
		{"bare name with a comment", "type: feat\nlib # a note", []Release{{"lib", BumpNone}}},
		{"bare name under a type written after it", "\"lib\"\ntype: fix", []Release{{"lib", BumpNone}}},
		{"bare name beside a bumped one, under a type", "type: fix\na: major\n\"lib\"", []Release{{"a", BumpMajor}, {"lib", BumpNone}}},
		{"tab after the colon", "lib:\tpatch", []Release{{"lib", BumpPatch}}},
		{"tab before a trailing comment", "lib: patch\t# note", []Release{{"lib", BumpPatch}}},
		{"tab-only blank line", "a: patch\n\t\nlib: minor", []Release{{"a", BumpPatch}, {"lib", BumpMinor}}},
		{"tab-indented comment line", "a: patch\n\t# note\nlib: minor", []Release{{"a", BumpPatch}, {"lib", BumpMinor}}},
		{"space-indented throughout", "  a: patch\n  lib: minor", []Release{{"a", BumpPatch}, {"lib", BumpMinor}}},
		{"distinct packages", "a: patch\nlib: minor\n'@acme/lib': major", []Release{{"a", BumpPatch}, {"lib", BumpMinor}, {"@acme/lib", BumpMajor}}},
	} {
		cs, err := Parse("---\n"+tc.frontmatter+"\n---\n\nA change\n", "x")
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if !reflect.DeepEqual(cs.Releases, tc.want) {
			t.Errorf("%s: releases = %+v, want %+v", tc.name, cs.Releases, tc.want)
		}
	}
}

// A bare line takes its bump from the changeset's type, which may come from the
// summary's conventional prefix rather than a `type:` line.
func TestParseBareNameTypedBySummary(t *testing.T) {
	cs, err := Parse("---\n\"lib\"\n---\n\nfeat: a thing\n", "x")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Type != "feat" || len(cs.Releases) != 1 || cs.Releases[0] != (Release{"lib", BumpNone}) {
		t.Errorf("type = %q, releases = %+v; want feat and a bare lib", cs.Type, cs.Releases)
	}
}

// Render writes a bumpless release bare only when there is a type for it to
// take its bump from; otherwise it writes `: none`, which Parse reads back.
func TestRenderBumplessRelease(t *testing.T) {
	for _, tc := range []struct {
		name, typ, summary, wantLine string
	}{
		{"typed", "fix", "a change", "\"lib\"\n"},
		{"typed by the summary", "", "fix: a change", "\"lib\"\n"},
		{"untyped", "", "a change", "\"lib\": none\n"},
	} {
		out := Render([]Release{{"lib", BumpNone}}, tc.summary, tc.typ, false)
		if !strings.Contains(out, "\n"+tc.wantLine) {
			t.Errorf("%s: rendered %q, want the line %q", tc.name, out, tc.wantLine)
		}
		cs, err := Parse(out, "x")
		if err != nil {
			t.Errorf("%s: rendered changeset does not parse: %v", tc.name, err)
			continue
		}
		if len(cs.Releases) != 1 || cs.Releases[0] != (Release{"lib", BumpNone}) {
			t.Errorf("%s: releases = %+v", tc.name, cs.Releases)
		}
	}
}
