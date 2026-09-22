package cmdtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// @changesets v3's output contract (CHANGESETS_OUTPUT / --output): one NDJSON
// git-tag event per tag the command creates. shiprig-action reads these to
// push the tags and create a GitHub release for each.

type tagEvent struct {
	Type        string `json:"type"`
	Tag         string `json:"tag"`
	PackageName string `json:"packageName"`
}

func readTagEvents(t *testing.T, path string) []tagEvent {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var events []tagEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e tagEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("not an NDJSON event: %q: %v", line, err)
		}
		events = append(events, e)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Tag < events[j].Tag })
	return events
}

// withRemote gives dir a bare `origin`, so "already on the remote" can be
// tested and a push would be visible.
func withRemote(t *testing.T, dir string) string {
	t.Helper()
	remote := filepath.Join(tempDir(t), "remote.git")
	git(t, dir, "init", "--bare", remote)
	git(t, dir, "remote", "add", "origin", remote)
	git(t, dir, "push", "origin", "HEAD")
	return remote
}

func remoteTags(t *testing.T, remote string) string {
	t.Helper()
	return git(t, remote, "tag", "--list")
}

func TestTagWritesChangesetsOutputEvents(t *testing.T) {
	dir := tagWorkspace(t)
	remote := withRemote(t, dir)
	// pkg-b's tag is on the remote only: canon counts it as existing.
	git(t, dir, "tag", "-a", "pkg-b@2.1.0", "-m", "pkg-b@2.1.0")
	git(t, dir, "push", "origin", "pkg-b@2.1.0")
	git(t, dir, "tag", "-d", "pkg-b@2.1.0")

	events := filepath.Join(tempDir(t), "events.ndjson")
	t.Setenv("CHANGESETS_OUTPUT", events)

	code, out := runShiprig(t, dir, "tag")
	assertExitZero(t, code, out)

	want := []tagEvent{{"git-tag", "pkg-a@1.0.0", "pkg-a"}}
	if got := readTagEvents(t, events); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("events = %+v, want %+v", got, want)
	}

	// A re-run creates nothing, so it appends nothing.
	code, out = runShiprig(t, dir, "tag")
	assertExitZero(t, code, out)
	if got := readTagEvents(t, events); len(got) != 1 {
		t.Errorf("a re-run appended events for existing tags: %+v", got)
	}
	if strings.Contains(remoteTags(t, remote), "pkg-a@1.0.0") {
		t.Error("tag never pushes")
	}
}

func TestPublishWithOutputTagsLocallyAndLeavesThePushToTheCaller(t *testing.T) {
	dir := tagWorkspace(t)
	remote := withRemote(t, dir)
	fakeNpmPublishes(t, dir)
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"),
		`{ "updateInternalDependencies": "patch", "node": { "oidc": "off" } }`)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "config")

	events := filepath.Join(tempDir(t), "events.ndjson")
	code, out := runShiprig(t, dir, "publish", "--yes", "--output", events)
	assertExitZero(t, code, out)

	got := readTagEvents(t, events)
	want := []tagEvent{{"git-tag", "pkg-a@1.0.0", "pkg-a"}, {"git-tag", "pkg-b@2.1.0", "pkg-b"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("events = %+v, want %+v", got, want)
	}
	if tags := tagList(t, dir); len(tags) != 2 {
		t.Errorf("tags should be created locally, got %v", tags)
	}
	if rt := remoteTags(t, remote); rt != "" {
		t.Errorf("with --output the caller pushes; shiprig pushed %q", rt)
	}
}

// fakeNpmPublishes puts an npm on PATH for which nothing is published yet and
// every publish succeeds.
func fakeNpmPublishes(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join(dir, "..", filepath.Base(dir)+"-fakebin")
	if runtime.GOOS == "windows" {
		writeFile(t, filepath.Join(bin, "npm.cmd"), "@echo off\r\n"+
			"if \"%1\"==\"view\" (echo npm error code E404 1>&2 & exit /b 1)\r\n"+
			"exit /b 0\r\n")
	} else {
		writeFile(t, filepath.Join(bin, "npm"), "#!/bin/sh\ncase \"$1\" in view) echo \"npm error code E404\" >&2; exit 1 ;; *) exit 0 ;; esac\n")
		if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A tag whose event can't be written is removed again: left behind, a retry
// would find it existing, skip it, and never report it to the caller.
func TestTagRemovesATagItCouldNotReport(t *testing.T) {
	dir := tagWorkspace(t)
	unwritable := tempDir(t) // a directory: opening it for append fails

	code, out := runShiprig(t, dir, "tag", "--output", unwritable)
	assertExitNonZero(t, code, out)
	// (The message says "tag removed", but the error box wraps it; the tag
	// list is the evidence.)
	if tags := tagList(t, dir); len(tags) != 0 {
		t.Fatalf("an unreported tag was left behind: %v", tags)
	}

	events := filepath.Join(tempDir(t), "events.ndjson")
	code, out = runShiprig(t, dir, "tag", "--output", events)
	assertExitZero(t, code, out)
	if got := readTagEvents(t, events); len(got) != 2 {
		t.Errorf("the retry should create and report both tags, got %+v", got)
	}
}
