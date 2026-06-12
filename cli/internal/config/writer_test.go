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

	returned := SetRepoString(dir, "defaultProject", "App")

	if want := filepath.Join(dir, FileName); returned != want {
		t.Fatalf("returned path = %q, want %q", returned, want)
	}
	if cfg := parseT(t, readFileT(t, returned)); cfg.DefaultProject != "App" {
		t.Fatalf("defaultProject = %q, want App", cfg.DefaultProject)
	}
}

// ---- ConventionTests (ConfigWriter cases) ----

func TestConvention_ConfigWriterCreatesFileWithProperty(t *testing.T) {
	dir := t.TempDir()

	path := SetRepoString(dir, "defaultProject", "App.Desktop")

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
