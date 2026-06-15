package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/doctor"
)

func TestCheckChangesetConfig_ScaffoldsAbsent(t *testing.T) {
	dir := t.TempDir()
	ws := &Workspace{Root: dir, ChangesetDir: filepath.Join(dir, ".changeset"), Config: config.Default()}

	r := checkChangesetConfig(ws)
	if r.Status != doctor.Warn || r.Fix == nil {
		t.Fatalf("absent config: got %+v, want Warn with a Fix", r)
	}
	if err := r.Fix(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r2 := checkChangesetConfig(ws); r2.Status != doctor.OK {
		t.Fatalf("after scaffold: %+v, want OK", r2)
	}
}

func TestCheckChangesetConfig_InvalidIsFailNotFixable(t *testing.T) {
	dir := t.TempDir()
	cd := filepath.Join(dir, ".changeset")
	if err := os.MkdirAll(cd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cd, "config.json"), []byte("{ not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{Root: dir, ChangesetDir: cd, Config: config.Default()}

	// A broken-but-present config is a hard fail with no auto-fix — scaffolding is
	// deliberately non-destructive and must not clobber the user's file.
	r := checkChangesetConfig(ws)
	if r.Status != doctor.Fail || r.Fix != nil {
		t.Fatalf("invalid config: got %+v, want Fail with no Fix", r)
	}
}

func TestUniqueEcosystems(t *testing.T) {
	got := UniqueEcosystems(map[string]string{"a": "node", "b": "go", "c": "node"})
	if len(got) != 2 || got[0] != "go" || got[1] != "node" {
		t.Fatalf("UniqueEcosystems = %v, want [go node]", got)
	}
}
