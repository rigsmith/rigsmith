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
	cmd := exec.Command(shiprigBin, "packages", "list", "--json")
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
