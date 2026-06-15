package forge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestReleaseURLGitHubReturnsTrimmedURL(t *testing.T) {
	runner := &recordingRunner{responder: func(c recordedCall) (string, error) {
		switch {
		case c.name == "gh" && c.has("auth"): // Ready probe
			return "", nil
		case c.name == "gh" && c.has("view"):
			return "https://github.com/acme/acme/releases/tag/web@1.0.0\n", nil
		}
		return "", nil
	}}

	got := ReleaseURL(Selection{Forge: "github"}, "web@1.0.0", "/repo", runner.run)
	if got != "https://github.com/acme/acme/releases/tag/web@1.0.0" {
		t.Errorf("ReleaseURL = %q", got)
	}
}

func TestReleaseURLEmptyWhenForgeHasNoURLCommand(t *testing.T) {
	runner := &recordingRunner{responder: func(c recordedCall) (string, error) {
		return "", nil // glab auth ok, but ReleaseURLCmd is nil for GitLab
	}}

	if got := ReleaseURL(Selection{Forge: "gitlab"}, "web@1.0.0", "/repo", runner.run); got != "" {
		t.Errorf("ReleaseURL (gitlab) = %q, want empty", got)
	}
}

func TestReleaseURLEmptyWhenForgeDisabled(t *testing.T) {
	runner := &recordingRunner{}
	if got := ReleaseURL(Selection{Forge: "none"}, "web@1.0.0", "/repo", runner.run); got != "" {
		t.Errorf("ReleaseURL (none) = %q, want empty", got)
	}
}

func TestResolvedIssueNumbersSortedAndDeduped(t *testing.T) {
	got := ResolvedIssueNumbers([]string{
		"feat: thing (#42)",
		"fix: bug, fixes #7",
		"chore: more #42",
	})

	if len(got) != 2 || got[0] != 7 || got[1] != 42 {
		t.Errorf("ResolvedIssueNumbers = %v, want [7 42]", got)
	}
}

func TestNotesReadsChangelogSection(t *testing.T) {
	dir := t.TempDir()
	changelog := "# Changelog\n\n## 1.2.0\n\n- added a thing\n- fixed a bug\n\n## 1.1.0\n\n- old\n"
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Notes(plugin.Package{Name: "web", Version: "1.2.0", Dir: "."}, dir)
	if got != "- added a thing\n- fixed a bug" {
		t.Errorf("Notes = %q", got)
	}
}
