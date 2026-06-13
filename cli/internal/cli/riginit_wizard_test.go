package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestWizardRigJSON(t *testing.T) {
	cases := []struct {
		eco, dp string
		quiet   bool
	}{
		{"go", "", false},
		{"dotnet", "App", true},
		{"", "", false},
	}
	for _, c := range cases {
		out := wizardRigJSON(c.eco, c.dp, c.quiet)
		if !json.Valid([]byte(out)) {
			t.Fatalf("not valid JSON for %+v:\n%s", c, out)
		}
		var doc struct {
			Ecosystem      string `json:"ecosystem"`
			DefaultProject string `json:"defaultProject"`
			Quiet          bool   `json:"quiet"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Ecosystem != c.eco || doc.DefaultProject != c.dp || doc.Quiet != c.quiet {
			t.Fatalf("round-trip = %+v, want %+v", doc, c)
		}
	}
}

func TestDetectedEcosystems(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodeDir := filepath.Join(root, "web")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "package.json"), []byte(`{"name":"web"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := detectedEcosystems(context.Background(), root)
	if !slices.Contains(got, "go") || !slices.Contains(got, "node") {
		t.Fatalf("got %v, want both go and node", got)
	}
	if !slices.IsSorted(got) {
		t.Fatalf("result should be sorted: %v", got)
	}
}

func TestRunnableDotnetNames_EmptyRepo(t *testing.T) {
	if names := runnableDotnetNames(t.TempDir()); len(names) != 0 {
		t.Fatalf("a non-.NET repo should yield no runnable names, got %v", names)
	}
}
