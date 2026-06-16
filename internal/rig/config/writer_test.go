// Port of the .NET rig's ConfigWriterTests (the comment-preserving .rig.json
// writer), plus the ConfigWriter cases from ConventionTests.
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRigJSON(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func parseT(t *testing.T, src string) Config {
	t.Helper()
	cfg, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v\nsource:\n%s", err, src)
	}
	return cfg
}

func TestAddRepoExclude_CreatesFileAndAppends(t *testing.T) {
	root := t.TempDir()

	// No .rig.json yet: a fresh file is written with the schema + exclude.
	path, ok := AddRepoExclude(root, nil, "testdata/*")
	if !ok {
		t.Fatal("AddRepoExclude on a fresh repo should write")
	}
	if cfg := parseT(t, readFileT(t, path)); len(cfg.Exclude) != 1 || cfg.Exclude[0] != "testdata/*" {
		t.Fatalf("exclude = %v, want [testdata/*]", cfg.Exclude)
	}

	// Adding a second glob keeps the first (deduped + sorted).
	if _, ok := AddRepoExclude(root, []string{"testdata/*"}, "changerig"); !ok {
		t.Fatal("second AddRepoExclude should write")
	}
	cfg := parseT(t, readFileT(t, path))
	if got := strings.Join(cfg.Exclude, ","); got != "changerig,testdata/*" {
		t.Fatalf("exclude = %q, want sorted [changerig,testdata/*]", got)
	}

	// Re-adding an existing glob is a no-op success.
	if _, ok := AddRepoExclude(root, cfg.Exclude, "changerig"); !ok {
		t.Fatal("re-adding an existing glob should report ok")
	}
	if cfg2 := parseT(t, readFileT(t, path)); len(cfg2.Exclude) != 2 {
		t.Fatalf("re-add changed the list: %v", cfg2.Exclude)
	}
}

func TestRemoveRepoExclude_DropsAGlobAndPreservesComments(t *testing.T) {
	root := t.TempDir()
	path := writeRigJSON(t, root, "{\n  // keep me\n  \"exclude\": [\"a\", \"b\"]\n}\n")

	if _, ok := RemoveRepoExclude(root, []string{"a", "b"}, "a"); !ok {
		t.Fatal("RemoveRepoExclude should write")
	}
	out := readFileT(t, path)
	if cfg := parseT(t, out); len(cfg.Exclude) != 1 || cfg.Exclude[0] != "b" {
		t.Fatalf("exclude = %v, want [b]", cfg.Exclude)
	}
	if !strings.Contains(out, "// keep me") {
		t.Errorf("the comment-preserving writer dropped a comment:\n%s", out)
	}
}

// ---- ConfigWriterTests ----

func TestConfigWriter_FreshFileIsWrittenWithSchemaAndANestedValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	if !SetString(path, []string{"coverage", "license"}, "PRO-KEY") {
		t.Fatal("SetString returned false")
	}

	text := readFileT(t, path)
	if !strings.Contains(text, SchemaURL) {
		t.Fatalf("fresh file missing the $schema URL:\n%s", text)
	}
	cfg := parseT(t, text)
	if cfg.Coverage == nil || cfg.Coverage.License != "PRO-KEY" {
		t.Fatalf("coverage.license = %+v, want PRO-KEY", cfg.Coverage)
	}
}

func TestConfigWriter_SubsequentWritesSpliceIntoTheExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	if !SetString(path, []string{"coverage", "license"}, "PRO-KEY") {
		t.Fatal("SetString(coverage.license) returned false")
	}
	if !SetBool(path, []string{"coverage", "open"}, true) {
		t.Fatal("SetBool(coverage.open) returned false")
	}
	if !SetNumber(path, []string{"coverage", "min"}, 80) {
		t.Fatal("SetNumber(coverage.min) returned false")
	}
	if !SetString(path, []string{"defaultProject"}, "App") {
		t.Fatal("SetString(defaultProject) returned false")
	}

	cfg := parseT(t, readFileT(t, path))
	if cfg.DefaultProject != "App" {
		t.Fatalf("defaultProject = %q, want App", cfg.DefaultProject)
	}
	if cfg.Coverage == nil || cfg.Coverage.License != "PRO-KEY" {
		t.Fatalf("coverage.license = %+v, want PRO-KEY (earlier writes must survive later ones)", cfg.Coverage)
	}
	if cfg.Coverage.Open == nil || !*cfg.Coverage.Open {
		t.Fatalf("coverage.open = %v, want true", cfg.Coverage.Open)
	}
	if cfg.Coverage.Min == nil || *cfg.Coverage.Min != 80 {
		t.Fatalf("coverage.min = %v, want 80", cfg.Coverage.Min)
	}
}

func TestConfigWriter_RefusesToClobberAnExistingFileItCannotEditInPlace(t *testing.T) {
	dir := t.TempDir()
	// "coverage" is a non-object here, so coverage.open can't be spliced.
	path := writeRigJSON(t, dir, `{ "defaultProject": "Keep", "coverage": "weird" }`)

	if SetBool(path, []string{"coverage", "open"}, true) {
		t.Fatal("SetBool succeeded, want a refusal")
	}

	text := readFileT(t, path)
	if !strings.Contains(text, `"Keep"`) || !strings.Contains(text, `"weird"`) {
		t.Fatalf("file was modified, want it untouched:\n%s", text)
	}
}

func TestConfigWriter_WhitespaceOnlyExistingFileIsWrittenFresh(t *testing.T) {
	dir := t.TempDir()
	path := writeRigJSON(t, dir, "   \n") // e.g. a stray `touch .rig.json`

	if !SetString(path, []string{"defaultProject"}, "App") {
		t.Fatal("SetString returned false")
	}
	if cfg := parseT(t, readFileT(t, path)); cfg.DefaultProject != "App" {
		t.Fatalf("defaultProject = %q, want App", cfg.DefaultProject)
	}
}

func TestConfigWriter_SetRepoStringReturnsTheRepoConfigPath(t *testing.T) {
	dir := t.TempDir()

	returned, ok := SetRepoString(dir, "defaultProject", "App")
	if !ok {
		t.Fatalf("SetRepoString reported failure writing to a fresh dir")
	}

	if want := filepath.Join(dir, FileName); returned != want {
		t.Fatalf("returned path = %q, want %q", returned, want)
	}
	if cfg := parseT(t, readFileT(t, returned)); cfg.DefaultProject != "App" {
		t.Fatalf("defaultProject = %q, want App", cfg.DefaultProject)
	}
}

func TestConfigWriter_SetRepoStringReportsFailureWithoutClobbering(t *testing.T) {
	dir := t.TempDir()
	// A non-empty .rig.json whose root is a JSON array (valid JSON, but not an
	// object) can't have a top-level property spliced in — confkit.Writer.Set
	// declines rather than clobber it (jsonc.Set returns false: "not a JSON
	// object"). SetRepoString must surface that as ok==false.
	const original = `[1, 2, 3]`
	path := writeRigJSON(t, dir, original)

	returned, ok := SetRepoString(dir, "defaultProject", "App")
	if ok {
		t.Fatal("SetRepoString reported success, want ok==false for an unspliceable file")
	}
	if want := filepath.Join(dir, FileName); returned != want {
		t.Fatalf("returned path = %q, want %q even on failure", returned, want)
	}
	if text := readFileT(t, path); text != original {
		t.Fatalf("file was modified, want it left intact:\ngot:  %q\nwant: %q", text, original)
	}
}

// ---- ConventionTests (ConfigWriter cases) ----

func TestConvention_ConfigWriterCreatesFileWithProperty(t *testing.T) {
	dir := t.TempDir()

	path, _ := SetRepoString(dir, "defaultProject", "App.Desktop")

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProject != "App.Desktop" {
		t.Fatalf("defaultProject = %q, want App.Desktop", cfg.DefaultProject)
	}
}

func TestConvention_ConfigWriterUpdatesExistingFilePreservingOtherKeys(t *testing.T) {
	dir := t.TempDir()
	writeRigJSON(t, dir, `{ "defaultProject": "Old", "commands": { "deploy": "./d.sh" } }`)

	SetRepoString(dir, "defaultProject", "New")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProject != "New" {
		t.Fatalf("defaultProject = %q, want New", cfg.DefaultProject)
	}
	if _, ok := cfg.Commands["deploy"]; !ok {
		t.Fatalf("commands.deploy went missing, want it untouched: %+v", cfg.Commands)
	}
}

func TestConvention_ConfigWriterPreservesCommentsOnExistingFile(t *testing.T) {
	dir := t.TempDir()
	writeRigJSON(t, dir, "{\n  // keep this comment\n  \"defaultProject\": \"Old\"\n}\n")

	SetRepoString(dir, "defaultProject", "New")

	text := readFileT(t, filepath.Join(dir, FileName))
	if !strings.Contains(text, "// keep this comment") {
		t.Fatalf("comment was dropped:\n%s", text)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProject != "New" {
		t.Fatalf("defaultProject = %q, want New", cfg.DefaultProject)
	}
}
