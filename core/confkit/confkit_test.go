package confkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruthy(t *testing.T) {
	const key = "CONFKIT_TEST_TRUTHY"
	cases := map[string]bool{
		"1": true, "true": true, "TRUE": true, "Yes": true, "on": true,
		"0": false, "false": false, "no": false, "off": false, "": false, "maybe": false,
		" true ": true,
	}
	for v, want := range cases {
		t.Setenv(key, v)
		if got := Truthy(key); got != want {
			t.Errorf("Truthy(%q) = %v, want %v", v, got, want)
		}
	}
	os.Unsetenv(key)
	if Truthy(key) {
		t.Errorf("Truthy(unset) = true, want false")
	}
}

func TestWriterFreshDocumentStampsSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	w := Writer{SchemaURL: "https://example.test/schema.json"}

	if !w.SetString(path, []string{"name"}, "rig") {
		t.Fatal("SetString returned false")
	}
	got := readFile(t, path)
	if !strings.Contains(got, `"$schema": "https://example.test/schema.json"`) {
		t.Errorf("fresh document missing $schema header:\n%s", got)
	}
	if !strings.Contains(got, `"name": "rig"`) {
		t.Errorf("fresh document missing key:\n%s", got)
	}
}

func TestWriterPreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const initial = `{
  // keep me
  "baseBranch": "main",
  "access": "restricted"
}
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	w := Writer{SchemaURL: "https://example.test/schema.json"}
	if !w.SetString(path, []string{"access"}, "public") {
		t.Fatal("SetString returned false")
	}
	got := readFile(t, path)
	if !strings.Contains(got, "// keep me") {
		t.Errorf("comment was dropped:\n%s", got)
	}
	if !strings.Contains(got, `"access": "public"`) {
		t.Errorf("value not updated:\n%s", got)
	}
	// An edit-in-place must NOT inject a second $schema.
	if strings.Count(got, "$schema") != 0 {
		t.Errorf("edit-in-place should not add $schema:\n%s", got)
	}
}

func TestWriterTypedSetters(t *testing.T) {
	dir := t.TempDir()
	w := Writer{}

	boolPath := filepath.Join(dir, "b.json")
	if !w.SetBool(boolPath, []string{"quiet"}, true) {
		t.Fatal("SetBool returned false")
	}
	if got := readFile(t, boolPath); !strings.Contains(got, `"quiet": true`) {
		t.Errorf("SetBool: %s", got)
	}

	numPath := filepath.Join(dir, "n.json")
	if !w.SetNumber(numPath, []string{"min"}, 80) {
		t.Fatal("SetNumber returned false")
	}
	if got := readFile(t, numPath); !strings.Contains(got, `"min": 80`) {
		t.Errorf("SetNumber: %s", got)
	}
}

func TestWriterNoSchemaWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	w := Writer{} // no SchemaURL
	if !w.SetString(path, []string{"name"}, "x") {
		t.Fatal("SetString returned false")
	}
	if got := readFile(t, path); strings.Contains(got, "$schema") {
		t.Errorf("expected no $schema header when SchemaURL empty:\n%s", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
