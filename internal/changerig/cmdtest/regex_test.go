package cmdtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestVersionBumpsRegexPackage drives the whole loop end-to-end for a
// config-declared regex package: discovery → plan → SetVersion → changelog.
func TestVersionBumpsRegexPackage(t *testing.T) {
	dir := tempDir(t)
	writeConfigJSON(t, dir, `{
	  "regex": {
	    "packages": [
	      { "name": "chart", "file": "deploy/Chart.yaml", "pattern": "version: (?<version>.*)" }
	    ]
	  }
	}`)
	writeFile(t, filepath.Join(dir, "deploy", "Chart.yaml"),
		"apiVersion: v2\nname: app\nversion: 1.0.0\nappVersion: \"keep\"\n")
	writeChangeset(t, dir, "brave-otters-dance", "chart", "minor", "Add a feature")

	code, out := runChangerig(t, dir, "version")
	assertExitZero(t, code, out)

	chart := readFile(t, filepath.Join(dir, "deploy", "Chart.yaml"))
	if !strings.Contains(chart, "version: 1.1.0") {
		t.Errorf("Chart.yaml not bumped to 1.1.0:\n%s", chart)
	}
	// Nothing else in the file moved.
	if !strings.Contains(chart, `appVersion: "keep"`) {
		t.Errorf("rewrite disturbed the rest of the file:\n%s", chart)
	}
	// A changelog landed next to the versioned file.
	if !fileExists(filepath.Join(dir, "deploy", "CHANGELOG.md")) {
		t.Error("expected CHANGELOG.md next to the regex-versioned file")
	}
	// The changeset was consumed.
	if files := changesetFiles(t, dir); len(files) != 0 {
		t.Errorf("changeset not removed: %v", files)
	}
}
