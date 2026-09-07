package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedManifest() *stackManifest {
	return &stackManifest{Repos: map[string]*stackRepo{
		"porta-pty": {Upstream: "github.com/tomlm/Porta.Pty", Fork: "github.com/JohnCampionJr/Porta.Pty"},
		"xterm-net": {Upstream: "github.com/tomlm/XTerm.NET", Fork: "github.com/JohnCampionJr/XTerm.NET"},
	}}
}

// The two things a hand-written README leaves out are the two that bite: the
// members are absent on purpose, and work leaves through propose rather than a
// push. Everything else is derivable, which is the argument for generating it.
func TestStackReadmeSaysWhatIsNotDerivable(t *testing.T) {
	root := t.TempDir()
	if wrote, err := writeStackReadme(root, seedManifest()); err != nil || !wrote {
		t.Fatalf("writeStackReadme = %v, %v", wrote, err)
	}
	body, err := os.ReadFile(stackReadmePath(root))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	for _, want := range []string{
		"not in this repository", // why the directories are missing
		"Do not commit the members",
		"Never `git push` from this workspace",
		"rig stack setup",   // the one command a clone needs
		"rig stack propose", // how work leaves
		"clean",             // the dirty-tree rule
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the README never says %q", want)
		}
	}

	// The member table comes from the manifest, so it cannot go stale the way a
	// hand-written one does.
	for _, want := range []string{"`porta-pty/`", "`xterm-net/`", "github.com/tomlm/XTerm.NET"} {
		if !strings.Contains(text, want) {
			t.Errorf("the member table is missing %q", want)
		}
	}
}

// The same rule the build overlay uses: rig rewrites what carries its marker and
// never touches what does not. Dropping the line is how you take it over.
func TestStackReadmeLeavesAHandWrittenOneAlone(t *testing.T) {
	root := t.TempDir()
	mine := "# My own words\n\nnothing generated here.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err := writeStackReadme(root, seedManifest())
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("overwrote a README that rig does not own")
	}
	got, _ := os.ReadFile(filepath.Join(root, "README.md"))
	if string(got) != mine {
		t.Errorf("the file changed:\n%s", got)
	}

	// And one it does own is refreshed, so the table follows the manifest.
	if err := os.WriteFile(filepath.Join(root, "README.md"),
		[]byte("# stale\n"+stackReadmeMarker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if wrote, err := writeStackReadme(root, seedManifest()); err != nil || !wrote {
		t.Fatalf("a marked README was not refreshed: %v, %v", wrote, err)
	}
}

// An ssh remote or a local path is not a URL; linking it produces something that
// does not resolve.
func TestStackReadmeLinksOnlyWhatResolves(t *testing.T) {
	if got := mdRepo("github.com/tomlm/XTerm.NET"); !strings.HasPrefix(got, "[") {
		t.Errorf("a normalised remote did not become a link: %q", got)
	}
	for _, s := range []string{"git@github.com:tomlm/XTerm.NET.git", "../local/checkout", ""} {
		if got := mdRepo(s); strings.HasPrefix(got, "[") {
			t.Errorf("%q was linked, and would not resolve: %q", s, got)
		}
	}
}
