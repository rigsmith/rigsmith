package engine

import (
	"encoding/json"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

// The fixtures embed real machine paths into TOML and JSON, and on Windows that
// is C:\Users\… — which a TOML basic string and a JSON string both read as escape
// sequences. When they did, the documents stopped parsing, sync skipped the
// config as unreadable, and four tests failed for a reason that had nothing to
// do with what they were checking. This runs on every host so the spelling
// cannot regress on the two thirds of CI that would not notice.
func TestFixturePathsSurviveAWindowsShapedPath(t *testing.T) {
	win := `C:\Users\runneradmin\AppData\Local\Temp\Test123\Git\thing`
	doc := "[projects." + tomlPath(win) + "]\ntrust_level = \"trusted\"\n"
	var out map[string]any
	if err := toml.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("literal-string table key did not parse: %v\n%s", err, doc)
	}
	projects, _ := out["projects"].(map[string]any)
	if _, ok := projects[win]; !ok {
		t.Fatalf("key did not round-trip: %v", projects)
	}
	// And the naive basic-string form is genuinely broken, which is the point.
	bad := "[projects.\"" + win + "\"]\ntrust_level = \"trusted\"\n"
	if err := toml.Unmarshal([]byte(bad), &out); err == nil {
		t.Error("expected the basic-string form to fail on a Windows path")
	}

	line := `{"cwd":` + jsonPath(win) + `}`
	var m map[string]string
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("jsonPath did not produce valid JSON: %v (%s)", err, line)
	}
	if m["cwd"] != win {
		t.Errorf("cwd = %q, want %q", m["cwd"], win)
	}
	if !strings.Contains(line, `\\`) {
		t.Errorf("expected escaped backslashes in %s", line)
	}
}
