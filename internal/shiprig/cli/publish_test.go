package cli

import (
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
)

func TestPackageSourceFor(t *testing.T) {
	// A per-ecosystem packageSource block overrides the built-in default.
	cfg, err := config.Parse([]byte(`{ "dotnet": { "packageSource": "github" }, "node": { "packageSource": "https://npm.acme.dev" } }`))
	if err != nil {
		t.Fatal(err)
	}
	if got := packageSourceFor(cfg, "dotnet"); got != "github" {
		t.Errorf("dotnet packageSource = %q, want github", got)
	}
	if got := packageSourceFor(cfg, "node"); got != "https://npm.acme.dev" {
		t.Errorf("node packageSource = %q, want the configured URL", got)
	}
	// No block for go → built-in default ("" for go).
	if got := packageSourceFor(cfg, "go"); got != "" {
		t.Errorf("go packageSource = %q, want empty", got)
	}

	// With no config blocks, dotnet falls back to its hardcoded "nuget" default.
	def := config.Default()
	if got := packageSourceFor(def, "dotnet"); got != "nuget" {
		t.Errorf("default dotnet packageSource = %q, want nuget", got)
	}
}
