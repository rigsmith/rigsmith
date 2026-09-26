package cmdtest

import (
	"path/filepath"
	"testing"
)

// A changeset naming its package in single quotes, valid YAML that
// @changesets reads, versions like any other (rig/node's `'@jcamp/rig': patch`).
func TestVersionReadsASingleQuotedPackage(t *testing.T) {
	dir := tempDir(t)
	writeFile(t, filepath.Join(dir, "package.json"), `{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	writeFile(t, filepath.Join(dir, "package-lock.json"), "{}")
	writeFile(t, filepath.Join(dir, "packages", "rig", "package.json"), `{ "name": "@acme/rig", "version": "1.0.0" }`)
	initChangesets(t, dir)
	writeFile(t, filepath.Join(dir, ".changeset", "harden.md"), "---\n'@acme/rig': patch\n---\n\nHarden the shell\n")
	gitInit(t, dir)

	code, out := runChangerig(t, dir, "version", "--yes")
	assertExitZero(t, code, out)
	assertContains(t, readFile(t, filepath.Join(dir, "packages", "rig", "package.json")), `"1.0.1"`)
}
