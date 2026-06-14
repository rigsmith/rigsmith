// Tests for the global+repo config wiring: a key set only in the user-wide
// ~/.rig.json must reach the spawned-process env builders (commandEnv /
// customEnv) and the custom-command surface, with the repo's .rig.json winning
// per key. The global path is injected via RIG_GLOBAL_CONFIG (the same seam
// the .NET rig's tests use), so the developer's real ~/.rig.json is never read.
package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
)

// withGlobalConfig points RIG_GLOBAL_CONFIG at a temp file with the given
// JSON, returning its path.
func withGlobalConfig(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".rig.json")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIG_GLOBAL_CONFIG", path)
	return path
}

func TestCommandEnv_GlobalKeyReachesTheEnvAndTheRepoOverridesIt(t *testing.T) {
	withGlobalConfig(t, `{ "env": { "FROM_GLOBAL": "1", "SHARED": "global" } }`)
	root := t.TempDir()
	writeTreeFile(t, root, config.FileName, `{ "env": { "SHARED": "repo" } }`)

	env := commandEnv(root)

	if env == nil {
		t.Fatal("commandEnv = nil (inherit), want the merged config env applied")
	}
	if !slices.Contains(env, "FROM_GLOBAL=1") {
		t.Error("the global-only env key should reach the spawned-process env")
	}
	if !slices.Contains(env, "SHARED=repo") || slices.Contains(env, "SHARED=global") {
		t.Errorf("the repo's value should win per key, env = %v", env)
	}
}

func TestCustomEnv_SeesTheMergedGlobalAndRepoEnv(t *testing.T) {
	withGlobalConfig(t, `{ "env": { "FROM_GLOBAL": "1", "SHARED": "global" } }`)
	root := t.TempDir()
	writeTreeFile(t, root, config.FileName, `{ "env": { "SHARED": "repo" } }`)

	cfg, err := config.LoadMerged(root) // what root.go hands to customCmds
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	env := customEnv(cfg, map[string]string{"PER_CMD": "x"})

	if !slices.Contains(env, "FROM_GLOBAL=1") {
		t.Error("the global-only env key should reach custom commands")
	}
	if !slices.Contains(env, "SHARED=repo") {
		t.Errorf("the repo's value should win per key, env = %v", env)
	}
	if !slices.Contains(env, "PER_CMD=x") {
		t.Error("the command's own env should layer on top")
	}
}

func TestCustomCmds_SurfaceGlobalOnlyCommandsWithTheRepoWinningPerName(t *testing.T) {
	withGlobalConfig(t, `{ "commands": { "hello": "echo global", "shared": "echo global" } }`)
	root := t.TempDir()
	writeTreeFile(t, root, config.FileName, `{ "commands": { "shared": "echo repo" } }`)

	cfg, err := config.LoadMerged(root)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	cmds := customCmds(cfg)

	byName := map[string]string{}
	for _, c := range cmds {
		byName[c.Name()] = c.Short
	}
	if _, ok := byName["hello"]; !ok {
		t.Errorf("a global-only custom command should surface, got %v", byName)
	}
	if got := byName["shared"]; got != "Custom command: echo repo" {
		t.Errorf("shared = %q, want the repo's definition to win", got)
	}
}

func TestQuiet_FallsThroughFromTheGlobalConfig(t *testing.T) {
	withGlobalConfig(t, `{ "quiet": true }`)
	root := t.TempDir()
	writeTreeFile(t, root, config.FileName, `{ "defaultProject": "App" }`)

	cfg, err := config.LoadMerged(root) // what PersistentPreRunE folds into --quiet
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if !cfg.IsQuiet() {
		t.Error("the global quiet should apply when the repo doesn't set it")
	}
}
