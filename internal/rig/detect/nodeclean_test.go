package detect

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestNodeCleanMapsToScript(t *testing.T) {
	// clean is symmetric with build/test/format — it runs the project's
	// package.json `clean` script under the detected package manager.
	for _, pm := range []NodePM{NPM, PNPM, Yarn, Bun} {
		argv, ok := nodeCommand(pm, plugin.VerbClean)
		if !ok {
			t.Fatalf("%s: clean should map to a script", pm)
		}
		want := []string{string(pm), "run", "clean"}
		if len(argv) != 3 || argv[0] != want[0] || argv[1] != "run" || argv[2] != "clean" {
			t.Errorf("%s: clean argv = %v, want %v", pm, argv, want)
		}
	}
}

func TestNodeHasScript(t *testing.T) {
	dir := t.TempDir()
	if NodeHasScript(dir, "clean") {
		t.Fatal("no package.json should report no clean script")
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"build":"tsc","clean":"rimraf dist"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NodeHasScript(dir, "clean") {
		t.Error("clean script should be detected")
	}
	if NodeHasScript(dir, "nope") {
		t.Error("absent script should not be detected")
	}
}

func TestVerbSkipReason(t *testing.T) {
	// A Node package provides a verb only if its package.json declares the
	// script — that is the difference between "nothing to typecheck here" and
	// "the typecheck failed".
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"build":"tsc"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := VerbSkipReason(Node, plugin.VerbBuild, dir); got != "" {
		t.Errorf("declared script should not skip, got %q", got)
	}
	if got := VerbSkipReason(Node, plugin.VerbTypecheck, dir); got != `no "typecheck" script` {
		t.Errorf("missing script reason = %q", got)
	}
	// Verbs the package manager answers itself (install, add, …) are not
	// project scripts and are never skipped.
	if got := VerbSkipReason(Node, plugin.VerbInstall, dir); got != "" {
		t.Errorf("install is not a script verb, got %q", got)
	}
	// Every project of a toolchain-verb ecosystem answers to its verbs.
	for _, eco := range []string{DotNet, Go, Cargo} {
		if got := VerbSkipReason(eco, plugin.VerbTypecheck, dir); got != "" {
			t.Errorf("%s should never skip, got %q", eco, got)
		}
	}
}
