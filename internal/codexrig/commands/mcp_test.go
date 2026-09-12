package commands

import (
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
	u := mcp.Entry{Server: mcp.Server{Name: "h", URL: "https://user:tok@example.com/mcp"}}
	if got := forDisplay([]mcp.Entry{u})[0].Target; strings.Contains(got, "tok") || !strings.Contains(got, "example.com") {
		t.Errorf("url = %s", got)
	}
}
