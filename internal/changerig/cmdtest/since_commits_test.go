package cmdtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// --since narrows a plan to what a branch adds, whatever the versioning
// source: with commits as a source, a commit already on the base branch is
// not the branch's, any more than a changeset file already there is.

// sinceCommitsRepo is a two-package workspace versioning from the given source.
// main has a feat commit to pkg-a and a pkg-a changeset; the feature branch
// adds a fix commit to pkg-b and a pkg-b changeset.
func sinceCommitsRepo(t *testing.T, source string) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "1.0.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "versioning": { "source": "`+source+`" } }`)
	gitInit(t, dir)

	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "index.js"), "export {}\n")
	writeChangeset(t, dir, "main-one", "pkg-a", "patch", "Already on main")
	gitCommitAll(t, dir, "feat: a thing on main")

	git(t, dir, "switch", "-c", "feature")
	writeFile(t, filepath.Join(dir, "packages", "pkg-b", "index.js"), "export {}\n")
	writeChangeset(t, dir, "pr-one", "pkg-b", "minor", "The branch's feature")
	gitCommitAll(t, dir, "fix: a fix on the branch")
	return dir
}

// planNames runs status with --output (and any extra args) and returns the
// planned package names.
func planNames(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	plan := filepath.Join(t.TempDir(), "plan.json")
	code, out := runChangerig(t, dir, append([]string{"status", "--output", plan}, args...)...)
	assertExitZero(t, code, out)
	data, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Releases []struct{ Name string } `json:"releases"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("plan: %v\n%s", err, data)
	}
	var names []string
	for _, r := range parsed.Releases {
		names = append(names, r.Name)
	}
	return names
}

func TestStatusSinceNarrowsCommitsAndChangesets(t *testing.T) {
	for _, source := range []string{"both", "commits"} {
		t.Run(source, func(t *testing.T) {
			dir := sinceCommitsRepo(t, source)

			// Without --since, main's commit plans pkg-a too.
			if got := planNames(t, dir); len(got) != 2 {
				t.Fatalf("plan without --since = %v, want pkg-a and pkg-b", got)
			}
			got := planNames(t, dir, "--since", "main")
			if len(got) != 1 || got[0] != "pkg-b" {
				t.Fatalf("plan --since main = %v, want only pkg-b", got)
			}
		})
	}
}

func TestVersionChangelogSincePreviewsOnlyTheBranch(t *testing.T) {
	dir := sinceCommitsRepo(t, "both")

	code, out := runChangerig(t, dir, "version", "--changelog")
	assertExitZero(t, code, out)
	assertContains(t, out, "a thing on main")

	code, out = runChangerig(t, dir, "version", "--changelog", "--since", "main")
	assertExitZero(t, code, out)
	assertContains(t, out, "The branch's feature")
	assertContains(t, out, "a fix on the branch")
	assertNotContains(t, out, "a thing on main")
	assertNotContains(t, out, "Already on main")
	assertNotContains(t, out, "pkg-a")
}

// Writing a branch's share of a release would drop the base branch's changes.
func TestVersionSinceRefusesToWrite(t *testing.T) {
	dir := sinceCommitsRepo(t, "both")
	code, out := runChangerig(t, dir, "version", "--yes", "--since", "main")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "--since only narrows a preview")
	if len(changesetFiles(t, dir)) != 2 {
		t.Fatal("version --since consumed changesets")
	}
}

// On the run after `pre exit`, the prerelease's changesets graduate. They were
// consumed on the base branch, so a branch's --since plan leaves them out.
func TestStatusSinceLeavesOutTheBaseBranchsGraduation(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "1.0.0"})
	initChangesets(t, dir)
	prereleaseThenExit(t, dir) // cs1, a pkg-a minor, waits in .changeset/pre/
	gitInit(t, dir)
	git(t, dir, "switch", "-c", "feature")
	writeChangeset(t, dir, "pr-one", "pkg-b", "minor", "The branch's feature")
	gitCommitAll(t, dir, "the branch's changeset")

	if got := planNames(t, dir); len(got) != 2 {
		t.Fatalf("plan without --since = %v, want pkg-a (graduating) and pkg-b", got)
	}
	got := planNames(t, dir, "--since", "main")
	if len(got) != 1 || got[0] != "pkg-b" {
		t.Fatalf("plan --since main = %v, want only pkg-b", got)
	}
}

// A branch with nothing releasable still gets the empty plan --output
// promises, in commit mode too.
func TestStatusSinceWritesAnEmptyPlanInCommitMode(t *testing.T) {
	dir := sinceCommitsRepo(t, "commits")
	git(t, dir, "switch", "-q", "-c", "docs-only", "main")
	writeFile(t, filepath.Join(dir, "NOTES.md"), "notes\n")
	gitCommitAll(t, dir, "docs: notes")

	if got := planNames(t, dir, "--since", "main"); len(got) != 0 {
		t.Fatalf("plan --since main = %v, want empty", got)
	}
}

// A branch that exits prerelease mode itself owns the graduation.
func TestStatusSinceKeepsTheBranchsOwnGraduation(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	initChangesets(t, dir)
	writeChangeset(t, dir, "cs1", "pkg-a", "minor", "a feature")
	for _, args := range [][]string{{"pre", "enter", "next"}, {"version"}} {
		code, out := runChangerig(t, dir, args...)
		assertExitZero(t, code, out)
	}
	gitInit(t, dir)
	git(t, dir, "switch", "-c", "feature")
	code, out := runChangerig(t, dir, "pre", "exit")
	assertExitZero(t, code, out)
	gitCommitAll(t, dir, "leave prerelease mode")

	got := planNames(t, dir, "--since", "main")
	if len(got) != 1 || got[0] != "pkg-a" {
		t.Fatalf("plan --since main = %v, want pkg-a graduating", got)
	}
}

// With commits as a source, the plan's changesets include the ones the commits
// stand for, beside the files: id (the commit's short hash), summary (the
// subject without its prefix), and the bump its type gives the package, which
// a bare name in the synthesized changeset doesn't carry itself.
func TestStatusOutputListsCommitChangesets(t *testing.T) {
	dir := sinceCommitsRepo(t, "both")
	plan := filepath.Join(t.TempDir(), "plan.json")
	code, out := runChangerig(t, dir, "status", "--since", "main", "--output", plan)
	assertExitZero(t, code, out)
	data, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	type rel struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	var parsed struct {
		Changesets []struct {
			ID       string `json:"id"`
			Summary  string `json:"summary"`
			Releases []rel  `json:"releases"`
		} `json:"changesets"`
		Releases []struct {
			Name       string   `json:"name"`
			Type       string   `json:"type"`
			Changesets []string `json:"changesets"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("plan: %v\n%s", err, data)
	}
	if len(parsed.Changesets) != 2 {
		t.Fatalf("changesets = %d, want the branch's file and its commit:\n%s", len(parsed.Changesets), data)
	}
	// In the order they were loaded, which this test doesn't pin.
	file, commit := parsed.Changesets[0], parsed.Changesets[1]
	if file.ID != "pr-one" {
		file, commit = commit, file
	}
	if file.ID != "pr-one" || len(file.Releases) != 1 || file.Releases[0] != (rel{"pkg-b", "minor"}) {
		t.Errorf("file changeset = %+v, want pr-one: pkg-b minor", file)
	}
	if !regexp.MustCompile(`^[0-9a-f]{7,}$`).MatchString(commit.ID) || commit.Summary != "a fix on the branch" ||
		len(commit.Releases) != 1 || commit.Releases[0] != (rel{"pkg-b", "patch"}) {
		t.Errorf("commit changeset = %+v, want a short hash, the subject, and pkg-b patch from `fix:`", commit)
	}
	if len(parsed.Releases) != 1 || parsed.Releases[0].Name != "pkg-b" || len(parsed.Releases[0].Changesets) != 2 {
		t.Errorf("releases = %+v, want pkg-b naming both changesets", parsed.Releases)
	}
}
