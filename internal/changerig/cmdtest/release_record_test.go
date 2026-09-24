package cmdtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The release record (versioning.record): `version` writes the version every
// package releases at into .changeset/versions.json's `released`, and
// `doctor` checks the manifests and tags against it.

// recordRepo has lib, app (depending on lib's ^1.0.0) and solo, all at
// 1.0.0, with a major changeset on lib, which takes app. config is extra
// config JSON fields.
func recordRepo(t *testing.T, config string) string {
	t.Helper()
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"lib": "1.0.0", "solo": "1.0.0"})
	writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
		`{ "name": "app", "version": "1.0.0", "dependencies": { "lib": "^1.0.0" } }`)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch"`+config+` }`)
	writeChangeset(t, dir, "lib-change", "lib", "major", "A breaking lib change")
	gitInit(t, dir)
	return dir
}

const recordOn = `, "versioning": { "record": true }`

// readRecord returns versions.json's `released`, and whether the file exists.
func readRecord(t *testing.T, dir string) (map[string]string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".changeset", "versions.json"))
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Released map[string]string `json:"released"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("versions.json: %v\n%s", err, data)
	}
	return parsed.Released, true
}

func TestVersionRecordsWhatItReleases(t *testing.T) {
	dir := recordRepo(t, recordOn)
	code, out := runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)

	got, _ := readRecord(t, dir)
	// lib's major, and app's patch from the cascade; solo didn't release.
	want := map[string]string{"lib": "2.0.0", "app": "1.0.1"}
	if len(got) != len(want) || got["lib"] != want["lib"] || got["app"] != want["app"] {
		t.Errorf("released = %v, want %v", got, want)
	}

	// The next release updates only what it releases.
	gitCommitAll(t, dir, "release")
	writeChangeset(t, dir, "solo-change", "solo", "patch", "A solo fix")
	code, out = runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)
	got, _ = readRecord(t, dir)
	if got["solo"] != "1.0.1" || got["lib"] != "2.0.0" || got["app"] != "1.0.1" {
		t.Errorf("released after the second run = %v", got)
	}
}

// A range-only rewrite releases nothing, so it isn't recorded: tool
// dev-depends on lib, whose major leaves its ^1.0.0, so tool's range is
// rewritten without a release.
func TestVersionDoesNotRecordARangeOnlyRewrite(t *testing.T) {
	dir := recordRepo(t, recordOn)
	writeFile(t, filepath.Join(dir, "packages", "tool", "package.json"),
		`{ "name": "tool", "version": "1.0.0", "devDependencies": { "lib": "^1.0.0" } }`)
	code, out := runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)
	assertContains(t, readFile(t, filepath.Join(dir, "packages", "tool", "package.json")), `"^2.0.0"`)
	got, _ := readRecord(t, dir)
	if _, recorded := got["tool"]; recorded || got["lib"] != "2.0.0" {
		t.Errorf("released = %v, want lib and not tool", got)
	}
}

// Canon keeps no record: without the flag, versions.json isn't created.
func TestVersionKeepsNoRecordByDefault(t *testing.T) {
	dir := recordRepo(t, "")
	code, out := runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)
	if _, exists := readRecord(t, dir); exists {
		t.Error("versions.json was written without versioning.record")
	}
}

func TestVersionRecordsNothingForASnapshot(t *testing.T) {
	dir := recordRepo(t, recordOn)
	code, out := runChangerig(t, dir, "version", "--yes", "--snapshot", "canary")
	assertExitZero(t, code, out)
	if got, _ := readRecord(t, dir); len(got) != 0 {
		t.Errorf("a snapshot recorded %v", got)
	}
}

// An unstamped release keeps both: the version source (`packages`) and the
// record.
func TestVersionRecordsAnUnstampedRelease(t *testing.T) {
	dir := recordRepo(t, recordOn)
	code, out := runChangerig(t, dir, "version", "--yes", "--no-stamp")
	assertExitZero(t, code, out)
	got, _ := readRecord(t, dir)
	if got["lib"] != "2.0.0" {
		t.Errorf("released = %v, want lib at 2.0.0", got)
	}
	assertContains(t, readFile(t, filepath.Join(dir, ".changeset", "versions.json")), `"packages": {`)
	assertContains(t, readFile(t, filepath.Join(dir, "packages", "lib", "package.json")), `"1.0.0"`)
}

func TestDoctorChecksTheRecord(t *testing.T) {
	released := func(t *testing.T) string {
		t.Helper()
		dir := recordRepo(t, recordOn)
		code, out := runChangerig(t, dir, "version", "--yes")
		assertExitZero(t, code, out)
		gitCommitAll(t, dir, "release")
		return dir
	}

	t.Run("agrees after a release", func(t *testing.T) {
		dir := released(t)
		_, out := runChangerig(t, dir, "doctor")
		assertContains(t, out, "2 package(s) recorded; versions and tags agree")
	})

	t.Run("flags a manifest edited by hand", func(t *testing.T) {
		dir := released(t)
		writeFile(t, filepath.Join(dir, "packages", "lib", "package.json"), `{ "name": "lib", "version": "5.0.0" }`)
		_, out := runChangerig(t, dir, "doctor")
		assertContains(t, out, "lib (5.0.0 here, 2.0.0 recorded)")
	})

	t.Run("flags a tagged package whose release has no tag", func(t *testing.T) {
		dir := released(t)
		git(t, dir, "tag", "lib@1.0.0", "HEAD~1") // tagged before; 2.0.0 isn't
		_, out := runChangerig(t, dir, "doctor")
		assertContains(t, out, "have no tag: lib@2.0.0")
		assertNotContains(t, out, "app@1.0.1") // app was never tagged at all

		git(t, dir, "tag", "lib@2.0.0")
		_, out = runChangerig(t, dir, "doctor")
		assertContains(t, out, "versions and tags agree")
	})

	t.Run("says nothing without the flag", func(t *testing.T) {
		dir := recordRepo(t, "")
		_, out := runChangerig(t, dir, "doctor")
		assertNotContains(t, out, "release record")
	})
}
