package redact

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRedact_SecretKeysAndContainers(t *testing.T) {
	in := []byte(`{
		"effortLevel": "high",
		"apiKey": "sk-ant-abc12345678",
		"mcpServers": {
			"x": {
				"command": "node",
				"env": {"ANTHROPIC_API_KEY": "secret-value-here", "PORT": "3000"},
				"headers": {"Authorization": "Bearer tok-9999999999"}
			}
		}
	}`)
	out, paths, err := RedactBytes(in, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["apiKey"] != Placeholder {
		t.Errorf("apiKey not redacted: %v", got["apiKey"])
	}
	mcp := got["mcpServers"].(map[string]any)["x"].(map[string]any)
	env := mcp["env"].(map[string]any)
	if env["ANTHROPIC_API_KEY"] != Placeholder || env["PORT"] != Placeholder {
		t.Errorf("env not fully redacted: %v", env)
	}
	if mcp["command"] != "node" {
		t.Errorf("non-secret command clobbered: %v", mcp["command"])
	}
	hdr := mcp["headers"].(map[string]any)
	if hdr["Authorization"] != Placeholder {
		t.Errorf("header not redacted: %v", hdr)
	}
	// paths reported and sorted
	want := []string{
		"apiKey",
		"mcpServers.x.env.ANTHROPIC_API_KEY",
		"mcpServers.x.env.PORT",
		"mcpServers.x.headers.Authorization",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestRedact_NoSecretsIsNoOp(t *testing.T) {
	// Mirrors John's real settings.json: permissions/preferences only.
	in := []byte(`{"permissions":{"allow":["Bash(ls)"]},"effortLevel":"high"}`)
	out, paths, err := RedactBytes(in, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("expected no redactions, got %v", paths)
	}
	var a, b any
	json.Unmarshal(in, &a)
	json.Unmarshal(out, &b)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("clean config changed: %s", out)
	}
}

func TestRedactThenScan_IsClean(t *testing.T) {
	// The real pipeline: redact, then the tripwire must find nothing.
	in := []byte(`{"apiKey":"sk-ant-abcdefgh12345678","env":{"K":"ghp_aaaaaaaaaaaaaaaaaaaa"}}`)
	out, _, err := RedactBytes(in, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	findings, err := ScanBytes(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("tripwire flagged after redaction: %v", findings)
	}
}
