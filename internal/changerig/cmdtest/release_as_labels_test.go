package cmdtest

import (
	"path/filepath"
	"testing"
)

// A version override labels the release by the move it makes, major if the
// major changes, minor if the minor does, else patch, not by the bump its
// changesets asked for; and an untyped changelog lists the change that decided
// it under that bump. --release-as 1.0.0 on a patch is a 1.0.0 release, and
// the plan and the changelog both say major.
func TestReleaseAsLabelsTheActualMove(t *testing.T) {
	for _, tc := range []struct {
		name, from, bump, to, label, heading string
	}{
		{"minor to 1.0.0", "0.3.0", "minor", "1.0.0", "major", "Major"},
		{"patch to 1.0.0", "0.3.0", "patch", "1.0.0", "major", "Major"},
		{"patch to 2.0.0", "1.2.0", "patch", "2.0.0", "major", "Major"},
		{"patch to 1.3.0", "1.2.0", "patch", "1.3.0", "minor", "Minor"},
		{"major on 0.x to 0.4.0", "0.3.0", "major", "0.4.0", "minor", "Minor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempDir(t)
			writeNpmWorkspace(t, dir, map[string]string{"lib": tc.from})
			writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{}`)
			writeChangeset(t, dir, "lib-change", "lib", tc.bump, "The change")
			writeChangeset(t, dir, "lib-fix", "lib", "patch", "A smaller fix")
			gitInit(t, dir)
			code, out := runChangerig(t, dir, "version", "--dry-run", "--changelog", "--release-as", "lib="+tc.to)
			assertExitZero(t, code, out)
			assertContains(t, out, tc.label+"  lib  "+tc.from+" → "+tc.to)
			assertContains(t, out, "## "+tc.to+"\n\n### "+tc.heading+" Changes\n\n- The change")
			if tc.bump != "patch" {
				// A smaller change keeps its own heading.
				assertContains(t, out, "### Patch Changes\n\n- A smaller fix")
			}
		})
	}
}

// A fixed group forced past its coordinated bump is labelled by the move too,
// every member of it.
func TestReleaseAsLabelsAGroupByTheActualMove(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"a": "1.2.0", "b": "1.2.0"})
	writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{ "fixed": [["a", "b"]] }`)
	writeChangeset(t, dir, "a-change", "a", "patch", "A fix")
	gitInit(t, dir)
	code, out := runChangerig(t, dir, "version", "--dry-run", "--changelog", "--release-as", "a=2.0.0", "--release-as", "b=2.0.0")
	assertExitZero(t, code, out)
	assertContains(t, out, "major  a  1.2.0 → 2.0.0")
	assertContains(t, out, "major  b  1.2.0 → 2.0.0")
	assertContains(t, out, "## 2.0.0\n\n### Major Changes\n\n- A fix")
}

// A prerelease or snapshot override is judged on its stable part: a
// prerelease on a new base labels that move as ever, one that stays on its
// base (1.1.0-next.0 → 1.1.0-next.1) keeps the planned bump, and a snapshot
// below the current version (0.0.0-canary-…) says nothing about the bump.
func TestPrereleaseAndSnapshotKeepTheirLabels(t *testing.T) {
	t.Run("prerelease", func(t *testing.T) {
		dir := tempDir(t)
		writeNpmWorkspace(t, dir, map[string]string{"lib": "1.0.0"})
		writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{}`)
		writeChangeset(t, dir, "one", "lib", "minor", "A feature")
		gitInit(t, dir)
		code, out := runChangerig(t, dir, "pre", "enter", "next")
		assertExitZero(t, code, out)
		code, out = runChangerig(t, dir, "version", "--yes")
		assertExitZero(t, code, out)
		assertContains(t, out, "minor  lib  1.0.0 → 1.1.0-next.0")
		// Another minor stays on the 1.1.0 base: no move to judge by, so
		// the planned minor stands (not a patch for "same base").
		writeChangeset(t, dir, "two", "lib", "minor", "Another feature")
		code, out = runChangerig(t, dir, "version", "--dry-run", "--changelog")
		assertExitZero(t, code, out)
		assertContains(t, out, "minor  lib  1.1.0-next.0 → 1.1.0-next.1")
		assertContains(t, out, "### Minor Changes\n\n- Another feature")
	})
	t.Run("snapshot", func(t *testing.T) {
		dir := tempDir(t)
		writeNpmWorkspace(t, dir, map[string]string{"lib": "1.2.0"})
		writeFile(t, filepath.Join(dir, ".changeset", "config.json"), `{}`)
		writeChangeset(t, dir, "one", "lib", "minor", "A feature")
		gitInit(t, dir)
		code, out := runChangerig(t, dir, "version", "--snapshot", "canary", "--dry-run", "--changelog")
		assertExitZero(t, code, out)
		assertContains(t, out, "minor  lib  1.2.0 → 0.0.0-canary")
		assertContains(t, out, "### Minor Changes\n\n- A feature")
	})
}
