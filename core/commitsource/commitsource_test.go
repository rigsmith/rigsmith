package commitsource

import (
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/changeset"
	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/gitutil"
	"github.com/rigsmith/rigsmith/core/plugin"
)

var root = filepath.FromSlash("/repo")

func pkgs() []plugin.Package {
	return []plugin.Package{
		{Name: "pkg-a", Dir: filepath.FromSlash("packages/pkg-a")},
		{Name: "pkg-b", Dir: filepath.FromSlash("packages/pkg-b")},
		// A nested package inside pkg-a, to exercise deepest-wins attribution.
		{Name: "pkg-a-inner", Dir: filepath.FromSlash("packages/pkg-a/inner")},
	}
}

func abs(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func TestSynthesizeAttributesByPathAndStripsPrefix(t *testing.T) {
	commits := []gitutil.Commit{
		{Hash: "aaaaaaaaaa", Subject: "feat(core): add thing", Files: []string{abs("packages/pkg-a/main.go")}},
	}
	got := Synthesize(commits, pkgs(), root, config.Default())
	if len(got) != 1 {
		t.Fatalf("got %d changesets, want 1", len(got))
	}
	cs := got[0]
	if cs.Type != "feat" || cs.Breaking {
		t.Errorf("type/breaking = %q/%v, want feat/false", cs.Type, cs.Breaking)
	}
	if cs.Summary != "add thing" {
		t.Errorf("summary = %q, want %q (prefix stripped)", cs.Summary, "add thing")
	}
	if names := cs.ChangedNames(); len(names) != 1 || names[0] != "pkg-a" {
		t.Errorf("names = %v, want [pkg-a]", names)
	}
	if cs.Releases[0].Bump != changeset.BumpNone {
		t.Errorf("bump = %v, want none (derived from type)", cs.Releases[0].Bump)
	}
	// The full source SHA is retained (for changelog provenance); the ID is the
	// 7-char abbreviation.
	if cs.Commit != "aaaaaaaaaa" {
		t.Errorf("Commit = %q, want full hash", cs.Commit)
	}
	if cs.ID != "aaaaaaa" {
		t.Errorf("ID = %q, want 7-char short hash", cs.ID)
	}
}

func TestSynthesizeSkipsNonConventional(t *testing.T) {
	commits := []gitutil.Commit{
		{Hash: "b", Subject: "Merge branch 'main'", Files: []string{abs("packages/pkg-a/x.go")}},
		{Hash: "c", Subject: "wip random", Files: []string{abs("packages/pkg-a/x.go")}},
	}
	if got := Synthesize(commits, pkgs(), root, config.Default()); len(got) != 0 {
		t.Errorf("got %d changesets, want 0 (non-conventional)", len(got))
	}
}

func TestSynthesizeSkipsUnrecognizedType(t *testing.T) {
	// headerRe parses any `word:` prefix, so these look conventional with
	// `rig`/`bump`/`merge` as the type — but none is a recognized conventional
	// type, so they must be dropped rather than slipping in as patch releases.
	commits := []gitutil.Commit{
		{Hash: "a1", Subject: "rig: live dashboard", Files: []string{abs("packages/pkg-a/x.go")}},
		{Hash: "a2", Subject: "Bump: deps", Files: []string{abs("packages/pkg-a/x.go")}},
		{Hash: "a3", Subject: "changerig: add flag", Files: []string{abs("packages/pkg-a/x.go")}},
	}
	if got := Synthesize(commits, pkgs(), root, config.Default()); len(got) != 0 {
		t.Errorf("got %d changesets, want 0 (unrecognized types)", len(got))
	}
}

func TestSynthesizeKeepsCanonicalTypesWithoutGroup(t *testing.T) {
	// ci/style/revert have no default changelog group but are recognized
	// conventional types — they should still produce releases.
	for _, typ := range []string{"ci", "style", "revert"} {
		commits := []gitutil.Commit{
			{Hash: "z", Subject: typ + ": something", Files: []string{abs("packages/pkg-a/x.go")}},
		}
		got := Synthesize(commits, pkgs(), root, config.Default())
		if len(got) != 1 {
			t.Errorf("type %q: got %d changesets, want 1", typ, len(got))
			continue
		}
		if got[0].Type != typ {
			t.Errorf("type %q: cs.Type = %q", typ, got[0].Type)
		}
	}
}

func TestSynthesizeRecognizesConfiguredGroupType(t *testing.T) {
	// A custom changelog group makes its type recognized even though it isn't a
	// canonical conventional type.
	cfg := config.Default()
	cfg.ChangelogGroups = append(config.DefaultChangelogGroups,
		config.ChangelogGroup{Type: "deps", Section: "Dependencies", Bump: "patch"})
	commits := []gitutil.Commit{
		{Hash: "d1", Subject: "deps: bump x", Files: []string{abs("packages/pkg-a/x.go")}},
	}
	if got := Synthesize(commits, pkgs(), root, cfg); len(got) != 1 {
		t.Errorf("got %d changesets, want 1 (configured group type recognized)", len(got))
	}
}

func TestSynthesizeSkipsCommitUnderNoPackage(t *testing.T) {
	commits := []gitutil.Commit{
		{Hash: "d", Subject: "docs: readme", Files: []string{abs("README.md"), abs(".github/ci.yml")}},
	}
	if got := Synthesize(commits, pkgs(), root, config.Default()); len(got) != 0 {
		t.Errorf("got %d changesets, want 0 (no package owns the files)", len(got))
	}
}

func TestSynthesizeBreaking(t *testing.T) {
	commits := []gitutil.Commit{
		{Hash: "e", Subject: "feat!: bang", Files: []string{abs("packages/pkg-a/x.go")}},
		{Hash: "f", Subject: "fix: footer", Body: "details\n\nBREAKING CHANGE: removed Y", Files: []string{abs("packages/pkg-b/x.go")}},
	}
	got := Synthesize(commits, pkgs(), root, config.Default())
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if !got[0].Breaking {
		t.Error("`!` subject should be breaking")
	}
	// A `!`-only breaking change has no separate footer note — bullet stays the subject.
	if got[0].Summary != "bang" {
		t.Errorf("got[0].Summary = %q, want %q", got[0].Summary, "bang")
	}
	if !got[1].Breaking {
		t.Error("BREAKING CHANGE footer should be breaking")
	}
	// changelogen-style: the footer description becomes a continuation line.
	if want := "footer\nremoved Y"; got[1].Summary != want {
		t.Errorf("got[1].Summary = %q, want %q", got[1].Summary, want)
	}
}

func TestBreakingNoteMultilineUntilBlank(t *testing.T) {
	commits := []gitutil.Commit{
		{Hash: "m", Subject: "feat: x", Files: []string{abs("packages/pkg-a/x.go")},
			Body: "BREAKING CHANGE: line one\ncontinues here\n\nReviewed-by: someone"},
	}
	got := Synthesize(commits, pkgs(), root, config.Default())
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// Continuation lines join with a space; the trailing footer after the blank
	// line is excluded.
	if want := "x\nline one continues here"; got[0].Summary != want {
		t.Errorf("Summary = %q, want %q", got[0].Summary, want)
	}
}

func TestSynthesizeMultiPackageAndDeepestWins(t *testing.T) {
	commits := []gitutil.Commit{
		{Hash: "g", Subject: "fix: spread", Files: []string{
			abs("packages/pkg-a/x.go"),       // pkg-a
			abs("packages/pkg-a/inner/y.go"), // deepest: pkg-a-inner, not pkg-a
			abs("packages/pkg-b/z.go"),       // pkg-b
		}},
	}
	got := Synthesize(commits, pkgs(), root, config.Default())
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	names := got[0].ChangedNames()
	want := map[string]bool{"pkg-a": true, "pkg-a-inner": true, "pkg-b": true}
	if len(names) != 3 {
		t.Fatalf("names = %v, want 3", names)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected name %q", n)
		}
	}
}

func TestSynthesizeScopeWins(t *testing.T) {
	cfg := config.Default()
	cfg.Versioning.Scopes = map[string]string{"b": "pkg-b"}
	commits := []gitutil.Commit{
		// Files live in pkg-a, but the scope maps to pkg-b → scope wins.
		{Hash: "h", Subject: "feat(b): scoped", Files: []string{abs("packages/pkg-a/x.go")}},
	}
	got := Synthesize(commits, pkgs(), root, cfg)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if names := got[0].ChangedNames(); len(names) != 1 || names[0] != "pkg-b" {
		t.Errorf("names = %v, want [pkg-b] (scope wins)", names)
	}
}

func TestSynthesizeUnknownScopeFallsBackToPath(t *testing.T) {
	cfg := config.Default()
	cfg.Versioning.Scopes = map[string]string{"b": "pkg-b"}
	commits := []gitutil.Commit{
		// scope "z" is not mapped → fall back to path attribution (pkg-a).
		{Hash: "i", Subject: "feat(z): unmapped", Files: []string{abs("packages/pkg-a/x.go")}},
	}
	got := Synthesize(commits, pkgs(), root, cfg)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if names := got[0].ChangedNames(); len(names) != 1 || names[0] != "pkg-a" {
		t.Errorf("names = %v, want [pkg-a] (path fallback)", names)
	}
}
