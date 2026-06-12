package jsonc

import (
	"strings"
	"testing"
)

// Port of rig's .NET JsoncEditorTests (13 cases). The C# tests validate
// results through RigConfig.Parse; here a minimal equivalent struct is
// parsed with the package's own JSONC Unmarshal.

type editorTestCoverage struct {
	License   string `json:"license"`
	Collector string `json:"collector"`
	Open      bool   `json:"open"`
	Full      bool   `json:"full"`
	Min       int    `json:"min"`
}

type editorTestConfig struct {
	DefaultProject string              `json:"defaultProject"`
	Commands       map[string]string   `json:"commands"`
	Coverage       *editorTestCoverage `json:"coverage"`
}

func parseConfig(t *testing.T, text string) editorTestConfig {
	t.Helper()
	var cfg editorTestConfig
	if err := Unmarshal([]byte(text), &cfg); err != nil {
		t.Fatalf("edited document is not valid JSONC: %v\n%s", err, text)
	}
	return cfg
}

func mustContain(t *testing.T, result, want string) {
	t.Helper()
	if !strings.Contains(result, want) {
		t.Errorf("result should contain %q\n%s", want, result)
	}
}

func mustNotContain(t *testing.T, result, want string) {
	t.Helper()
	if strings.Contains(result, want) {
		t.Errorf("result should not contain %q\n%s", want, result)
	}
}

func TestReplacesExistingValueAndPreservesComments(t *testing.T) {
	src := `{
  // pick the app to run
  "defaultProject": "Old", // trailing note
  "commands": { "deploy": "./d.sh" }
}`

	result, ok := SetTopLevelString(src, "defaultProject", "New")
	if !ok {
		t.Fatal("SetTopLevelString returned false")
	}

	mustContain(t, result, `"defaultProject": "New"`)
	mustNotContain(t, result, `"Old"`)
	mustContain(t, result, "// pick the app to run") // leading comment kept
	mustContain(t, result, "// trailing note")       // trailing comment kept
	mustContain(t, result, `"deploy": "./d.sh"`)     // other keys kept

	// still valid + correct after the edit
	if got := parseConfig(t, result).DefaultProject; got != "New" {
		t.Errorf("defaultProject = %q, want %q", got, "New")
	}
}

func TestInsertsWhenAbsentKeepingCommentsAndMembers(t *testing.T) {
	src := `{
  // top comment
  "commands": { "deploy": "./d.sh" }
}`

	result, ok := SetTopLevelString(src, "defaultProject", "App")
	if !ok {
		t.Fatal("SetTopLevelString returned false")
	}

	mustContain(t, result, "// top comment")
	// The comment must stay attached to "commands", not get stolen by the
	// newly-inserted key: defaultProject is inserted *before* the comment.
	if strings.Index(result, "defaultProject") >= strings.Index(result, "// top comment") {
		t.Errorf("defaultProject should precede the comment\n%s", result)
	}
	if strings.Index(result, "// top comment") >= strings.Index(result, "commands") {
		t.Errorf("the comment should precede commands\n%s", result)
	}

	cfg := parseConfig(t, result)
	if cfg.DefaultProject != "App" {
		t.Errorf("defaultProject = %q, want %q", cfg.DefaultProject, "App")
	}
	if _, hasDeploy := cfg.Commands["deploy"]; !hasDeploy {
		t.Errorf("commands should contain deploy\n%s", result)
	}
}

func TestDoesNotMatchANestedPropertyOfTheSameName(t *testing.T) {
	src := `{
  "test": { "defaultProject": "INNER" },
  "defaultProject": "OUTER"
}`

	result, ok := SetTopLevelString(src, "defaultProject", "CHANGED")
	if !ok {
		t.Fatal("SetTopLevelString returned false")
	}

	mustContain(t, result, `"INNER"`) // nested untouched
	mustContain(t, result, `"CHANGED"`)
	mustNotContain(t, result, `"OUTER"`)
}

func TestInsertsIntoAnEmptyObject(t *testing.T) {
	result, ok := SetTopLevelString("{}", "defaultProject", "App")
	if !ok {
		t.Fatal("SetTopLevelString returned false")
	}
	if got := parseConfig(t, result).DefaultProject; got != "App" {
		t.Errorf("defaultProject = %q, want %q", got, "App")
	}
}

func TestHandlesUnicodeWithoutCorruptingByteOffsets(t *testing.T) {
	// An em-dash before the target ensures byte offsets != char offsets.
	src := `{
  "note": "build — bundle",
  "defaultProject": "Old"
}`

	result, ok := SetTopLevelString(src, "defaultProject", "New")
	if !ok {
		t.Fatal("SetTopLevelString returned false")
	}
	mustContain(t, result, "build — bundle") // unicode intact
	if got := parseConfig(t, result).DefaultProject; got != "New" {
		t.Errorf("defaultProject = %q, want %q", got, "New")
	}
}

func TestReturnsFalseOnMalformedInput(t *testing.T) {
	if _, ok := SetTopLevelString("{ not json", "x", "y"); ok {
		t.Error("expected false for malformed input")
	}
}

// ---- Nested (depth-2) + typed writes ----

func TestReplacesANestedValueAndKeepsSiblingKeysAndComments(t *testing.T) {
	src := `{
  // coverage prefs
  "coverage": { "license": "OLD", "collector": "mtp" },
  "defaultProject": "App"
}`

	result, ok := Set(src, []string{"coverage", "license"}, `"NEW"`)
	if !ok {
		t.Fatal("Set returned false")
	}

	mustContain(t, result, `"license": "NEW"`)
	mustNotContain(t, result, "OLD")
	mustContain(t, result, `"collector": "mtp"`) // sibling kept
	mustContain(t, result, "// coverage prefs")  // comment kept
	cov := parseConfig(t, result).Coverage
	if cov == nil || cov.License != "NEW" || cov.Collector != "mtp" {
		t.Errorf("coverage = %+v, want license=NEW collector=mtp", cov)
	}
}

func TestInsertsANestedKeyIntoAnExistingParentObject(t *testing.T) {
	src := `{
  "coverage": { "license": "KEY" }
}`

	result, ok := Set(src, []string{"coverage", "open"}, "true")
	if !ok {
		t.Fatal("Set returned false")
	}

	cov := parseConfig(t, result).Coverage
	if cov == nil {
		t.Fatalf("coverage missing\n%s", result)
	}
	if cov.License != "KEY" { // existing sibling intact
		t.Errorf("license = %q, want %q", cov.License, "KEY")
	}
	if !cov.Open {
		t.Errorf("open = false, want true\n%s", result)
	}
}

func TestCreatesTheParentObjectWhenAbsent(t *testing.T) {
	src := `{
  "defaultProject": "App"
}`

	result, ok := Set(src, []string{"coverage", "min"}, "80")
	if !ok {
		t.Fatal("Set returned false")
	}

	cfg := parseConfig(t, result)
	if cfg.DefaultProject != "App" { // untouched
		t.Errorf("defaultProject = %q, want %q", cfg.DefaultProject, "App")
	}
	if cfg.Coverage == nil || cfg.Coverage.Min != 80 {
		t.Errorf("coverage = %+v, want min=80\n%s", cfg.Coverage, result)
	}
}

func TestCreatesNestedKeyInAnEmptyRootAndEmptyParent(t *testing.T) {
	fromEmptyRoot, ok := Set("{}", []string{"coverage", "open"}, "true")
	if !ok {
		t.Fatal("Set returned false for empty root")
	}
	if cov := parseConfig(t, fromEmptyRoot).Coverage; cov == nil || !cov.Open {
		t.Errorf("coverage.open should be true\n%s", fromEmptyRoot)
	}

	fromEmptyParent, ok := Set(`{ "coverage": {} }`, []string{"coverage", "full"}, "true")
	if !ok {
		t.Fatal("Set returned false for empty parent")
	}
	if cov := parseConfig(t, fromEmptyParent).Coverage; cov == nil || !cov.Full {
		t.Errorf("coverage.full should be true\n%s", fromEmptyParent)
	}
}

func TestRefusesToClobberANonObjectParent(t *testing.T) {
	// "coverage" is a string here — merging a child would destroy it, so bail.
	if _, ok := Set(`{ "coverage": "oops" }`, []string{"coverage", "open"}, "true"); ok {
		t.Error("expected false when the parent is not an object")
	}
}

func TestInsertsInlineIntoASingleLineParentObject(t *testing.T) {
	src := `{ "coverage": { "license": "" } }`

	result, ok := Set(src, []string{"coverage", "open"}, "true")
	if !ok {
		t.Fatal("Set returned false")
	}

	mustNotContain(t, result, "\n") // stays on one line — no stray newline
	cov := parseConfig(t, result).Coverage
	if cov == nil {
		t.Fatalf("coverage missing\n%s", result)
	}
	if !cov.Open {
		t.Errorf("open = false, want true\n%s", result)
	}
	if cov.License != "" {
		t.Errorf("license = %q, want empty\n%s", cov.License, result)
	}
}

func TestToleratesALeadingBOM(t *testing.T) {
	src := "\uFEFF{ \"defaultProject\": \"Old\" }"

	result, ok := Set(src, []string{"defaultProject"}, `"New"`)
	if !ok {
		t.Fatal("Set returned false")
	}
	if got := parseConfig(t, result).DefaultProject; got != "New" {
		t.Errorf("defaultProject = %q, want %q", got, "New")
	}
}
