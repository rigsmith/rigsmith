package cmdtest

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// `shiprig packages list --json` is a contract: shiprig-action reads it in
// place of an npm-only workspace lookup (package dirs, versions before and
// after `version`, where each changelog lives, private). Field names matter.
func TestShiprigPackagesListJSON(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0", "pkg-b": "2.0.0"})
	writeFile(t, filepath.Join(dir, "packages", "app", "package.json"),
		`{ "name": "app", "version": "0.1.0", "private": true }`)
	initChangesets(t, dir)
	writeChangeset(t, dir, "cs", "pkg-a", "minor", "a feature")

	// stdout alone: that is what a script parses, and nothing but the JSON
	// may be on it.
	cmd := exec.CommandContext(t.Context(), shiprigBin, "packages", "list", "--json")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("packages list --json: %v\nstderr:\n%s", err, stderr.String())
	}
	out := string(stdout)

	var got struct {
		Packages []map[string]any `json:"packages"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	byName := map[string]map[string]any{}
	for _, p := range got.Packages {
		byName[p["name"].(string)] = p
	}

	want := map[string]map[string]any{
		"pkg-a": {"ecosystem": "node", "dir": "packages/pkg-a", "version": "1.0.0",
			"nextVersion": "1.1.0", "bump": "minor", "private": false, "ignored": false,
			"changelog": "packages/pkg-a/CHANGELOG.md"},
		"pkg-b": {"ecosystem": "node", "dir": "packages/pkg-b", "version": "2.0.0",
			"private": false, "ignored": false, "changelog": "packages/pkg-b/CHANGELOG.md"},
		// Private and unversioned (@changesets v3), so reported ignored.
		"app": {"ecosystem": "node", "dir": "packages/app", "version": "0.1.0",
			"private": true, "ignored": true, "changelog": "packages/app/CHANGELOG.md"},
	}
	if len(byName) != len(want) {
		t.Fatalf("got %d packages, want %d:\n%s", len(byName), len(want), out)
	}
	for name, fields := range want {
		p, ok := byName[name]
		if !ok {
			t.Errorf("%s missing:\n%s", name, out)
			continue
		}
		for k, v := range fields {
			if p[k] != v {
				t.Errorf("%s.%s = %v, want %v", name, k, p[k], v)
			}
		}
		// Not releasing: no nextVersion/bump keys at all, not empty strings.
		if _, releasing := fields["nextVersion"]; !releasing {
			if _, has := p["nextVersion"]; has {
				t.Errorf("%s: nextVersion present on a package that isn't releasing", name)
			}
		}
		if _, has := p["changelogSection"]; has {
			t.Errorf("%s: changelogSection is only for a shared (stackspace) changelog", name)
		}
	}
}

// Listing packages isn't a changesets command: a repo with no .changeset/ is
// listed with nothing releasing. `status` still requires the folder, as
// `changeset status` does.
func TestShiprigPackagesListJSONWithoutChangesetDir(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})

	cmd := exec.CommandContext(t.Context(), shiprigBin, "packages", "list", "--json")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("packages list --json without .changeset/: %v\nstderr:\n%s", err, stderr.String())
	}
	var got struct {
		Packages []map[string]any `json:"packages"`
	}
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(got.Packages) != 1 || got.Packages[0]["name"] != "pkg-a" || got.Packages[0]["version"] != "1.0.0" {
		t.Fatalf("packages = %+v, want just pkg-a@1.0.0", got.Packages)
	}
	if _, releasing := got.Packages[0]["nextVersion"]; releasing {
		t.Errorf("nothing can be pending without .changeset/: %+v", got.Packages[0])
	}

	code, out := runChangerig(t, dir, "status")
	assertExitNonZero(t, code, out)
}

// With the changeset config outside .changeset/ (a root changerig.json) and
// the source set to "both", a missing .changeset/ must not hide the releases
// the commits imply.
func TestShiprigPackagesListJSONWithoutChangesetDirStillReadsCommits(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	writeFile(t, filepath.Join(dir, "changerig.json"), `{ "versioning": { "source": "both" } }`)
	gitInit(t, dir)
	writeFile(t, filepath.Join(dir, "packages", "pkg-a", "index.js"), "export {}\n")
	gitCommitAll(t, dir, "feat: a feature in pkg-a")

	cmd := exec.CommandContext(t.Context(), shiprigBin, "packages", "list", "--json")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("packages list --json: %v\nstderr:\n%s", err, stderr.String())
	}
	var got struct {
		Packages []map[string]any `json:"packages"`
	}
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if len(got.Packages) != 1 || got.Packages[0]["name"] != "pkg-a" {
		t.Fatalf("packages = %+v, want just pkg-a", got.Packages)
	}
	if next, _ := got.Packages[0]["nextVersion"].(string); next == "" {
		t.Errorf("the feat commit should release pkg-a even without .changeset/: %+v", got.Packages[0])
	}
}

// status is a changesets command and keeps requiring .changeset/, as
// `changeset status` does, even when the config lives outside it (a root
// changerig.json), which is the case where the package listing's leniency
// must not leak into it.
func TestStatusStillRequiresTheChangesetDir(t *testing.T) {
	dir := tempDir(t)
	writeNpmWorkspace(t, dir, map[string]string{"pkg-a": "1.0.0"})
	writeFile(t, filepath.Join(dir, "changerig.json"), `{}`)

	code, out := runChangerig(t, dir, "status")
	assertExitNonZero(t, code, out)
	assertContains(t, out, "reading changesets")
}
