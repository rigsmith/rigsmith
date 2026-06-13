package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMixedRepo makes a dir that is ambiguous to NearestEcosystem (a go.mod and
// a package.json at the same level).
func writeMixedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestResolvePrimary_AmbiguousErrorsOffTTY(t *testing.T) {
	dir := writeMixedRepo(t)
	// Tests run without a TTY, so the picker is gated off and the error stands.
	if _, err := resolvePrimary(dir, dir); err == nil || !strings.Contains(err.Error(), "multiple ecosystems") {
		t.Fatalf("ambiguous repo off a TTY should error with 'multiple ecosystems', got %v", err)
	}
}

func TestResolvePrimary_ConfigEcosystemWins(t *testing.T) {
	dir := writeMixedRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".rig.json"), []byte(`{"ecosystem":"go"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	eco, err := resolvePrimary(dir, dir)
	if err != nil || eco != "go" {
		t.Fatalf("pinned ecosystem should win: got %q, %v", eco, err)
	}
}

func TestResolvePrimary_UsesCachedPick(t *testing.T) {
	dir := writeMixedRepo(t)
	pickedEcosystem[dir] = "node" // simulate a prior interactive pick this process
	t.Cleanup(func() { delete(pickedEcosystem, dir) })
	eco, err := resolvePrimary(dir, dir)
	if err != nil || eco != "node" {
		t.Fatalf("a cached pick should be reused without erroring: got %q, %v", eco, err)
	}
}

func TestPickPrimaryEcosystem_NonInteractiveReturnsFalse(t *testing.T) {
	if _, ok := pickPrimaryEcosystem(t.TempDir(), []string{"go", "node"}); ok {
		t.Fatal("the picker must not run (or write anything) off a TTY")
	}
}
