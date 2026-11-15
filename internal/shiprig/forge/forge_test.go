// Ported from net-changesets tests/Changesets.Tests/Release/ForgeReleaseServiceTests.cs.
package forge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

// recordedCall is one Runner invocation (the C# RecordedCommand).
type recordedCall struct {
	dir  string
	name string
	args []string
}

func (c recordedCall) has(arg string) bool {
	for _, a := range c.args {
		if a == arg {
			return true
		}
	}
	return false
}

// recordingRunner mirrors the C# RecordingProcessExecutor: it records every
// call and delegates the result to a configurable responder.
type recordingRunner struct {
	calls     []recordedCall
	responder func(call recordedCall) (string, error)
}

func (r *recordingRunner) run(dir, name string, args ...string) (string, error) {
	call := recordedCall{dir: dir, name: name, args: args}
	r.calls = append(r.calls, call)
	if r.responder != nil {
		return r.responder(call)
	}
	return "", nil
}

func isGitRemote(call recordedCall) bool { return call.name == "git" && call.has("remote") }
func isReleaseView(call recordedCall) bool {
	return call.name == "gh" && call.has("view")
}

func pkg(name, version string) plugin.Package {
	return plugin.Package{Name: name, Version: version, Dir: name}
}

func runService(t *testing.T, packages []plugin.Package, sel Selection, repoRoot string, runner *recordingRunner) (bool, string) {
	t.Helper()
	ok, message := Run(packages, nil, nil, config.Default(), sel, repoRoot, runner.run, nil)
	return ok, message
}

// Selection shorthands for the tests.
var (
	selNone   = Selection{Forge: "none"}
	selAuto   = Selection{Forge: "auto"}
	selGitHub = Selection{Forge: "github"}
)

func TestNoneMode_DoesNothing(t *testing.T) {
	runner := &recordingRunner{}

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.0.0")}, selNone, t.TempDir(), runner)

	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %v, want none", runner.calls)
	}
	want := "Forge releases disabled; tags are handled by the publish/tag steps."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

func TestGithubMode_CreatesReleaseForMissingTag(t *testing.T) {
	runner := &recordingRunner{}
	// `release view` fails -> the release does not yet exist.
	runner.responder = func(call recordedCall) (string, error) {
		if isReleaseView(call) {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, selGitHub, t.TempDir(), runner)

	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	creates := 0
	for _, call := range runner.calls {
		if call.name == "gh" && call.has("create") && call.has("pkg-a@1.2.0") {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("gh create calls for pkg-a@1.2.0 = %d, want 1 (calls: %v)", creates, runner.calls)
	}
}

func TestGithubMode_AttachesBuildAssets(t *testing.T) {
	runner := &recordingRunner{}
	runner.responder = func(call recordedCall) (string, error) {
		if isReleaseView(call) {
			return "", errors.New("exit status 1") // release does not exist yet
		}
		return "", nil
	}
	packages := []plugin.Package{{Name: "core", Version: "1.2.0", Dir: "core"}}
	ecoOf := map[string]string{"core": "go"}
	attach := map[string][]string{"core": {"/dist/core_1.2.0_darwin_arm64.tar.gz", "/dist/checksums.txt"}}

	ok, message := Run(packages, ecoOf, attach, config.Default(), selGitHub, t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}

	var upload *recordedCall
	for i := range runner.calls {
		if c := runner.calls[i]; c.name == "gh" && c.has("upload") {
			upload = &runner.calls[i]
		}
	}
	if upload == nil {
		t.Fatalf("no gh release upload call; calls: %v", runner.calls)
	}
	// Uploads to the package's real tag, with each asset and --clobber (idempotent).
	for _, want := range []string{"core/v1.2.0", "/dist/core_1.2.0_darwin_arm64.tar.gz", "/dist/checksums.txt", "--clobber"} {
		if !upload.has(want) {
			t.Errorf("upload call missing %q; got %v", want, upload.args)
		}
	}
}

// TestGithubMode_NoAttachables_NoUpload: a package with no Attach:true artifacts
// (e.g. a registry package) gets a release but no upload call.
func TestGithubMode_NoAttachables_NoUpload(t *testing.T) {
	runner := &recordingRunner{}
	runner.responder = func(call recordedCall) (string, error) {
		if isReleaseView(call) {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}
	packages := []plugin.Package{pkg("pkg-a", "1.2.0")}

	ok, _ := Run(packages, nil, nil, config.Default(), selGitHub, t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatal("Run ok = false, want true")
	}
	for _, call := range runner.calls {
		if call.has("upload") {
			t.Fatalf("unexpected upload call: %v", call)
		}
	}
}

func TestGithubMode_SkipsExistingRelease(t *testing.T) {
	runner := &recordingRunner{}
	// `release view` succeeds -> the release already exists.

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, selGitHub, t.TempDir(), runner)

	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	for _, call := range runner.calls {
		if call.has("create") {
			t.Fatalf("unexpected create call: %v", call)
		}
	}
}

func TestAutoMode_UnsupportedOrigin_SkipsReleases(t *testing.T) {
	runner := &recordingRunner{}
	// An origin on no supported forge host (not github.com/gitlab.com, and Gitea
	// never auto-matches) ⇒ nothing selected ⇒ tags-only.
	runner.responder = func(call recordedCall) (string, error) {
		if isGitRemote(call) {
			return "https://bitbucket.org/owner/repo.git", nil
		}
		return "", nil
	}

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, selAuto, t.TempDir(), runner)

	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	for _, call := range runner.calls {
		if call.has("create") {
			t.Fatalf("unexpected create call: %v", call)
		}
	}
	want := "No supported forge remote or its CLI is unavailable; skipped releases (tags only)."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

func TestAutoMode_GithubOriginAndGhReady_CreatesRelease(t *testing.T) {
	runner := &recordingRunner{}
	runner.responder = func(call recordedCall) (string, error) {
		if isGitRemote(call) {
			return "git@github.com:owner/repo.git", nil
		}
		if isReleaseView(call) {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, selAuto, t.TempDir(), runner)

	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	authChecked := false
	creates := 0
	for _, call := range runner.calls {
		if call.has("auth") {
			authChecked = true // gh auth status was checked
		}
		if call.has("create") && call.has("pkg-a@1.2.0") {
			creates++
		}
	}
	if !authChecked {
		t.Fatalf("gh auth status was not checked (calls: %v)", runner.calls)
	}
	if creates != 1 {
		t.Fatalf("gh create calls for pkg-a@1.2.0 = %d, want 1 (calls: %v)", creates, runner.calls)
	}
}

// createTag returns the positional tag arg of the single `gh release create`
// call recorded by the runner (the arg after "create"). It fails if there is no
// such call or more than one. The positional tag is the source of truth the
// forge attaches the GitHub release to, so it must equal the tag the
// tag/publish steps pushed.
func createTag(t *testing.T, runner *recordingRunner) string {
	t.Helper()
	tag := ""
	found := false
	for _, call := range runner.calls {
		if call.name != "gh" || !call.has("create") {
			continue
		}
		for i, arg := range call.args {
			if arg == "create" && i+1 < len(call.args) {
				if found {
					t.Fatalf("multiple gh release create calls: %v", runner.calls)
				}
				tag = call.args[i+1]
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no gh release create call (calls: %v)", runner.calls)
	}
	return tag
}

func TestGithubMode_GoPackage_TagsWithModulePathTag(t *testing.T) {
	runner := &recordingRunner{}
	// `release view` fails -> the release does not yet exist, so Run creates it.
	runner.responder = func(call recordedCall) (string, error) {
		if isReleaseView(call) {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}

	// A Go-ecosystem package: the release tag must follow the module-path
	// convention (dir/vX.Y.Z), matching the tag the publish/tag steps pushed,
	// not the friendly DisplayName@version form.
	packages := []plugin.Package{{Name: "core", Version: "1.2.0", Dir: "core"}}
	ecoOf := map[string]string{"core": "go"}

	ok, message := Run(packages, ecoOf, nil, config.Default(), selGitHub, t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}

	if got := createTag(t, runner); got != "core/v1.2.0" {
		t.Fatalf("release create tag = %q, want %q", got, "core/v1.2.0")
	}
}

func TestGithubMode_MultiPackageNonGo_TagsWithNameAtVersion(t *testing.T) {
	runner := &recordingRunner{}
	runner.responder = func(call recordedCall) (string, error) {
		if isReleaseView(call) {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}

	// With more than one package there is a name to disambiguate, so each keeps
	// the name@version convention (the single-app vX.Y.Z default does not apply).
	packages := []plugin.Package{
		{Name: "widgets", Version: "1.2.0", Dir: "packages/widgets"},
		{Name: "gadgets", Version: "2.0.0", Dir: "packages/gadgets"},
	}
	ecoOf := map[string]string{"widgets": "node", "gadgets": "node"}

	ok, message := Run(packages, ecoOf, nil, config.Default(), selGitHub, t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	if findCall(runner, "gh", "create", "widgets@1.2.0") == nil {
		t.Errorf("no gh create with widgets@1.2.0; calls: %v", runner.calls)
	}
	if findCall(runner, "gh", "create", "gadgets@2.0.0") == nil {
		t.Errorf("no gh create with gadgets@2.0.0; calls: %v", runner.calls)
	}
}

func TestGithubMode_SingleApp_TagsWithVPrefix(t *testing.T) {
	runner := &recordingRunner{}
	runner.responder = func(call recordedCall) (string, error) {
		if isReleaseView(call) {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}

	// A single non-Go package is a single-app repo: there is no name to
	// disambiguate, so the tag defaults to the vX.Y.Z convention.
	packages := []plugin.Package{{Name: "Halyards.Desktop", Version: "1.0.0", Dir: "."}}
	ecoOf := map[string]string{"Halyards.Desktop": "dotnet"}

	ok, message := Run(packages, ecoOf, nil, config.Default(), selGitHub, t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	if got := createTag(t, runner); got != "v1.0.0" {
		t.Fatalf("release create tag = %q, want %q", got, "v1.0.0")
	}
}

func TestGithubMode_PackageAbsentFromEcoMap_TagsWithNameAtVersion(t *testing.T) {
	runner := &recordingRunner{}
	runner.responder = func(call recordedCall) (string, error) {
		if isReleaseView(call) {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}

	// Two packages (so the name@version convention applies); one is missing from
	// ecoOf and must still fall back to name@version, not crash.
	packages := []plugin.Package{
		{Name: "widgets", Version: "1.2.0", Dir: "packages/widgets"},
		{Name: "gadgets", Version: "2.0.0", Dir: "packages/gadgets"},
	}

	ok, message := Run(packages, map[string]string{}, nil, config.Default(), selGitHub, t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}
	if findCall(runner, "gh", "create", "widgets@1.2.0") == nil {
		t.Errorf("no gh create with widgets@1.2.0; calls: %v", runner.calls)
	}
}

func TestGithubMode_UsesChangelogSectionAsReleaseNotes(t *testing.T) {
	repoRoot := t.TempDir()
	dir := filepath.Join(repoRoot, "pkg-a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	changelog := "# pkg-a\n\n## 1.2.0\n### Minor Changes\n\n- Did a thing.\n\n## 1.1.0\n\n- Old.\n"
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &recordingRunner{}
	runner.responder = func(call recordedCall) (string, error) {
		if isReleaseView(call) {
			return "", errors.New("exit status 1")
		}
		return "", nil
	}

	ok, message := runService(t, []plugin.Package{pkg("pkg-a", "1.2.0")}, selGitHub, repoRoot, runner)
	if !ok {
		t.Fatalf("Run ok = false, want true (message: %q)", message)
	}

	var create *recordedCall
	for i := range runner.calls {
		if runner.calls[i].has("create") {
			if create != nil {
				t.Fatalf("multiple create calls: %v", runner.calls)
			}
			create = &runner.calls[i]
		}
	}
	if create == nil {
		t.Fatalf("no create call (calls: %v)", runner.calls)
	}

	notesIndex := -1
	for i, arg := range create.args {
		if arg == "--notes" {
			notesIndex = i
			break
		}
	}
	if notesIndex < 0 || notesIndex+1 >= len(create.args) {
		t.Fatalf("--notes not found in argv: %v", create.args)
	}
	notes := create.args[notesIndex+1]
	want := "### Minor Changes\n\n- Did a thing."
	if notes != want {
		t.Fatalf("notes = %q, want %q", notes, want)
	}
	if strings.Contains(notes, "Old.") {
		t.Fatalf("notes leaked the next section: %q", notes)
	}
}

// --- forgeRelease ----------------------------------------------------------

// missingReleases answers gh as if authenticated with no releases yet.
func missingReleases(call recordedCall) (string, error) {
	if isReleaseView(call) {
		return "", errors.New("exit status 1")
	}
	return "", nil
}

func ghCreates(runner *recordingRunner) []recordedCall {
	var out []recordedCall
	for _, c := range runner.calls {
		if c.name == "gh" && c.has("create") {
			out = append(out, c)
		}
	}
	return out
}

func combinedConfig(name, tag string) *config.Config {
	cfg := config.Default()
	cfg.ForgeRelease = config.ForgeRelease{Mode: config.ForgeReleaseCombined, Name: name, Tag: tag}
	return cfg
}

func writeChangelog(t *testing.T, root, dir, body string) {
	t.Helper()
	p := filepath.Join(root, dir, "CHANGELOG.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The default mode is untouched: the per-package create argv carries neither
// --latest nor --prerelease, exactly as before forgeRelease existed.
func TestPerPackageMode_CreateArgvUnchanged(t *testing.T) {
	runner := &recordingRunner{responder: missingReleases}
	cfg := config.Default()
	cfg.ForgeRelease = config.ForgeRelease{Mode: config.ForgeReleasePerPackage}
	ok, msg := Run([]plugin.Package{pkg("a", "1.0.0-next.0"), pkg("b", "2.0.0")}, nil, nil, cfg, selGitHub, t.TempDir(), runner.run, nil)
	if !ok {
		t.Fatalf("Run: %s", msg)
	}
	creates := ghCreates(runner)
	if len(creates) != 2 {
		t.Fatalf("creates = %d, want one per package: %v", len(creates), runner.calls)
	}
	want := []string{"release", "create", "a@1.0.0-next.0", "--title", "a@1.0.0-next.0", "--notes", "a@1.0.0-next.0"}
	if strings.Join(creates[0].args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q, want %q", creates[0].args, want)
	}
}

// Combined: one release on the templated tag, named from the template, marked
// latest, with each package's changelog entry under its own heading, ordered by
// package name whatever order discovery gave, and the tag ensured first.
func TestCombinedMode_OneLatestReleaseWithOrderedNotes(t *testing.T) {
	root := t.TempDir()
	writeChangelog(t, root, "@zooui/nuxt", "# @zooui/nuxt\n\n## 0.1.1\n\n### Patch Changes\n\n- nuxt fix\n\n## 0.1.0\n\n- old\n")
	writeChangelog(t, root, "@zooui/theme-forest", "# theme\n\n## 0.1.1\n\n### Patch Changes\n\n- forest fix\n")
	runner := &recordingRunner{responder: missingReleases}
	var ensured []string
	results, ok, msg := Make(Request{
		Packages:  []plugin.Package{pkg("@zooui/theme-forest", "0.1.1"), pkg("@zooui/nuxt", "0.1.1")},
		Config:    combinedConfig("Zoo UI ${version}", ""),
		Selection: selGitHub, RepoRoot: root, Run: runner.run,
		EnsureTag: func(tag string, dry bool) (string, error) {
			ensured = append(ensured, tag)
			return "", nil
		},
	})
	if !ok {
		t.Fatalf("Make: %s", msg)
	}
	creates := ghCreates(runner)
	if len(creates) != 1 {
		t.Fatalf("creates = %d, want 1: %v", len(creates), runner.calls)
	}
	c := creates[0]
	if c.args[2] != "v0.1.1" {
		t.Errorf("tag = %q, want v0.1.1 (the default template)", c.args[2])
	}
	if got := argAfter(&c, "--title"); got != "Zoo UI 0.1.1" {
		t.Errorf("title = %q", got)
	}
	if !c.has("--latest") || c.has("--prerelease") {
		t.Errorf("want --latest and no --prerelease: %q", c.args)
	}
	wantNotes := "## @zooui/nuxt@0.1.1\n\n### Patch Changes\n\n- nuxt fix\n\n## @zooui/theme-forest@0.1.1\n\n### Patch Changes\n\n- forest fix"
	if got := argAfter(&c, "--notes"); got != wantNotes {
		t.Errorf("notes =\n%s\nwant\n%s", got, wantNotes)
	}
	if strings.Join(ensured, ",") != "v0.1.1" {
		t.Errorf("ensured tags = %v, want [v0.1.1]", ensured)
	}
	if len(results) != 1 || results[0].Action != ActionCreated || !results[0].Latest ||
		strings.Join(results[0].Packages, ",") != "@zooui/nuxt,@zooui/theme-forest" {
		t.Errorf("results = %+v", results)
	}
}

// Packages on different versions can't fill ${version}: the run fails naming
// each, before any forge or tag call.
func TestCombinedMode_VersionMismatchFails(t *testing.T) {
	runner := &recordingRunner{responder: missingReleases}
	ensured := false
	_, ok, msg := Make(Request{
		Packages: []plugin.Package{pkg("a", "1.0.0"), pkg("b", "1.1.0")},
		Config:   combinedConfig("", ""), Selection: selGitHub, RepoRoot: t.TempDir(), Run: runner.run,
		EnsureTag: func(string, bool) (string, error) { ensured = true; return "", nil },
	})
	if ok {
		t.Fatal("Make ok with packages on different versions")
	}
	for _, want := range []string{"a 1.0.0", "b 1.1.0", "fixed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
	if len(runner.calls) != 0 || ensured {
		t.Errorf("forge or tag touched before the check: %v", runner.calls)
	}
}

// Templates without ${version} take any mix of versions.
func TestCombinedMode_NoVersionInTemplatesAllowsMixedVersions(t *testing.T) {
	runner := &recordingRunner{responder: missingReleases}
	_, ok, msg := Make(Request{
		Packages: []plugin.Package{pkg("a", "1.0.0"), pkg("b", "1.1.0")},
		Config:   combinedConfig("Nightly", "nightly"), Selection: selGitHub, RepoRoot: t.TempDir(), Run: runner.run,
	})
	if !ok {
		t.Fatalf("Make: %s", msg)
	}
	if c := ghCreates(runner); len(c) != 1 || c[0].args[2] != "nightly" || argAfter(&c[0], "--title") != "Nightly" {
		t.Errorf("creates = %v", c)
	}
}

// Any prerelease among the packages makes the release a prerelease, which is
// never marked latest.
func TestCombinedMode_Prerelease(t *testing.T) {
	runner := &recordingRunner{responder: missingReleases}
	results, ok, msg := Make(Request{
		Packages: []plugin.Package{pkg("a", "2.0.0-next.1"), pkg("b", "2.0.0-next.1")},
		Config:   combinedConfig("", ""), Selection: selGitHub, RepoRoot: t.TempDir(), Run: runner.run,
	})
	if !ok {
		t.Fatalf("Make: %s", msg)
	}
	c := ghCreates(runner)
	if len(c) != 1 || !c[0].has("--prerelease") || c[0].has("--latest") {
		t.Fatalf("creates = %v", c)
	}
	if argAfter(&c[0], "--title") != "v2.0.0-next.1" {
		t.Errorf("title = %q, want the tag when no name is set", argAfter(&c[0], "--title"))
	}
	if !results[0].Prerelease || results[0].Latest {
		t.Errorf("result = %+v", results[0])
	}
}

// An existing combined release is skipped; its assets are still (re)attached
// and its URL reported.
func TestCombinedMode_ExistingReleaseSkipped(t *testing.T) {
	runner := &recordingRunner{responder: func(c recordedCall) (string, error) {
		if isReleaseView(c) && c.has("--json") {
			return "https://github.com/acme/repo/releases/tag/v1.0.0\n", nil
		}
		return "", nil // view succeeds: it exists
	}}
	results, ok, msg := Make(Request{
		Packages: []plugin.Package{pkg("a", "1.0.0"), pkg("b", "1.0.0")},
		Attach:   map[string][]string{"b": {"/dist/b.tgz"}, "a": {"/dist/a.zip", "/dist/b.tgz"}},
		Config:   combinedConfig("", ""), Selection: selGitHub, RepoRoot: t.TempDir(), Run: runner.run, WantURLs: true,
	})
	if !ok {
		t.Fatalf("Make: %s", msg)
	}
	if c := ghCreates(runner); len(c) != 0 {
		t.Errorf("created over an existing release: %v", c)
	}
	var uploads []recordedCall
	for _, c := range runner.calls {
		if c.has("upload") {
			uploads = append(uploads, c)
		}
	}
	// One upload to the one release: every package's assets, deduplicated.
	if len(uploads) != 1 || strings.Join(uploads[0].args, " ") != "release upload v1.0.0 /dist/a.zip /dist/b.tgz --clobber" {
		t.Errorf("uploads = %v", uploads)
	}
	if len(results) != 1 || results[0].Action != ActionExists || results[0].URL != "https://github.com/acme/repo/releases/tag/v1.0.0" {
		t.Errorf("results = %+v", results)
	}
}

// A dry run creates, uploads and tags nothing, and says what it would do.
func TestCombinedMode_DryRun(t *testing.T) {
	runner := &recordingRunner{responder: missingReleases}
	var lines []string
	var dryEnsure []bool
	results, ok, msg := Make(Request{
		Packages: []plugin.Package{pkg("a", "1.0.0")},
		Attach:   map[string][]string{"a": {"/dist/a.zip"}},
		Config:   combinedConfig("Acme ${version}", ""), Selection: selGitHub, RepoRoot: t.TempDir(), Run: runner.run,
		DryRun: true, WantURLs: true,
		Report: func(l ...string) { lines = append(lines, l...) },
		EnsureTag: func(tag string, dry bool) (string, error) {
			dryEnsure = append(dryEnsure, dry)
			return "would tag " + tag, nil
		},
	})
	if !ok {
		t.Fatalf("Make: %s", msg)
	}
	for _, c := range runner.calls {
		if c.has("create") || c.has("upload") {
			t.Errorf("dry run ran %v", c)
		}
	}
	if len(dryEnsure) != 1 || !dryEnsure[0] {
		t.Errorf("EnsureTag dry flags = %v, want [true]", dryEnsure)
	}
	out := strings.Join(lines, "\n")
	for _, want := range []string{"would tag v1.0.0", `would create release v1.0.0 "Acme 1.0.0" on github, latest`, "## a@1.0.0", "would attach 1 asset(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if len(results) != 1 || results[0].Action != ActionWouldCreate || results[0].URL != "" {
		t.Errorf("results = %+v", results)
	}
}

// Released narrows the run to a subset; Packages still decides single-app tags.
func TestMake_ReleasedSubset(t *testing.T) {
	runner := &recordingRunner{responder: missingReleases}
	_, ok, msg := Make(Request{
		Packages: []plugin.Package{pkg("a", "1.0.0"), pkg("b", "1.0.0")},
		Released: []plugin.Package{pkg("b", "1.0.0")},
		Config:   config.Default(), Selection: selGitHub, RepoRoot: t.TempDir(), Run: runner.run,
	})
	if !ok {
		t.Fatalf("Make: %s", msg)
	}
	if c := ghCreates(runner); len(c) != 1 || c[0].args[2] != "b@1.0.0" {
		t.Errorf("creates = %v", c)
	}
}

// An unknown mode is an error, not the per-package default.
func TestMake_UnknownModeFails(t *testing.T) {
	cfg := config.Default()
	cfg.ForgeRelease.Mode = "combind"
	_, ok, msg := Make(Request{Packages: []plugin.Package{pkg("a", "1.0.0")}, Config: cfg, Selection: selGitHub, RepoRoot: t.TempDir(), Run: (&recordingRunner{}).run})
	if ok || !strings.Contains(msg, `"combind"`) {
		t.Errorf("ok=%v msg=%q", ok, msg)
	}
}
