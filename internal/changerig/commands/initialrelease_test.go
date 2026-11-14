package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
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

// The reviewed case: A at 0.1.0 with a pending minor changeset, B depending
// on it through ^0.1.0. A's first release leaves A at 0.1.0, so B, whose
// range still holds it, is not released at all.
func TestVersionInitialReleaseCascadesNothing(t *testing.T) {
	root := initialWorkspace(t)
	writeFileAt(t, filepath.Join(root, "packages", "theme", "package.json"), "{\n  \"name\": \"@acme/theme\",\n  \"version\": \"0.1.0\",\n  \"dependencies\": {\n    \"@acme/core\": \"^0.1.0\"\n  }\n}\n")
	writeFileAt(t, filepath.Join(root, ".changeset", "core-buttons.md"), "---\n\"@acme/core\": minor\n---\n\nButtons and inputs\n")
	stubReleasedAt(t, nil)
	runVersion(t, "--release-as", "@acme/core=0.1.0")

	theme := mustRead(t, filepath.Join(root, "packages", "theme", "package.json"))
	if !strings.Contains(theme, `"version": "0.1.0"`) || !strings.Contains(theme, `"@acme/core": "^0.1.0"`) {
		t.Errorf("the theme was touched by the core's first release:\n%s", theme)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", "theme", "CHANGELOG.md")); !os.IsNotExist(err) {
		t.Error("the theme got a changelog entry, so it was released")
	}
	if log := mustRead(t, filepath.Join(root, "packages", "core", "CHANGELOG.md")); !strings.Contains(log, "## 0.1.0\n") {
		t.Errorf("core changelog:\n%s", log)
	}
}

// A remote that can't be asked isn't proof the tag is missing: the real
// lookup refuses, before any registry is asked.
func TestVersionInitialReleaseRefusesWhenTheRemoteCantBeChecked(t *testing.T) {
	root := initialWorkspace(t)
	gitCmd(t, root, "remote", "add", "origin", filepath.Join(t.TempDir(), "no-such-remote"))
	_, err := runVersionErr(t, "--release-as", "@acme/core=0.1.0")
	if err == nil || !strings.Contains(err.Error(), "couldn't check origin for tag @acme/core@0.1.0") {
		t.Fatalf("err = %v, want a refusal naming the remote it couldn't check", err)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", "core", "CHANGELOG.md")); !os.IsNotExist(err) {
		t.Error("a changelog was written")
	}
}

// The current version and a higher one for the same package clash: refused,
// where splitting the specs used to let the higher one win silently.
func TestVersionInitialReleaseRefusesConflictingSpecs(t *testing.T) {
	root := initialWorkspace(t)
	writeFileAt(t, filepath.Join(root, ".changeset", "core-buttons.md"), "---\n\"@acme/core\": minor\n---\n\nButtons and inputs\n")
	stubReleasedAt(t, nil)
	_, err := runVersionErr(t, "--release-as", "@acme/core=0.1.0", "--release-as", "@acme/core=0.3.0")
	if err == nil || !strings.Contains(err.Error(), "two versions, 0.1.0 (its first release) and 0.3.0") {
		t.Fatalf("err = %v, want the two-versions refusal", err)
	}
	if got := mustRead(t, filepath.Join(root, "packages", "core", "package.json")); !strings.Contains(got, `"version": "0.1.0"`) {
		t.Errorf("core was written:\n%s", got)
	}
}

// The registry lookup carries the ecosystem's configured credential, as
// publish-plan's does, for a private feed; one that can't be resolved is
// marked unavailable rather than dropped.
func TestInitialReleaseLookupCarriesTheConfiguredAuth(t *testing.T) {
	t.Setenv("ACME_NPM_TOKEN", "npm_secret")
	cfg, err := config.Parse([]byte(`{"node": {"auth": "env:ACME_NPM_TOKEN", "user": "ci"}}`))
	if err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: "/repo", Config: cfg}
	p := plugin.Package{Name: "@acme/core", Version: "0.1.0", Dir: "packages/core"}
	req := publishedRequest(context.Background(), ws, "node", p)
	if req.Auth == nil || req.Auth.Token != "npm_secret" || req.User != "ci" || req.AuthUnavailable {
		t.Errorf("request = %+v (auth %+v), want the resolved token and user", req, req.Auth)
	}

	t.Setenv("ACME_NPM_TOKEN", "")
	req = publishedRequest(context.Background(), ws, "node", p)
	if req.Auth != nil || !req.AuthUnavailable || !strings.Contains(req.AuthError, "env:ACME_NPM_TOKEN") {
		t.Errorf("unresolvable: request = %+v, want it marked unavailable with the reference named", req)
	}
}

// Packages sharing a version file move in lockstep: a first release of one is
// a first release of each, or publish would ship the others with no entry.
func TestInitialReleaseTakesInPackagesSharingTheVersionFile(t *testing.T) {
	pkgs := []plugin.Package{
		{Name: "Acme.Core", Version: "1.0.0", ManifestPath: "src/Core/Core.csproj", VersionFile: "Directory.Build.props"},
		{Name: "Acme.Web", Version: "1.0.0", ManifestPath: "src/Web/Web.csproj", VersionFile: "Directory.Build.props"},
		{Name: "Acme.Tool", Version: "1.0.0", ManifestPath: "tools/Tool.csproj"},
	}
	ecoOf := map[string]string{"Acme.Core": "dotnet", "Acme.Web": "dotnet", "Acme.Tool": "dotnet"}
	ws := &Workspace{Root: "/repo", Config: config.Default()}

	stubReleasedAt(t, nil)
	initial, rest, err := initialReleases(context.Background(), ws, pkgs, ecoOf, []string{"Acme.Core=1.0.0"}, false)
	if err != nil || len(rest) != 0 {
		t.Fatalf("initial %v, rest %v, err %v", initial, rest, err)
	}
	if initial["Acme.Core"] != "1.0.0" || initial["Acme.Web"] != "1.0.0" || initial["Acme.Tool"] != "" {
		t.Errorf("initial = %v, want Core and Web (sharing the version file), not Tool", initial)
	}

	// --independent writes each inline: nothing is shared, nothing taken in.
	if initial, _, _ := initialReleases(context.Background(), ws, pkgs, ecoOf, []string{"Acme.Core=1.0.0"}, true); len(initial) != 1 {
		t.Errorf("independent: initial = %v, want Core alone", initial)
	}

	// A sibling already released refuses the whole first release.
	stubReleasedAt(t, map[string]string{"Acme.Web": "on its registry"})
	if _, _, err := initialReleases(context.Background(), ws, pkgs, ecoOf, []string{"Acme.Core=1.0.0"}, false); err == nil || !strings.Contains(err.Error(), "Acme.Web 1.0.0 is already released") {
		t.Errorf("err = %v, want the sibling's already-released refusal", err)
	}
}

// A package the plan holds only to rewrite a range (an out-of-range
// devDependency) is released when named for its first release: it gets its
// entry, keeps its version, and keeps the rewrite.
func TestVersionInitialReleasePromotesARangeOnlyPackage(t *testing.T) {
	root := initialWorkspace(t)
	writeFileAt(t, filepath.Join(root, "packages", "theme", "package.json"), "{\n  \"name\": \"@acme/theme\",\n  \"version\": \"0.1.0\",\n  \"devDependencies\": {\n    \"@acme/core\": \"^0.1.0\"\n  }\n}\n")
	writeFileAt(t, filepath.Join(root, ".changeset", "core-buttons.md"), "---\n\"@acme/core\": minor\n---\n\nButtons and inputs\n")
	stubReleasedAt(t, nil)
	runVersion(t, "--release-as", "@acme/theme=0.1.0")

	theme := mustRead(t, filepath.Join(root, "packages", "theme", "package.json"))
	if !strings.Contains(theme, `"version": "0.1.0"`) || !strings.Contains(theme, `"@acme/core": "^0.2.0"`) {
		t.Errorf("theme manifest (want 0.1.0, its range rewritten to ^0.2.0):\n%s", theme)
	}
	if log := mustRead(t, filepath.Join(root, "packages", "theme", "CHANGELOG.md")); !strings.Contains(log, "## 0.1.0\n") || !strings.Contains(log, "- Initial release") {
		t.Errorf("theme changelog:\n%s", log)
	}
	if got := mustRead(t, filepath.Join(root, "packages", "core", "package.json")); !strings.Contains(got, `"version": "0.2.0"`) {
		t.Errorf("core, an ordinary release, wasn't bumped:\n%s", got)
	}
}

// A configured credential that can't be resolved refuses the first release
// before the registry is asked: asked without it, a registry that answers an
// unauthorized read with 404 would make a published version look unpublished.
func TestVersionInitialReleaseRefusesWhenTheConfiguredAuthIsUnavailable(t *testing.T) {
	root := initialWorkspace(t)
	writeFileAt(t, filepath.Join(root, ".changeset", "config.json"), `{"baseBranch": "main", "node": {"auth": "env:ACME_UNSET_NPM_TOKEN"}}`)
	t.Setenv("ACME_UNSET_NPM_TOKEN", "")
	_, err := runVersionErr(t, "--release-as", "@acme/core=0.1.0")
	if err == nil || !strings.Contains(err.Error(), "the configured node.auth couldn't be resolved") || !strings.Contains(err.Error(), "env:ACME_UNSET_NPM_TOKEN") {
		t.Fatalf("err = %v, want a refusal naming the unresolved node.auth", err)
	}
	if _, err := os.Stat(filepath.Join(root, "packages", "core", "CHANGELOG.md")); !os.IsNotExist(err) {
		t.Error("a changelog was written")
	}
}

// A spec naming a package that joins the first release through its version
// file clashes with that first release: refused, where applyReleaseAs would
// have moved the whole group and overwritten the pin.
func TestInitialReleaseRefusesASpecForAVersionFileSibling(t *testing.T) {
	pkgs := []plugin.Package{
		{Name: "Acme.Core", Version: "1.0.0", ManifestPath: "src/Core/Core.csproj", VersionFile: "Directory.Build.props"},
		{Name: "Acme.Web", Version: "1.0.0", ManifestPath: "src/Web/Web.csproj", VersionFile: "Directory.Build.props"},
	}
	ecoOf := map[string]string{"Acme.Core": "dotnet", "Acme.Web": "dotnet"}
	ws := &Workspace{Root: "/repo", Config: config.Default()}
	stubReleasedAt(t, nil)
	_, _, err := initialReleases(context.Background(), ws, pkgs, ecoOf, []string{"Acme.Core=1.0.0", "Acme.Web=2.0.0"}, false)
	if err == nil || !strings.Contains(err.Error(), "Acme.Web two versions, 1.0.0 (its first release) and 2.0.0") {
		t.Fatalf("err = %v, want the two-versions refusal for the sibling", err)
	}
}

// --only plans as the run does, first releases pinned: the core's minor
// changeset doesn't cascade to the theme there either, so --only the core is
// its whole group and isn't refused.
func TestVersionOnlyWithAFirstReleaseUsesThePinnedPlan(t *testing.T) {
	root := initialWorkspace(t)
	writeFileAt(t, filepath.Join(root, "packages", "theme", "package.json"), "{\n  \"name\": \"@acme/theme\",\n  \"version\": \"0.1.0\",\n  \"dependencies\": {\n    \"@acme/core\": \"^0.1.0\"\n  }\n}\n")
	writeFileAt(t, filepath.Join(root, ".changeset", "core-buttons.md"), "---\n\"@acme/core\": minor\n---\n\nButtons and inputs\n")
	stubReleasedAt(t, nil)
	out, err := runVersionErr(t, "--only", "@acme/core", "--release-as", "@acme/core=0.1.0")
	if err != nil {
		t.Fatalf("version --only: %v\n%s", err, out)
	}
	if log := mustRead(t, filepath.Join(root, "packages", "core", "CHANGELOG.md")); !strings.Contains(log, "## 0.1.0\n") || !strings.Contains(log, "Buttons and inputs") {
		t.Errorf("core changelog:\n%s", log)
	}
}

// A fixed group releases together at one version: a first release of one
// member is a first release of each. The theme, with no changeset of its own,
// joins at 0.1.0 (not the group's minor) and gets the first-release line,
// while the core keeps its changeset's notes.
func TestVersionInitialReleaseTakesInTheFixedGroup(t *testing.T) {
	root := initialWorkspace(t)
	writeFileAt(t, filepath.Join(root, ".changeset", "config.json"), `{"baseBranch": "main", "fixed": [["@acme/core", "@acme/theme"]]}`)
	writeFileAt(t, filepath.Join(root, ".changeset", "core-buttons.md"), "---\n\"@acme/core\": minor\n---\n\nButtons and inputs\n")
	stubReleasedAt(t, nil)
	runVersion(t, "--release-as", "@acme/core=0.1.0", "--initial-message", "First public release")

	for _, dir := range []string{"core", "theme"} {
		if got := mustRead(t, filepath.Join(root, "packages", dir, "package.json")); !strings.Contains(got, `"version": "0.1.0"`) {
			t.Errorf("%s left 0.1.0:\n%s", dir, got)
		}
	}
	if log := mustRead(t, filepath.Join(root, "packages", "core", "CHANGELOG.md")); !strings.Contains(log, "## 0.1.0\n") || !strings.Contains(log, "Buttons and inputs") {
		t.Errorf("core changelog:\n%s", log)
	}
	if log := mustRead(t, filepath.Join(root, "packages", "theme", "CHANGELOG.md")); !strings.Contains(log, "## 0.1.0\n") || !strings.Contains(log, "- First public release") {
		t.Errorf("theme changelog (want its first-release line):\n%s", log)
	}
}

// Two members of a fixed group, each named at its own current version, are
// both selected already: the group's one-version check still applies, so the
// run is refused rather than releasing the group at two versions.
func TestInitialReleaseRefusesAFixedGroupAtTwoVersions(t *testing.T) {
	pkgs := []plugin.Package{
		{Name: "a", Version: "1.0.0", ManifestPath: "a/package.json"},
		{Name: "b", Version: "2.0.0", ManifestPath: "b/package.json"},
	}
	ecoOf := map[string]string{"a": "node", "b": "node"}
	cfg := config.Default()
	cfg.Fixed = [][]string{{"a", "b"}}
	ws := &Workspace{Root: "/repo", Config: cfg}
	stubReleasedAt(t, nil)
	_, _, err := initialReleases(context.Background(), ws, pkgs, ecoOf, []string{"a=1.0.0", "b=2.0.0"}, false)
	if err == nil || !strings.Contains(err.Error(), "must release at the same version") {
		t.Fatalf("err = %v, want the one-version refusal", err)
	}
}
