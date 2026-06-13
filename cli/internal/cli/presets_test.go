package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// isolateGlobalConfig points RIG_GLOBAL_CONFIG at a nonexistent file so
// LoadMerged sees only the repo .rig.json under test.
func isolateGlobalConfig(t *testing.T) {
	t.Helper()
	t.Setenv("RIG_GLOBAL_CONFIG", filepath.Join(t.TempDir(), "none.json"))
}

func writeRigJSON(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".rig.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestActivePresetEnv(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	writeRigJSON(t, root, `{ "envPresets": { "log": { "APP_LOG": "1" }, "prod": { "NODE_ENV": "production" } } }`)

	// A preset whose flag is set contributes its bundle.
	presets := []presetFlag{{name: "log", on: boolPtr(true)}, {name: "prod", on: boolPtr(false)}}
	env := activePresetEnv(root, presets)
	if env["APP_LOG"] != "1" {
		t.Errorf("APP_LOG = %q, want 1 (env=%v)", env["APP_LOG"], env)
	}
	if _, ok := env["NODE_ENV"]; ok {
		t.Errorf("prod preset was off; NODE_ENV should be absent (env=%v)", env)
	}

	// Nothing active → nil.
	if got := activePresetEnv(root, []presetFlag{{name: "log", on: boolPtr(false)}}); got != nil {
		t.Errorf("no active preset should yield nil, got %v", got)
	}
}

func TestResolveRootOverride(t *testing.T) {
	dir := t.TempDir()
	defer func() { rootFlag = "" }()

	rootFlag = dir
	if got := resolveRoot("/somewhere/else"); got != dir {
		t.Errorf("resolveRoot with --root = %q, want %q", got, dir)
	}
	rootFlag = ""
	// Without the override it falls back to walk-up discovery (just ensure it
	// doesn't return the override and produces a non-empty path).
	if got := resolveRoot(dir); got == "" {
		t.Error("resolveRoot without --root returned empty")
	}
}

func TestCommandEnvNoEnv(t *testing.T) {
	isolateGlobalConfig(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("FROM_DOTENV=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() { noEnv = false; presetEnv = nil }()

	// Default: the .env layer is loaded.
	noEnv = false
	if !envContains(commandEnv(root), "FROM_DOTENV=yes") {
		t.Error("expected .env to be loaded by default")
	}
	// --no-env drops the file layer.
	noEnv = true
	if envContains(commandEnv(root), "FROM_DOTENV=yes") {
		t.Error("--no-env should skip .env loading")
	}
}

func envContains(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
