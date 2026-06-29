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
