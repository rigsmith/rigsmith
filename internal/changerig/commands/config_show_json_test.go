package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// showJSON runs `config show --json` in a workspace holding the given files and
// returns the parsed output.
func showJSON(t *testing.T, files map[string]string) map[string]any {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".changeset"), 0o755); err != nil { // anchors the workspace root
		t.Fatal(err)
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)

	cmd := newConfigShowCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("--json output isn't JSON: %v\n%s", err, out.String())
	}
	return parsed
}

func sourceOf(t *testing.T, cfg map[string]any) any {
	t.Helper()
	versioning, ok := cfg["versioning"].(map[string]any)
	if !ok {
		t.Fatalf("no versioning object in %v", cfg)
	}
	return versioning["source"]
}

// With no config at all, --json prints the defaults, source included.
func TestConfigShowJSONDefaults(t *testing.T) {
	cfg := showJSON(t, nil)
	if got := sourceOf(t, cfg); got != "changesets" {
		t.Errorf("versioning.source = %v, want the default, changesets", got)
	}
	if cfg["baseBranch"] != "main" {
		t.Errorf("baseBranch = %v, want the default, main", cfg["baseBranch"])
	}
}

// A JSONC config (comments, trailing comma) comes out as plain JSON, with its
// source and its ecosystem blocks.
func TestConfigShowJSONParsesJSONC(t *testing.T) {
	cfg := showJSON(t, map[string]string{
		".changeset/config.jsonc": `{
  // commits too
  "versioning": { "source": "both" },
  "node": { "publishDirs": ["dist"] },
}`,
	})
	if got := sourceOf(t, cfg); got != "both" {
		t.Errorf("versioning.source = %v, want both", got)
	}
	node, ok := cfg["node"].(map[string]any)
	if !ok || node["publishDirs"] == nil {
		t.Errorf("the node ecosystem block is missing: %v", cfg["node"])
	}
}

// A config set without a source still reports the effective one, and one
// nested in shiprig.jsonc resolves like any other location.
func TestConfigShowJSONFillsTheSourceWherever(t *testing.T) {
	cfg := showJSON(t, map[string]string{
		"shiprig.jsonc": `{ "changeset": { "baseBranch": "trunk" } }`,
	})
	if cfg["baseBranch"] != "trunk" {
		t.Errorf("baseBranch = %v, want trunk from shiprig.jsonc", cfg["baseBranch"])
	}
	if got := sourceOf(t, cfg); got != "changesets" {
		t.Errorf("versioning.source = %v, want changesets", got)
	}
}

// Numbers come out as written: a large integer in an ecosystem block keeps
// every digit, rather than passing through float64.
func TestConfigShowJSONKeepsNumbersExact(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".changeset"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".changeset", "config.json"),
		[]byte(`{ "node": { "big": 9007199254740993 } }`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	cmd := newConfigShowCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("9007199254740993")) {
		t.Errorf("output lost digits:\n%s", out.String())
	}
}
