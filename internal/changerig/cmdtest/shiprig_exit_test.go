package cmdtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shiprig's exit code is the contract a CI job reads. Each failure below must
// exit non-zero; #419 reported them exiting 0, but every observation there was
// the exit code of a `| tail` the output was piped through. These pin the
// real behavior, reading the binary's own exit code with no pipe in between.

// An unknown flag is rejected before anything runs.
func TestShiprigUnknownFlagExitsNonZero(t *testing.T) {
	dir := newWorkspace(t)

	code, out := runShiprig(t, dir, "publish", "--registry", "https://example.invalid/", "--yes")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "unknown flag")
}

// A registry that rejects the publish fails the command.
func TestShiprigPublishFailureExitsNonZero(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, nil)
	// Scoped name, plain directory: packages/* is the workspace glob.
	writeFile(t, filepath.Join(dir, "packages", "auth", "package.json"),
		`{ "name": "@acme/auth", "version": "1.0.0" }`)
	initChangesets(t, dir)
	// Precondition: the package is discovered, or "nothing to publish" would
	// exit 0 and this test would pass without reaching the failure.
	if code, out := runShiprig(t, dir, "info"); code != 0 || !strings.Contains(out, "@acme/auth") {
		t.Fatalf("precondition: shiprig should discover @acme/auth (exit %d):\n%s", code, out)
	}

	// A fake npm: the package is not on the registry yet (view → E404), and
	// the registry then refuses the publish, as in #419.
	bin := filepath.Join(dir, "fakebin")
	writeFile(t, filepath.Join(bin, "npm"), `#!/bin/sh
case "$1" in
  view) echo "npm error code E404" >&2; exit 1 ;;
  publish) echo "npm error 404 Not Found - PUT https://registry.npmjs.org/@acme%2fauth" >&2; exit 1 ;;
  *) exit 0 ;;
esac
`)
	if err := os.Chmod(filepath.Join(bin, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	code, out := runShiprig(t, dir, "publish", "--yes")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "404 Not Found")
}

// A failed pipeline step fails the release, and the exit code says so even
// though shiprig reports the failure and a resume hint itself.
func TestShiprigReleaseStepFailureExitsNonZero(t *testing.T) {
	dir := newWorkspace(t)
	writeFile(t, filepath.Join(dir, ".changeset", "release.jsonc"),
		`{ "order": ["build"], "steps": { "build": { "run": "exit 3" } } }`)
	gitInit(t, dir)

	code, out := runShiprig(t, dir, "release", "--yes")

	assertExitNonZero(t, code, out)
	assertContains(t, out, "step 'build' failed")
}
