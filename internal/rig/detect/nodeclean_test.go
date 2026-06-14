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
