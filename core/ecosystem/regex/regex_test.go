package regex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// writeRepo lays out a temp repo with a .changeset/config.json holding a regex
// block and the named files, returning the repo root.
func writeRepo(t *testing.T, configBody string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	csDir := filepath.Join(root, ".changeset")
	if err := os.MkdirAll(csDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(csDir, "config.json"), []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const chartConfig = `{
  "regex": {
    "packages": [
      { "name": "chart", "file": "deploy/Chart.yaml", "pattern": "version: (?<version>.*)" }
    ]
  }
}`

func TestDetectAndDiscover(t *testing.T) {
	root := writeRepo(t, chartConfig, map[string]string{
		"deploy/Chart.yaml": "apiVersion: v2\nname: app\nversion: 1.2.0\nappVersion: \"x\"\n",
	})
	a := New()

	if ok, _ := a.Detect(context.Background(), root); !ok {
		t.Fatal("Detect should be true when a regex block is declared")
	}
	resp, err := a.Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 1 {
		t.Fatalf("got %d packages, want 1: %+v", len(resp.Packages), resp.Packages)
	}
	p := resp.Packages[0]
	if p.Name != "chart" || p.Version != "1.2.0" || p.ManifestPath != "deploy/Chart.yaml" || p.Dir != "deploy" {
		t.Errorf("package = %+v", p)
	}
}

func TestDetectFalseWithoutBlock(t *testing.T) {
	root := writeRepo(t, `{ "updateInternalDependencies": "patch" }`, nil)
	if ok, _ := New().Detect(context.Background(), root); ok {
		t.Error("Detect should be false without a regex block")
	}
}

func TestSetVersionRewritesOnlyTheGroup(t *testing.T) {
	root := writeRepo(t, chartConfig, map[string]string{
		"deploy/Chart.yaml": "apiVersion: v2\nname: app\nversion: 1.2.0\nappVersion: \"keep-me\"\n",
	})
	a := New()
	err := a.SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot:   root,
		Package:    plugin.Package{Name: "chart", ManifestPath: "deploy/Chart.yaml"},
		NewVersion: "1.3.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "deploy", "Chart.yaml"))
	want := "apiVersion: v2\nname: app\nversion: 1.3.0\nappVersion: \"keep-me\"\n"
	if string(got) != want {
		t.Errorf("rewrite changed more than the version:\n got: %q\nwant: %q", got, want)
	}
}

func TestSetVersionUnknownPackage(t *testing.T) {
	root := writeRepo(t, chartConfig, map[string]string{"deploy/Chart.yaml": "version: 1.0.0\n"})
	err := New().SetVersion(context.Background(), plugin.SetVersionRequest{
		RepoRoot: root, Package: plugin.Package{Name: "nope"}, NewVersion: "2.0.0",
	})
	if err == nil {
		t.Error("expected an error for a package not in the regex config")
	}
}

func TestGoStyleNamedGroupAlsoWorks(t *testing.T) {
	cfg := `{ "regex": { "packages": [ { "name": "ver", "file": "VERSION", "pattern": "^(?P<version>\\d+\\.\\d+\\.\\d+)$" } ] } }`
	root := writeRepo(t, cfg, map[string]string{"VERSION": "0.4.1"})
	resp, err := New().Discover(context.Background(), plugin.DiscoverRequest{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Packages) != 1 || resp.Packages[0].Version != "0.4.1" {
		t.Fatalf("Go-style (?P<version>) pattern: %+v", resp.Packages)
	}
}

// TestArtifactsSkipped: a regex-managed package carries its version in arbitrary
// files and ships by tag, so there is nothing to build.
func TestArtifactsSkipped(t *testing.T) {
	resp, err := New().Artifacts(context.Background(), plugin.ArtifactsRequest{
		RepoRoot:  t.TempDir(),
		OutputDir: t.TempDir(),
		Package:   plugin.Package{Name: "app", Version: "1.0.0", Dir: "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Built || !resp.Skipped {
		t.Errorf("regex artifacts = %+v, want skipped", resp)
	}
}

// TestInfoOmitsArtifacts: regex has nothing to build, so it must not advertise
// the artifacts capability.
func TestInfoOmitsArtifacts(t *testing.T) {
	for _, c := range New().Info().Capabilities {
		if c == plugin.MethodArtifacts {
			t.Error("regex Info() should not advertise MethodArtifacts")
		}
	}
}

// TestReleaseInitEmpty: a regex package needs no token and no build config — it
// releases by git tag — and so does not advertise the capability.
func TestReleaseInitEmpty(t *testing.T) {
	for _, c := range New().Info().Capabilities {
		if c == plugin.MethodReleaseInit {
			t.Error("regex Info() should not advertise MethodReleaseInit")
		}
	}
	resp, err := New().ReleaseInit(context.Background(), plugin.ReleaseInitRequest{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if resp.BuildConfig != nil || len(resp.Tokens) != 0 || len(resp.Notes) != 0 {
		t.Errorf("regex ReleaseInit should be empty, got %+v", resp)
	}
}
