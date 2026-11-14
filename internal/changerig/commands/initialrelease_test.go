package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// initialWorkspace is Zoo UI's shape: a pnpm workspace whose two packages sit
// at 0.1.0, set up by hand and never published, the theme depending on the
// core through workspace:*. No changesets. It's a git repo with one commit.
func initialWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFileAt(t, filepath.Join(root, ".changeset", "config.json"), `{"baseBranch": "main"}`)
	writeFileAt(t, filepath.Join(root, "package.json"), `{"name":"root","private":true}`)
	writeFileAt(t, filepath.Join(root, "pnpm-workspace.yaml"), "packages:\n  - 'packages/*'\n")
	writeFileAt(t, filepath.Join(root, "packages", "core", "package.json"), "{\n  \"name\": \"@acme/core\",\n  \"version\": \"0.1.0\"\n}\n")
	writeFileAt(t, filepath.Join(root, "packages", "theme", "package.json"), "{\n  \"name\": \"@acme/theme\",\n  \"version\": \"0.1.0\",\n  \"dependencies\": {\n    \"@acme/core\": \"workspace:*\"\n  }\n}\n")
	gitCmd(t, root, "init", "-q", "-b", "main")
	gitCmd(t, root, "add", "-A")
	gitCmd(t, root, "commit", "-q", "-m", "init")
	t.Chdir(root)
	return root
}

// stubReleasedAt stands in for the tag and registry lookup.
func stubReleasedAt(t *testing.T, where map[string]string) {
	t.Helper()
	was := releasedAt
	releasedAt = func(_ context.Context, _ *Workspace, _ []plugin.Package, _ map[string]string, p plugin.Package) (string, error) {
		return where[p.Name], nil
	}
	t.Cleanup(func() { releasedAt = was })
}

func runVersionErr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := NewVersionCmd()
	cmd.SetContext(context.Background())
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"--yes"}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

// The first release of versions already on disk: each package gets a 0.1.0
// changelog entry, with no changeset in sight, and nothing is bumped — not
// the packages, not the theme's workspace:* range.
func TestVersionReleasesAnUnpublishedCurrentVersion(t *testing.T) {
	root := initialWorkspace(t)
	stubReleasedAt(t, nil)
	out := runVersion(t, "--release-as", "@acme/core=0.1.0", "--release-as", "@acme/theme=0.1.0")
	if !strings.Contains(out, "initial release @acme/core 0.1.0") || !strings.Contains(out, "initial release @acme/theme 0.1.0") {
		t.Errorf("output does not name the initial releases:\n%s", out)
	}
	for _, dir := range []string{"core", "theme"} {
		log := mustRead(t, filepath.Join(root, "packages", dir, "CHANGELOG.md"))
		if !strings.Contains(log, "## 0.1.0\n") || !strings.Contains(log, "### Minor Changes") || !strings.Contains(log, "- Initial release") {
			t.Errorf("%s changelog:\n%s", dir, log)
		}
		if strings.Contains(log, "0.1.1") || strings.Contains(log, "0.2.0") {
			t.Errorf("%s changelog names a bumped version:\n%s", dir, log)
		}
	}
	if got := mustRead(t, filepath.Join(root, "packages", "core", "package.json")); !strings.Contains(got, `"version": "0.1.0"`) {
		t.Errorf("core manifest:\n%s", got)
	}
	theme := mustRead(t, filepath.Join(root, "packages", "theme", "package.json"))
	if !strings.Contains(theme, `"version": "0.1.0"`) || !strings.Contains(theme, `"@acme/core": "workspace:*"`) {
		t.Errorf("theme manifest:\n%s", theme)
	}
}

// --initial-message words the entry; only the named package is released.
func TestVersionInitialReleaseTakesAMessage(t *testing.T) {
	root := initialWorkspace(t)
	stubReleasedAt(t, nil)
	runVersion(t, "--release-as", "@acme/core=0.1.0", "--initial-message", "First public release")
	if log := mustRead(t, filepath.Join(root, "packages", "core", "CHANGELOG.md")); !strings.Contains(log, "- First public release") {
		t.Errorf("core changelog:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", "theme", "CHANGELOG.md")); !os.IsNotExist(err) {
		t.Error("the theme was released without being named")
	}
}

// Pending changesets are the entry's notes when there are some: consumed as
// ever, the version still not bumped.
func TestVersionInitialReleaseUsesPendingChangesets(t *testing.T) {
	root := initialWorkspace(t)
	writeFileAt(t, filepath.Join(root, ".changeset", "core-buttons.md"), "---\n\"@acme/core\": minor\n---\n\nButtons and inputs\n")
	stubReleasedAt(t, nil)
	runVersion(t, "--release-as", "@acme/core=0.1.0")
	log := mustRead(t, filepath.Join(root, "packages", "core", "CHANGELOG.md"))
	if !strings.Contains(log, "## 0.1.0\n") || !strings.Contains(log, "Buttons and inputs") || strings.Contains(log, "Initial release") {
		t.Errorf("core changelog:\n%s", log)
	}
	if got := mustRead(t, filepath.Join(root, "packages", "core", "package.json")); !strings.Contains(got, `"version": "0.1.0"`) {
		t.Errorf("core was bumped:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".changeset", "core-buttons.md")); !os.IsNotExist(err) {
		t.Error("the changeset was not consumed")
	}
}

// A version already on the registry is not a first release: refused, with
// nothing written.
func TestVersionRefusesAnInitialReleaseOfAPublishedVersion(t *testing.T) {
	root := initialWorkspace(t)
	stubReleasedAt(t, map[string]string{"@acme/core": "on its registry"})
	out, err := runVersionErr(t, "--release-as", "@acme/core=0.1.0")
	if err == nil || !strings.Contains(err.Error(), "already released (on its registry)") {
		t.Fatalf("err = %v, want the already-released refusal\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", "core", "CHANGELOG.md")); !os.IsNotExist(err) {
		t.Error("a changelog was written")
	}
}

// ...and neither is one with a tag. The real lookup: the tag answers before
// any registry is asked.
func TestVersionRefusesAnInitialReleaseOfATaggedVersion(t *testing.T) {
	root := initialWorkspace(t)
	gitCmd(t, root, "tag", "@acme/core@0.1.0")
	_, err := runVersionErr(t, "--release-as", "@acme/core=0.1.0")
	if err == nil || !strings.Contains(err.Error(), "already released (tag @acme/core@0.1.0)") {
		t.Fatalf("err = %v, want the already-released refusal naming the tag", err)
	}
}

// Below the current version is still refused, and --initial-message is
// refused without an initial release to word.
func TestVersionInitialReleaseRefusals(t *testing.T) {
	initialWorkspace(t)
	stubReleasedAt(t, nil)
	if _, err := runVersionErr(t, "--release-as", "@acme/core=0.0.9"); err == nil {
		t.Error("--release-as below the current version succeeded")
	}
	if _, err := runVersionErr(t, "--release-as", "@acme/core=0.2.0", "--initial-message", "hi"); err == nil || !strings.Contains(err.Error(), "--initial-message") {
		t.Errorf("err = %v, want --initial-message refused without an initial release", err)
	}
}
