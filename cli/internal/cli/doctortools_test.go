package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/cli/internal/detect"
)

// labelsOf runs each pendingCheck and returns label→level for assertions.
func labelsOf(checks []pendingCheck) map[string]docLevel {
	out := map[string]docLevel{}
	for _, pc := range checks {
		out[pc.label] = pc.run().level
	}
	return out
}

func TestToolChecks_RelevantPerEcosystem(t *testing.T) {
	root := t.TempDir()

	// Cargo only → the three cargo subcommands, no dnx/reportgenerator.
	cargo := toolChecks(map[string]bool{detect.Cargo: true}, root)
	got := map[string]bool{}
	for _, pc := range cargo {
		if pc.eco != "tools" {
			t.Errorf("tool row eco = %q, want tools", pc.eco)
		}
		got[pc.label] = true
	}
	for _, want := range []string{"cargo-llvm-cov", "cargo-outdated", "cargo-watch"} {
		if !got[want] {
			t.Errorf("cargo tools missing %q (got %v)", want, got)
		}
	}
	if got["dnx"] || got["reportgenerator"] {
		t.Errorf("cargo should not pull in dnx/reportgenerator (got %v)", got)
	}

	// .NET → dnx + reportgenerator.
	net := labelsOf(toolChecks(map[string]bool{detect.DotNet: true}, root))
	if _, ok := net["dnx"]; !ok {
		t.Error(".NET tools should include dnx")
	}
	if _, ok := net["reportgenerator"]; !ok {
		t.Error(".NET tools should include reportgenerator")
	}

	// Go → wgo (the watcher) + reportgenerator (coverage).
	goTools := labelsOf(toolChecks(map[string]bool{detect.Go: true}, root))
	if _, ok := goTools["wgo"]; !ok {
		t.Error("Go tools should include wgo")
	}

	// node + go + .NET present → reportgenerator listed exactly once (deduped).
	multi := toolChecks(map[string]bool{detect.Node: true, detect.Go: true, detect.DotNet: true}, root)
	rg := 0
	for _, pc := range multi {
		if pc.label == "reportgenerator" {
			rg++
		}
	}
	if rg != 1 {
		t.Errorf("reportgenerator listed %d times, want 1 (deduped)", rg)
	}
}

func TestToolHowto(t *testing.T) {
	// install command wins.
	if got := toolHowto(extTool{install: []string{"cargo", "install", "x"}, hint: "h", why: "w"}); got != "cargo install x" {
		t.Errorf("install case = %q", got)
	}
	// else the hint.
	if got := toolHowto(extTool{hint: "ships with the SDK", why: "w"}); got != "ships with the SDK" {
		t.Errorf("hint case = %q", got)
	}
	// else what it's for.
	if got := toolHowto(extTool{why: "does a thing"}); got != "does a thing" {
		t.Errorf("why case = %q", got)
	}
}

func TestConfigChecks_FlagsBrokenPaths(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".rig.json"), `{
	  "solution": "Missing.sln",
	  "defaultProject": "Ghost",
	  "commands": { "deploy": { "command": "echo hi", "cwd": "nope" } }
	}`)

	got := labelsOf(configChecks(root, map[string]bool{"Real": true}))
	for _, label := range []string{"solution", "default", "deploy"} {
		if got[label] != docError {
			t.Errorf("%s level = %v, want error", label, got[label])
		}
	}
}

func TestConfigChecks_PassesWhenPathsResolve(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "App.sln"), "")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".rig.json"), `{
	  "solution": "App.sln",
	  "defaultProject": "Api",
	  "commands": { "deploy": { "command": "echo hi", "cwd": "src" } }
	}`)

	got := labelsOf(configChecks(root, map[string]bool{"Api": true}))
	for _, label := range []string{"solution", "default", "deploy"} {
		if got[label] != docOK {
			t.Errorf("%s level = %v, want ok", label, got[label])
		}
	}
}

func TestConfigChecks_NoConfigNoRows(t *testing.T) {
	if rows := configChecks(t.TempDir(), nil); len(rows) != 0 {
		t.Errorf("a repo with no config should yield no Config rows, got %d", len(rows))
	}
}
