package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
)

func mkProject(t *testing.T, projectsDir, slug, cwd string) {
	t.Helper()
	dir := filepath.Join(projectsDir, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + cwd + `","isSidechain":false}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildAndRoundTrip(t *testing.T) {
	projects := t.TempDir()
	mkProject(t, projects, "-Users-john-Git-rigsmith", "/Users/john/Git/rigsmith")
	mkProject(t, projects, "-opt-shared-proj", "/opt/shared/proj") // not under home
	// an empty project dir (no transcript) must be skipped
	if err := os.MkdirAll(filepath.Join(projects, "-empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	src := map[string]string{"HOME": "/Users/john"}
	m, err := Build(projects, "2.1.175", pathmap.OSMacOS, src)
	if err != nil {
		t.Fatal(err)
	}

	if m.Schema != schemaVersion || m.SourceOS != pathmap.OSMacOS || m.ClaudeVersion != "2.1.175" {
		t.Fatalf("header wrong: %+v", m)
	}
	if len(m.Projects) != 2 {
		t.Fatalf("want 2 projects (empty skipped), got %d: %v", len(m.Projects), m.Slugs())
	}
	if got := m.Projects["-Users-john-Git-rigsmith"]; got.Template != "$HOME/Git/rigsmith" || got.Cwd != "/Users/john/Git/rigsmith" {
		t.Fatalf("rigsmith entry: %+v", got)
	}
	// not under home → no template, original cwd retained
	if got := m.Projects["-opt-shared-proj"]; got.Template != "" || got.Cwd != "/opt/shared/proj" {
		t.Fatalf("opt entry: %+v", got)
	}

	// save → load round-trips
	repo := t.TempDir()
	if err := m.Save(repo); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Projects) != 2 || loaded.Projects["-Users-john-Git-rigsmith"].Template != "$HOME/Git/rigsmith" {
		t.Fatalf("round-trip lost data: %+v", loaded)
	}
}

func TestLoad_Missing(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("expected error loading absent manifest")
	}
}
