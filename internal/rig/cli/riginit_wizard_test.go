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
		eco, sln, dp string
		exclude      []string
		quiet        bool
	}{
		{"go", "", "", nil, false},
		{"dotnet", "App.sln", "App", nil, true},
		{"node", "", "", []string{"examples", "fixtures"}, false},
	}
	for _, c := range cases {
		out := wizardRigJSON(c.eco, c.sln, c.dp, c.exclude, c.quiet)
		if !json.Valid([]byte(out)) {
			t.Fatalf("not valid JSON for %+v:\n%s", c, out)
		}
		var doc struct {
			Ecosystem      string   `json:"ecosystem"`
			Solution       string   `json:"solution"`
			DefaultProject string   `json:"defaultProject"`
			Exclude        []string `json:"exclude"`
			Quiet          bool     `json:"quiet"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Ecosystem != c.eco || doc.Solution != c.sln || doc.DefaultProject != c.dp || doc.Quiet != c.quiet {
			t.Fatalf("round-trip = %+v, want %+v", doc, c)
		}
		if !slices.Equal(doc.Exclude, c.exclude) && !(len(doc.Exclude) == 0 && len(c.exclude) == 0) {
			t.Fatalf("exclude = %v, want %v", doc.Exclude, c.exclude)
		}
	}
}

func TestSolutionFiles(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"Beta.slnx", "Alpha.sln", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := solutionFiles(root)
	if !slices.Equal(got, []string{"Alpha.sln", "Beta.slnx"}) {
		t.Fatalf("got %v, want [Alpha.sln Beta.slnx]", got)
	}
}

func TestExcludeCandidates(t *testing.T) {
	root := t.TempDir()
	// examples/* and src/* both hold packages; only "examples" is an exclude candidate.
	for _, dir := range []string{"examples/demo", "examples/sample", "src/app", "vendoredstuff"} {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "package.json"), []byte(`{"name":"`+filepath.Base(dir)+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := excludeCandidates(context.Background(), root)
	if !slices.Equal(got, []string{"examples"}) {
		t.Fatalf("got %v, want [examples]", got)
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
