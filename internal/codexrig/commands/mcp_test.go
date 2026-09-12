package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/codexrig/mcp"
)

// The listing is a portability report. It names which env keys will be
// stripped and never needs their values; argv and URLs can carry credentials
// and are the kind of thing that ends up pasted into an issue from here.
func TestMCPDisplayKeepsKeysAndDropsValues(t *testing.T) {
	e := mcp.Entry{Server: mcp.Server{
		Name: "x", Command: "npx", Args: []string{"-y", "@acme/mcp", "--token=ghp_" + strings.Repeat("a", 40)},
		Env: map[string]string{"API_KEY": "sk-secret", "REGION": "eu"},
	}}
	d := forDisplay([]mcp.Entry{e})[0]
	if strings.Join(d.EnvKeys, ",") != "API_KEY,REGION" {
		t.Errorf("envKeys = %v", d.EnvKeys)
	}
	if strings.Contains(d.Target, "ghp_") || !strings.Contains(d.Target, "<redacted>") {
		t.Errorf("target still carries the token: %s", d.Target)
	}
	for _, raw := range []string{
		"https://user:tok@example.com/mcp",
		"https://example.com/mcp?api_key=tok",
		"https://example.com/mcp#access_token=tok",
		"https://example.com/mcp/ghp_" + strings.Repeat("a", 40) + "/v1",
	} {
		u := mcp.Entry{Server: mcp.Server{Name: "h", URL: raw}}
		if got := forDisplay([]mcp.Entry{u})[0].Target; strings.Contains(got, "tok") || strings.Contains(got, "ghp_") || !strings.Contains(got, "example.com/mcp") {
			t.Errorf("url %s shown as %s", raw, got)
		}
	}
}

func TestMCPGetJSONUsesTheListingsObject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[mcp_servers.tidy]\ncommand = \"npx\"\nargs = [\"-y\", \"@acme/tidy-mcp\"]\n[mcp_servers.tidy.env]\nAPI_KEY = \"sk-secret\"\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewMCPCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"get", "tidy", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var d displayEntry
	if err := json.Unmarshal(out.Bytes(), &d); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out.String())
	}
	if d.Name != "tidy" || strings.Join(d.EnvKeys, ",") != "API_KEY" || strings.Contains(out.String(), "sk-secret") {
		t.Errorf("object = %+v (raw %s)", d, out.String())
	}
}

func TestMCPGetJSONRefusesAMissingNameWithOneObject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := NewMCPCmd()
	var out, errb bytes.Buffer // stdout alone carries the object; cobra's "Error:" line goes to stderr
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{"get", "nope", "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("a missing server exited zero")
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out.String())
	}
	if msg, _ := doc["message"].(string); doc["found"] != false || doc["name"] != "nope" || msg == "" {
		t.Errorf("refusal object = %v", doc)
	}
}
