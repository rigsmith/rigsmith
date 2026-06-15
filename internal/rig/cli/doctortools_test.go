package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
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

func TestDoctorToolFixes_OwnedToolsOnly(t *testing.T) {
	root := t.TempDir()
	cmd := &cobra.Command{}

	// .NET only → its relevant tools are dnx (ships with the SDK) and
	// ReportGenerator (fetched on use). Neither has an install command rig owns,
	// so doctor offers no installs regardless of what's on PATH.
	if got := doctorToolFixes(cmd, map[string]bool{detect.DotNet: true}, root); got != nil {
		t.Errorf(".NET should yield no install offers (owned tools only), got %+v", got)
	}

	// Cargo's tools DO have install commands. Pin all three off → report-only, no
	// offers, independent of PATH.
	writeFile(t, filepath.Join(root, ".rig.json"), `{
	  "tools": {
	    "cargo-llvm-cov": "off",
	    "cargo-outdated": "off",
	    "cargo-watch": "off"
	  }
	}`)
	if got := doctorToolFixes(cmd, map[string]bool{detect.Cargo: true}, root); got != nil {
		t.Errorf("tools pinned off should yield no install offers, got %+v", got)
	}
}

func TestDoctorToolFixes_MissingCargoToolsOffered(t *testing.T) {
	root := t.TempDir() // no .rig.json ⇒ auto mode
	cmd := &cobra.Command{}

	sections := doctorToolFixes(cmd, map[string]bool{detect.Cargo: true}, root)
	// Every offered fix must be a missing, install-capable cargo tool — never dnx
	// or reportgenerator — and carry a runnable Fix + an "install …" label.
	for _, sec := range sections {
		if sec.Title != "tools" {
			t.Errorf("section title = %q, want tools", sec.Title)
		}
		for _, r := range sec.Results {
			if r.Status != doctor.Warn || r.Fix == nil {
				t.Errorf("%s: got %+v, want Warn with a Fix", r.Name, r)
			}
			if !strings.HasPrefix(r.FixLabel, "install ") {
				t.Errorf("%s: FixLabel = %q, want it to start with \"install \"", r.Name, r.FixLabel)
			}
			if r.Name == "dnx" || r.Name == "reportgenerator" {
				t.Errorf("%s has no install command and must not be offered", r.Name)
			}
			// What's offered must actually be absent from PATH.
			if _, err := exec.LookPath(r.Name); err == nil {
				t.Errorf("%s is on PATH and should not be offered for install", r.Name)
			}
		}
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
