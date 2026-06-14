// Port of the .NET rig's InfoInitTests, against riginit.go / info.go's actual
// behavior. One deliberate divergence: `rig init` on an existing .rig.json
// reports and returns success in Go (the .NET rig exits 1) — the file is left
// untouched either way, which is the contract that matters.
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/spf13/cobra"
)

// tempRepo creates an isolated repo root (a .git marker pins detect.Root so
// the walk never escapes the temp dir) and chdirs into it. The user-wide
// config path is pinned inside the temp dir too, so the merged loaders never
// read the developer's real ~/.rig.json.
func tempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RIG_GLOBAL_CONFIG", filepath.Join(dir, "global-rig.json"))
	t.Chdir(dir)
	return dir
}

// runSub executes a subcommand's RunE with captured output.
func runSub(t *testing.T, cmd *cobra.Command) (string, error) {
	t.Helper()
	cmd.SetContext(context.Background())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.RunE(cmd, nil)
	return buf.String(), err
}

func TestInit_CreatesATemplateWhenAbsent(t *testing.T) {
	dir := tempRepo(t)

	if _, err := runSub(t, newRigInitCmd()); err != nil {
		t.Fatalf("init: %v", err)
	}

	path := filepath.Join(dir, config.FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf(".rig.json should exist: %v", err)
	}
	if !strings.Contains(string(data), "$schema") {
		t.Fatal("the scaffold should carry a $schema")
	}
	// the scaffold is valid JSON and parses to an (otherwise-empty) config
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultProject != "" {
		t.Fatalf("DefaultProject = %q, want empty", cfg.DefaultProject)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("the scaffold should parse warning-free, got %v", cfg.Warnings)
	}
}

func TestInit_RefusesToOverwriteAnExistingFile(t *testing.T) {
	dir := tempRepo(t)
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(`{ "defaultProject": "Keep" }`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runSub(t, newRigInitCmd())
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(out, "already exists") {
		t.Fatalf("output = %q, want an already-exists notice", out)
	}

	// untouched
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DefaultProject != "Keep" {
		t.Fatalf("DefaultProject = %q, want Keep (file must be left untouched)", cfg.DefaultProject)
	}
}

func TestInfo_RunsAndSucceedsOnAnEmptyRepo(t *testing.T) {
	tempRepo(t)
	if _, err := runSub(t, newInfoCmd()); err != nil {
		t.Fatalf("info on an empty repo should succeed, got %v", err)
	}
}

func TestCoverageDefaultsSummary_ListsOnlyActivePrefs(t *testing.T) {
	bptr := func(b bool) *bool { return &b }

	if got := coverageDefaults(nil); got != "(none)" {
		t.Fatalf("nil = %q, want (none)", got)
	}
	// license has its own surfacing; it is not a behavior default.
	if got := coverageDefaults(&config.Coverage{License: "KEY"}); got != "(none)" {
		t.Fatalf("license-only = %q, want (none)", got)
	}
	if got := coverageDefaults(&config.Coverage{Open: bptr(false), Full: bptr(false)}); got != "(none)" {
		t.Fatalf("false bools = %q, want (none)", got)
	}
	if got := coverageDefaults(&config.Coverage{Min: fptr(80), Open: bptr(true)}); got != "min 80%, auto-open" {
		t.Fatalf("got %q, want %q", got, "min 80%, auto-open")
	}
}
