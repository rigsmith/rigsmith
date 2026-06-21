// Command-level tests ported from net-changesets:
//
//	InitCommandTests, AddCommandTests, StatusChangesetCommandTests,
//	PreChangesetCommandTests, VersionChangesetCommandTests,
//	InfoChangesetCommandTests, TagChangesetCommandTests, CommandDispatcherTests
//
// Interop-extension (.net.mkd), Node-delegation (autoRunNode / NodeChangesetService),
// and Spectre console-markup cases are out of scope: the Go tool has one engine
// for every ecosystem, so there is nothing to delegate to and no second
// changeset extension. Interactive-prompt cases are also skipped — the Go CLI's
// prompts are a bubbletea/huh TUI that needs a real TTY; the non-interactive
// flag forms cover the same contracts.
package cmdtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
)

// --- init (InitCommandTests) ---

// Ported from InitCommand_HappyPath_FolderAndFilesAreCreated.
func TestInitCreatesChangesetFolderAndConfig(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "init")

	assertExitZero(t, code, out)
	assertContains(t, out, "initialized changesets")
	if !fileExists(filepath.Join(dir, ".changeset", "config.json")) {
		t.Error(".changeset/config.json was not created")
	}
	if !fileExists(filepath.Join(dir, ".changeset", "README.md")) {
		t.Error(".changeset/README.md was not created")
	}
}

// Ported from InitCommand_HappyPathRunTwice_ConsoleContainMessageThatAlreadyExists.
// The Go tool reports "already initialized" with exit 0 (C# used a distinct
// AlreadyInitialized result code; Go treats it as a benign no-op).
func TestInitTwiceReportsAlreadyInitialized(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "init")
	assertExitZero(t, code, out)

	code, out = runChangerig(t, dir, "init")
	assertExitZero(t, code, out)
	assertContains(t, out, "already initialized")
}

// Ported from InitCommand_HappyPathRunTwice_ConfigIsMissingAndWillBeGenerated:
// deleting config.json and re-running init regenerates it.
func TestInitRegeneratesDeletedConfig(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "init")
	assertExitZero(t, code, out)

	cfg := filepath.Join(dir, ".changeset", "config.json")
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}

	code, out = runChangerig(t, dir, "init")
	assertExitZero(t, code, out)
	assertContains(t, out, "initialized changesets")
	if !fileExists(cfg) {
		t.Error("config.json was not regenerated")
	}
}

// Ported from CreateDefaultAsync_WritesDualToolValidConfigThatRoundTrips
// (file-writing half; TestDefaults in core/config covers the values): the
// config `init` writes parses back to the documented defaults.
func TestInitConfigRoundTrips(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "init")
	assertExitZero(t, code, out)

	cfg, err := config.Load(filepath.Join(dir, ".changeset"))
	if err != nil {
		t.Fatalf("written config does not parse: %v", err)
	}
	if cfg.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main", cfg.BaseBranch)
	}
	if cfg.Access != "restricted" {
		t.Errorf("Access = %q, want restricted", cfg.Access)
	}
	if cfg.UpdateInternalDependencies != config.UpdatePatch {
		t.Errorf("UpdateInternalDependencies = %q, want patch", cfg.UpdateInternalDependencies)
	}
	if len(cfg.Ignore) != 0 || len(cfg.Fixed) != 0 || len(cfg.Linked) != 0 {
		t.Error("ignore/fixed/linked should round-trip empty")
	}
	if got := cfg.ChangelogSpec(); got != "default" {
		t.Errorf("ChangelogSpec() = %q, want default", got)
	}
}

// init --source commits scaffolds a commit-driven workspace non-interactively,
// writing versioning.source so a version run reads intent from commits.
func TestInitSourceCommitsWritesVersioning(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "init", "--source", "commits")

	assertExitZero(t, code, out)
	assertContains(t, out, "source: commits")
	cfg, err := config.Load(filepath.Join(dir, ".changeset"))
	if err != nil {
		t.Fatalf("written config does not parse: %v", err)
	}
	if got := cfg.CommitSource(); got != config.SourceCommits {
		t.Errorf("CommitSource() = %q, want commits", got)
	}
}

// An unknown --source is rejected before anything is written.
func TestInitSourceInvalidFails(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "init", "--source", "bogus")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "source")
	if fileExists(filepath.Join(dir, ".changeset", "config.json")) {
		t.Error("config.json should not be written when --source is invalid")
	}
}

// The default init (no --source, off a TTY) stays changeset mode and omits the
// versioning block, so the classic config round-trips to changesets.
func TestInitDefaultsToChangesets(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "init")
	assertExitZero(t, code, out)

	cfg, err := config.Load(filepath.Join(dir, ".changeset"))
	if err != nil {
		t.Fatalf("written config does not parse: %v", err)
	}
	if got := cfg.CommitSource(); got != config.SourceChangesets {
		t.Errorf("CommitSource() = %q, want changesets", got)
	}
}

// --- add (AddCommandTests, non-interactive forms) ---

// Ported from AddCommand_Empty_CreatesEmptyChangesetThatRoundTrips: --empty
// writes a changeset naming no packages.
func TestAddEmptyWritesChangesetNamingNoPackages(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "add", "--empty")

	assertExitZero(t, code, out)
	// The binary prints the path with the OS separator (filepath.Join), so
	// assert only up to the directory name.
	assertContains(t, out, "created .changeset")
	files := changesetFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("changeset files = %d, want 1", len(files))
	}
	if got, want := readFile(t, files[0]), "---\n---\n\n"; got != want {
		t.Errorf("empty changeset content = %q, want %q", got, want)
	}
}

// Ported from AddCommand_Message_UsesProvidedSummaryWithoutPrompting, adapted
// to the fully non-interactive form: -m + -p writes the package at the default
// patch bump with the given summary.
func TestAddMessageAndPackageWritesPatchChangeset(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "add", "-m", "Summary provided via flag", "-p", "pkg-a")

	assertExitZero(t, code, out)
	files := changesetFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("changeset files = %d, want 1", len(files))
	}
	want := "---\n\"pkg-a\": patch\n---\n\nSummary provided via flag"
	if got := readFile(t, files[0]); got != want {
		t.Errorf("changeset content = %q, want %q", got, want)
	}
}

// A --package naming something outside the workspace fails up front (rather
// than writing a changeset `version` would later reject), and the error names
// both the bad package and the available ones.
func TestAddUnknownPackageFails(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "add", "-m", "x", "-p", "ghost")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "unknown package(s): ghost")
	assertContains(t, out, "pkg-a")
	if files := changesetFiles(t, dir); len(files) != 0 {
		t.Fatalf("no changeset should be written on an invalid package; got %d", len(files))
	}
}

// --bump overrides the default patch bump.
func TestAddBumpOverride(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "add", "-m", "a feature", "-p", "pkg-a", "--bump", "minor")

	assertExitZero(t, code, out)
	files := changesetFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("changeset files = %d, want 1", len(files))
	}
	assertContains(t, readFile(t, files[0]), `"pkg-a": minor`)
}

// --type writes the conventional type into the frontmatter and leaves the
// per-package bump to derive from it (no explicit bump on the release line).
func TestAddTypeDerivesBump(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "add", "-m", "a feature", "-p", "pkg-a", "--type", "feat")

	assertExitZero(t, code, out)
	files := changesetFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("changeset files = %d, want 1", len(files))
	}
	want := "---\ntype: feat\n\"pkg-a\"\n---\n\na feature"
	if got := readFile(t, files[0]); got != want {
		t.Errorf("changeset content = %q, want %q", got, want)
	}
}

// Ported from AddCommand_NotInitialized_ReturnsErrorCodeWithMessage: in a
// workspace without .changeset, add (off a TTY — it can't offer to set up
// changesets) fails with a clear error pointing at `changerig init`.
func TestAddNotInitialized(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "add", "--empty")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "changerig init")
	assertContains(t, out, ".changeset")
}

// In a commits-mode workspace, `add` doesn't write a changeset file — the
// commits are the release source, so it guides the user to a conventional
// commit instead. (Reachable now that init can scaffold commits mode.)
func TestAddInCommitsModeGuidesToConventionalCommits(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "versioning": { "source": "commits" } }`)

	code, out := runChangerig(t, dir, "add", "-m", "a feature", "-p", "pkg-a")

	assertExitZero(t, code, out)
	assertContains(t, out, "conventional commit")
	if files := changesetFiles(t, dir); len(files) != 0 {
		t.Errorf("commits mode should write no changeset file, got %v", files)
	}
}

// --- status (StatusChangesetCommandTests) ---

// Ported from StatusChangesetCommand_WhenChangesetExists_FinishesWithNoError;
// the plan lists the package with its target version.
func TestStatusWithChangesetListsThePlan(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a change")

	code, out := runChangerig(t, dir, "status")

	assertExitZero(t, code, out)
	assertContains(t, out, "pkg-a")
	assertContains(t, out, "1.1.0")
}

// Ported from StatusChangesetCommand_WhenNoChangesetExists_FinishesWithError.
func TestStatusNoChangesetsFails(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "status")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "no changesets found")
}

// Ported from StatusChangesetCommand_Verbose_ShowsTheNewVersionAndChangesetFiles,
// adapted: Go's --verbose shows the change summary line under each package
// (it does not print changeset file paths).
func TestStatusVerboseShowsChangeSummary(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a very specific change line")

	code, out := runChangerig(t, dir, "status", "--verbose")

	assertExitZero(t, code, out)
	assertContains(t, out, "1.1.0")
	assertContains(t, out, "a very specific change line")
}

// Ported from StatusChangesetCommand_Output_WritesTheReleasePlanAsJson. The Go
// plan shape is @changesets' { releases: [{ name, type, newVersion }] } —
// changeset ids are not part of it (unlike the C# plan).
func TestStatusOutputWritesJSONPlan(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a change")

	code, out := runChangerig(t, dir, "status", "--output", "plan.json")

	assertExitZero(t, code, out)
	var plan struct {
		Releases []struct {
			Name       string `json:"name"`
			Type       string `json:"type"`
			NewVersion string `json:"newVersion"`
		} `json:"releases"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "plan.json"))), &plan); err != nil {
		t.Fatalf("parse plan.json: %v", err)
	}
	if len(plan.Releases) != 1 {
		t.Fatalf("plan releases = %d, want 1", len(plan.Releases))
	}
	r := plan.Releases[0]
	if r.Name != "pkg-a" || r.Type != "minor" || r.NewVersion != "1.1.0" {
		t.Errorf("release = %+v, want {pkg-a minor 1.1.0}", r)
	}
}

// sinceRepo builds a git repo on main holding a two-package workspace, a
// changeset for pkg-b (old.md) and a pkg-a source file, then checks out a
// feature branch — the baseline for the --since scenarios.
func sinceRepo(t *testing.T) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "1.0.0"})
	initChangesets(t, dir)
	writeChangeset(t, dir, "old", "pkg-b", "minor", "Old change")
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "index.js"), "x\n")
	gitInit(t, dir)
	git(t, dir, "checkout", "-b", "feature")
	return dir
}

// Ported from StatusChangesetCommand_Since_WhenAChangesetWasAdded_ContinuesNormally,
// strengthened: the plan is narrowed to the changesets added since the ref, so
// pkg-b's pre-existing changeset (committed on main) drops out.
func TestStatusSinceNarrowsToChangesetsAddedSinceRef(t *testing.T) {
	dir := sinceRepo(t)
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "index.js"), "x\ny\n")
	writeChangeset(t, dir, "new", "pkg-a", "patch", "New fix")
	gitCommitAll(t, dir, "change pkg-a with changeset")

	code, out := runChangerig(t, dir, "status", "--since", "main")

	assertExitZero(t, code, out)
	assertContains(t, out, "pkg-a")
	assertContains(t, out, "1.0.1")
	assertNotContains(t, out, "pkg-b")
}

// Ported from StatusChangesetCommand_Since_WhenProjectChangedButNoChangesetAdded_FinishesWithError:
// a project change with no changeset since the ref fails with add-a-changeset guidance.
func TestStatusSinceChangedProjectWithoutChangesetFails(t *testing.T) {
	dir := sinceRepo(t)
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "index.js"), "x\ny\n")
	gitCommitAll(t, dir, "change pkg-a without changeset")

	code, out := runChangerig(t, dir, "status", "--since", "main")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "no changeset was found")
	assertContains(t, out, "pkg-a")
	assertContains(t, out, "add")
}

// An invalid ref is an error, not "no changes".
func TestStatusSinceInvalidRefFails(t *testing.T) {
	dir := sinceRepo(t)

	code, out := runChangerig(t, dir, "status", "--since", "bogus-ref")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "bogus-ref")
}

// In pre-mode, status shows the prerelease target version — exactly what
// `version` would produce (1.0.0 + minor under tag next → 1.1.0-next.0).
func TestStatusInPreModeShowsPrereleaseTarget(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a change")

	code, out := runChangerig(t, dir, "pre", "enter", "next")
	assertExitZero(t, code, out)

	code, out = runChangerig(t, dir, "status")
	assertExitZero(t, code, out)
	assertContains(t, out, "1.1.0-next.0")
}

// --- pre (PreChangesetCommandTests) ---

// preState mirrors .changeset/pre.json.
type preState struct {
	Mode            string            `json:"mode"`
	Tag             string            `json:"tag"`
	InitialVersions map[string]string `json:"initialVersions"`
}

func readPreState(t *testing.T, dir string) preState {
	t.Helper()
	var ps preState
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, ".changeset", "pre.json"))), &ps); err != nil {
		t.Fatalf("parse pre.json: %v", err)
	}
	return ps
}

// Ported from Enter_WritesPreModeWithTheTagAndInitialVersions.
func TestPreEnterWritesPreState(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "pre", "enter", "next")

	assertExitZero(t, code, out)
	ps := readPreState(t, dir)
	if ps.Mode != "pre" {
		t.Errorf("pre.json mode = %q, want \"pre\"", ps.Mode)
	}
	if ps.Tag != "next" {
		t.Errorf("pre.json tag = %q, want \"next\"", ps.Tag)
	}
	if got := ps.InitialVersions["pkg-a"]; got != "1.0.0" {
		t.Errorf("pre.json initialVersions[pkg-a] = %q, want \"1.0.0\"", got)
	}
}

// Ported from Enter_WithoutATag_Fails.
func TestPreEnterWithoutTagFails(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "pre", "enter")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "requires a tag")
	if fileExists(filepath.Join(dir, ".changeset", "pre.json")) {
		t.Error("pre.json should not be written on failure")
	}
}

// Ported from Enter_WhenAlreadyInPreMode_FailsWithoutWriting.
func TestPreEnterTwiceFails(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "pre", "enter", "next")
	assertExitZero(t, code, out)

	code, out = runChangerig(t, dir, "pre", "enter", "rc")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "already in prerelease mode")
	if got := readPreState(t, dir).Tag; got != "next" {
		t.Errorf("pre.json tag = %q, want the original \"next\" (second enter must not write)", got)
	}
}

// Ported from Exit_FlipsTheModeToExit.
func TestPreExitFlipsModeToExit(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "pre", "enter", "next")
	assertExitZero(t, code, out)

	code, out = runChangerig(t, dir, "pre", "exit")
	assertExitZero(t, code, out)
	if got := readPreState(t, dir).Mode; got != "exit" {
		t.Errorf("pre.json mode = %q, want \"exit\"", got)
	}
}

// Ported from Exit_WhenNotInPreMode_Fails.
func TestPreExitWhenNotInPreFails(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "pre", "exit")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "not in prerelease mode")
}

// Ported from UnknownAction_Fails.
func TestPreUnknownActionFails(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "pre", "sideways")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "unknown pre action")
}

// --- version (VersionChangesetCommandTests; full flows live in the parity suite) ---

// --dry-run prints the plan but writes nothing: the manifest keeps its version
// and the changeset is not consumed.
func TestVersionDryRunWritesNothing(t *testing.T) {
	dir := newWorkspace(t)
	csPath := writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a change")

	code, out := runChangerig(t, dir, "version", "--dry-run")

	assertExitZero(t, code, out)
	assertContains(t, out, "dry run")
	assertContains(t, out, "1.1.0")
	if got := readFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json")); !strings.Contains(got, `"version": "1.0.0"`) {
		t.Errorf("manifest was modified on a dry run:\n%s", got)
	}
	if !fileExists(csPath) {
		t.Error("changeset was consumed on a dry run")
	}
	if fileExists(filepath.Join(dir, "packages", "pkg-a", "CHANGELOG.md")) {
		t.Error("CHANGELOG.md was written on a dry run")
	}
}

// --dry-run alone prints only the bump table — no rendered changelog notes.
func TestVersionDryRunOmitsChangelogByDefault(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a shiny new feature")

	code, out := runChangerig(t, dir, "version", "--dry-run")

	assertExitZero(t, code, out)
	assertContains(t, out, "1.1.0")
	assertNotContains(t, out, "a shiny new feature")
	assertNotContains(t, out, "## 1.1.0")
}

// --dry-run --changelog appends each releasing package's rendered notes (the
// "## <version>" heading + grouped sections) and still writes nothing.
func TestVersionDryRunChangelogRendersNotes(t *testing.T) {
	dir := newWorkspace(t)
	csPath := writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a shiny new feature")

	code, out := runChangerig(t, dir, "version", "--dry-run", "--changelog")

	assertExitZero(t, code, out)
	assertContains(t, out, "dry run")
	assertContains(t, out, "## 1.1.0")          // the rendered heading
	assertContains(t, out, "### Minor Changes") // the grouped section
	assertContains(t, out, "a shiny new feature")
	if !fileExists(csPath) {
		t.Error("changeset was consumed on a --changelog preview")
	}
	if fileExists(filepath.Join(dir, "packages", "pkg-a", "CHANGELOG.md")) {
		t.Error("CHANGELOG.md was written on a --changelog preview")
	}
}

// --changelog on its own implies the dry-run preview: it renders the notes and
// writes nothing, no explicit --dry-run needed.
func TestVersionChangelogImpliesDryRun(t *testing.T) {
	dir := newWorkspace(t)
	csPath := writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a shiny new feature")

	code, out := runChangerig(t, dir, "version", "--changelog")

	assertExitZero(t, code, out)
	assertContains(t, out, "## 1.1.0")
	if got := readFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json")); !strings.Contains(got, `"version": "1.0.0"`) {
		t.Errorf("manifest was modified on a --changelog preview:\n%s", got)
	}
	if !fileExists(csPath) {
		t.Error("changeset was consumed on a --changelog preview")
	}
}

// version with no changesets is a friendly no-op, exit 0.
func TestVersionNoChangesetsIsANoOp(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "version")

	assertExitZero(t, code, out)
	assertContains(t, out, "no changesets")
	if got := readFile(t, filepath.Join(dir, "packages", "pkg-a", "package.json")); !strings.Contains(got, `"version": "1.0.0"`) {
		t.Errorf("manifest was modified with no changesets:\n%s", got)
	}
}

// --- info (InfoChangesetCommandTests) ---

// Ported from Info_RendersTheResolvedConfigAndProjectCount, adapted to the Go
// output: workspace facts, baseBranch, package count + names, changeset count.
func TestInfoListsConfigAndPackages(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a change")

	code, out := runChangerig(t, dir, "info")

	assertExitZero(t, code, out)
	assertContains(t, out, "initialized: true")
	assertContains(t, out, "baseBranch:  main")
	assertContains(t, out, "source:      changesets")
	assertContains(t, out, "Packages (1)")
	assertContains(t, out, "pkg-a")
	assertContains(t, out, "Changesets: 1")
}

// Ported from Info_BeforeInit_ReportsNotInitialized, adapted: the Go tool does
// not fail before init — it reports "initialized: false" with exit 0 (C#
// returned a NotInitialized error code; documented divergence).
func TestInfoBeforeInitReportsUninitialized(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runChangerig(t, dir, "info")

	assertExitZero(t, code, out)
	assertContains(t, out, "initialized: false")
	assertContains(t, out, "pkg-a")
}

// --- dispatcher (CommandDispatcherTests, adapted) ---

// The C# suite proved the menu's nested-dispatch plumbing; the Go CLI contract
// to hold is the front door's: an unknown subcommand fails with a usage-ish error.
func TestUnknownSubcommandFails(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runChangerig(t, dir, "definitely-not-a-command")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "unknown command")
	assertContains(t, out, "definitely-not-a-command")
}

// --- shiprig default command (bare shiprig = status, not add) ---

// Bare `shiprig` shows the pending release plan (it delegates to status), so an
// initialized workspace with a changeset lists the package and its target
// version — and writes no changeset, unlike the old `add` default.
func TestShiprigBareShowsStatus(t *testing.T) {
	dir := newWorkspace(t)
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a change")

	code, out := runShiprig(t, dir)

	assertExitZero(t, code, out)
	assertContains(t, out, "pkg-a")
	assertContains(t, out, "1.1.0")
	// status never creates changesets; only the seeded one exists.
	if files := changesetFiles(t, dir); len(files) != 1 {
		t.Errorf("bare shiprig should not create a changeset, files = %v", files)
	}
}

// Bare `shiprig` in an uninitialized repo, off a TTY (can't offer setup), fails
// with actionable guidance pointing at `shiprig init` rather than the raw
// no-changesets error.
func TestShiprigBareUninitializedGuidesToInit(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	code, out := runShiprig(t, dir)

	assertExitNonZero(t, code, out)
	assertContains(t, out, "shiprig init")
}

// --- tag (TagChangesetCommandTests, shiprig binary) ---

// tagWorkspace builds a committed two-package npm git repo for the tag tests.
func tagWorkspace(t *testing.T) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "2.1.0"})
	initChangesets(t, dir)
	gitInit(t, dir)
	return dir
}

func tagList(t *testing.T, dir string) []string {
	t.Helper()
	out := git(t, dir, "tag", "--list")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// Ported from Tag_CreatesATagForEachProjectAtItsCurrentVersion: npm packages
// get a name@version tag each.
func TestTagCreatesTagPerPackage(t *testing.T) {
	dir := tagWorkspace(t)

	code, out := runShiprig(t, dir, "tag")

	assertExitZero(t, code, out)
	assertContains(t, out, "pkg-a@1.0.0")
	assertContains(t, out, "pkg-b@2.1.0")
	tags := tagList(t, dir)
	if len(tags) != 2 {
		t.Fatalf("tags = %v, want exactly [pkg-a@1.0.0 pkg-b@2.1.0]", tags)
	}
	for _, want := range []string{"pkg-a@1.0.0", "pkg-b@2.1.0"} {
		if !contains(tags, want) {
			t.Errorf("missing tag %q in %v", want, tags)
		}
	}
}

// Ported from Tag_SkipsTagsThatAlreadyExist: a pre-existing tag is skipped,
// only the missing one is created, exit stays 0.
func TestTagSkipsExistingTags(t *testing.T) {
	dir := tagWorkspace(t)
	git(t, dir, "tag", "pkg-a@1.0.0")

	code, out := runShiprig(t, dir, "tag")

	assertExitZero(t, code, out)
	assertContains(t, out, "pkg-b@2.1.0")
	assertContains(t, out, "1 already present")
	if tags := tagList(t, dir); len(tags) != 2 {
		t.Errorf("tags = %v, want 2", tags)
	}

	// A full re-run skips everything and still exits 0.
	code, out = runShiprig(t, dir, "tag")
	assertExitZero(t, code, out)
	assertContains(t, out, "2 already present")
	if tags := tagList(t, dir); len(tags) != 2 {
		t.Errorf("tags after re-run = %v, want 2", tags)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Ported from Tag_HonorsTheIgnoreList: an ignored package gets no tag.
// (This exposed a real gap — tag.go originally looped every discovered
// package without consulting the ignore config; fixed alongside this test.)
func TestTagHonorsIgnore(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "2.1.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "ignore": ["pkg-b"] }`)
	gitInit(t, dir)

	code, out := runShiprig(t, dir, "tag")

	assertExitZero(t, code, out)
	tags := tagList(t, dir)
	if len(tags) != 1 || tags[0] != "pkg-a@1.0.0" {
		t.Errorf("tags = %v, want only pkg-a@1.0.0 (pkg-b is ignored)", tags)
	}
}
