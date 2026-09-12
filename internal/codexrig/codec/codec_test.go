package codec

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/redact"
)

func TestForPicksTheFormatByExtension(t *testing.T) {
	cases := map[string]string{
		"config.toml":      "toml",
		"work.config.toml": "toml",
		"CONFIG.TOML":      "toml",
		"settings.json":    "json",
	}
	for rel, want := range cases {
		c, ok := For(rel)
		if !ok {
			t.Fatalf("For(%q) found no codec", rel)
		}
		if c.Name() != want {
			t.Errorf("For(%q) = %s, want %s", rel, c.Name(), want)
		}
	}
	// A rollout or a skill is carried verbatim, and must not be parsed.
	for _, rel := range []string{"sessions/2026/09/05/rollout-x.jsonl", "skills/a/SKILL.md", "rules/default.rules"} {
		if _, ok := For(rel); ok {
			t.Errorf("For(%q) claimed a codec; that file should be carried verbatim", rel)
		}
	}
}

func TestTOMLRoundTripKeepsTheShapeAndIsDeterministic(t *testing.T) {
	src := `
model = "gpt-6-astra"
model_reasoning_effort = "high"

[features]
js_repl = false

[mcp_servers.railway]
command = "railway"
args = ["mcp", "proxy"]
startup_timeout_sec = 120

[mcp_servers.railway.env]
RAILWAY_TOKEN = "fake-token-0001"

[projects."/Users/someone/Git/thing"]
trust_level = "trusted"
`
	c := TOML{}
	v, err := c.Decode([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	// Deterministic: the engine byte-compares the encoded result against what is
	// staged to decide whether anything changed. An encoder that reordered keys
	// would report every file as modified on every sync.
	again, err := c.Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(again) {
		t.Fatal("TOML encoding is not deterministic; every sync would restage every config")
	}

	back, err := c.Decode(first)
	if err != nil {
		t.Fatalf("re-decoding our own output failed: %v", err)
	}
	m := back.(map[string]any)
	if m["model"] != "gpt-6-astra" {
		t.Errorf("model was lost: %v", m["model"])
	}
	srv := m["mcp_servers"].(map[string]any)["railway"].(map[string]any)
	if srv["command"] != "railway" {
		t.Errorf("mcp server command was lost: %v", srv)
	}
	if _, ok := m["projects"].(map[string]any)[`/Users/someone/Git/thing`]; !ok {
		t.Errorf("a quoted table key with slashes did not survive: %v", m["projects"])
	}
}

func TestTOMLRedactsAnMCPServerEnvTable(t *testing.T) {
	// This is the case the codec exists for. Reaching config.toml through the
	// raw-file path would publish this token; reaching it through a codec sends
	// it through the redactor's env-container rule.
	c := TOML{}
	v, err := c.Decode([]byte("[mcp_servers.x.env]\nAPI_TOKEN = \"fake-secret-0001\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, paths := redact.Redact(v, redact.DefaultPolicy())
	b, err := c.Encode(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "fake-secret-0001") {
		t.Fatalf("the token survived redaction:\n%s", b)
	}
	if !strings.Contains(string(b), redact.Placeholder) {
		t.Fatalf("the field was dropped rather than marked; restore could not then keep the local value:\n%s", b)
	}
	if len(paths) != 1 || paths[0] != "mcp_servers.x.env.API_TOKEN" {
		t.Errorf("redacted paths = %v, want the dotted path of the field", paths)
	}
}

func TestTOMLSecretPreservingMergeKeepsTheLocalValue(t *testing.T) {
	// The restore half: a synced placeholder must leave the machine's own secret
	// alone rather than overwrite it with the sentinel.
	c := TOML{}
	synced, err := c.Decode([]byte("model = \"gpt-6-astra\"\n\n[mcp_servers.x.env]\nAPI_TOKEN = \"" + redact.Placeholder + "\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := c.Decode([]byte("model = \"older\"\n\n[mcp_servers.x.env]\nAPI_TOKEN = \"this-machines-real-value\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	merged := redact.Merge(synced, local)
	b, err := c.Encode(merged)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "this-machines-real-value") {
		t.Errorf("the local secret was clobbered:\n%s", b)
	}
	if strings.Contains(string(b), redact.Placeholder) {
		t.Errorf("the sentinel was written into a live config:\n%s", b)
	}
	if !strings.Contains(string(b), "gpt-6-astra") {
		t.Errorf("the synced value did not win for a non-secret field:\n%s", b)
	}
}

func TestTOMLMergeOnAFreshMachineDropsTheFieldRatherThanWritingTheSentinel(t *testing.T) {
	c := TOML{}
	synced, err := c.Decode([]byte("[mcp_servers.x.env]\nAPI_TOKEN = \"" + redact.Placeholder + "\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	merged := redact.Merge(synced, map[string]any{})
	b, err := c.Encode(merged)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), redact.Placeholder) {
		t.Errorf("a fresh machine got the literal sentinel, which Codex would send as a token:\n%s", b)
	}
}

func TestEmptyTOMLRoundTripsAsEmpty(t *testing.T) {
	c := TOML{}
	v, err := c.Decode(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("an empty document encoded as %q", b)
	}
}
