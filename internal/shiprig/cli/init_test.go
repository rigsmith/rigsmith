package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
)

// The scaffolded release.jsonc must list the engine's actual DefaultOrder, so the
// starter can never drift from the real default.
func TestReleaseConfigStarterMatchesDefaultOrder(t *testing.T) {
	starter := releaseConfigStarter()
	for _, step := range pipeline.DefaultOrder {
		if !strings.Contains(starter, `"`+step+`"`) {
			t.Errorf("starter release config missing step %q", step)
		}
	}
	if !strings.Contains(starter, "--dry-build") {
		t.Error("starter should point at --dry-build")
	}

	// The starter must parse through the real engine loader (catches a stray
	// comma or bad JSONC) and resolve to exactly the default order.
	path := filepath.Join(t.TempDir(), "release.jsonc")
	if err := os.WriteFile(path, []byte(starter), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := pipeline.LoadConfig(path)
	if err != nil {
		t.Fatalf("starter release config does not parse: %v", err)
	}
	if strings.Join(cfg.Order, ",") != strings.Join(pipeline.DefaultOrder, ",") {
		t.Errorf("starter order %v != DefaultOrder %v", cfg.Order, pipeline.DefaultOrder)
	}
}

func TestScaffoldReleaseConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release.jsonc")

	created, err := scaffoldReleaseConfig(path)
	if err != nil || !created {
		t.Fatalf("first scaffold: created=%v err=%v, want created", created, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file written: %v", err)
	}

	// A second call leaves the existing file untouched.
	if err := os.WriteFile(path, []byte("EDITED"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err = scaffoldReleaseConfig(path)
	if err != nil || created {
		t.Fatalf("second scaffold: created=%v err=%v, want not-created", created, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "EDITED" {
		t.Error("scaffold overwrote an existing release config")
	}
}

func TestPrintTokenPreflight(t *testing.T) {
	// The env map stands in for the layered .env/.env.local < ambient view: a
	// token present there (e.g. from a local .env) reads as set, an empty or
	// absent one as missing.
	env := map[string]string{"RELEASE_TOKEN_SET": "x", "RELEASE_TOKEN_MISSING": ""}

	var buf bytes.Buffer
	printTokenPreflight(&buf, []plugin.TokenSpec{
		{EnvVar: "RELEASE_TOKEN_SET", For: "publish"},
		{EnvVar: "RELEASE_TOKEN_MISSING", For: "upload", URL: "https://example.test"},
	}, env)
	out := buf.String()
	if !strings.Contains(out, "✓ RELEASE_TOKEN_SET") {
		t.Errorf("set token should be ticked:\n%s", out)
	}
	if !strings.Contains(out, "⚠ RELEASE_TOKEN_MISSING not set") || !strings.Contains(out, "https://example.test") {
		t.Errorf("missing token should warn with its URL:\n%s", out)
	}
}

func TestHandleBuildConfigPresentLeavesFileAlone(t *testing.T) {
	var buf bytes.Buffer
	root := t.TempDir()
	err := handleBuildConfig(&buf, root, "Go", &plugin.BuildConfigSpec{
		Path: ".goreleaser.yaml", Present: true, Tool: "goreleaser",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".goreleaser.yaml")); !os.IsNotExist(err) {
		t.Error("a Present build config should not be written")
	}
	if !strings.Contains(buf.String(), "already present") {
		t.Errorf("expected an 'already present' note:\n%s", buf.String())
	}
}

// Off a TTY (the test environment), handleBuildConfig writes the starter without
// prompting and warns when the build tool is missing.
func TestHandleBuildConfigWritesAndPreflightsTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // goreleaser absent ⇒ deterministic warn
	var buf bytes.Buffer
	root := t.TempDir()
	err := handleBuildConfig(&buf, root, "Go", &plugin.BuildConfigSpec{
		Path: ".goreleaser.yaml", Content: "version: 2\n", Tool: "goreleaser",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil || string(got) != "version: 2\n" {
		t.Fatalf("expected the starter written, got %q err=%v", got, err)
	}
	if !strings.Contains(buf.String(), "goreleaser not on PATH") {
		t.Errorf("expected a missing-tool warning:\n%s", buf.String())
	}
}
