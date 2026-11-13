package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/secrets"
	"github.com/spf13/cobra"
)

// shiprig reads the Doppler block the way rig does: the repo's .rig.json,
// else the user-wide one layered under it.
func TestAttachSecretsReaderUsesTheGlobalBlockWhenTheRepoHasNone(t *testing.T) {
	prev := secrets.InCI
	secrets.InCI = func() bool { return false }
	t.Cleanup(func() { secrets.InCI = prev })

	home := t.TempDir()
	global := filepath.Join(home, ".rig.json")
	writeFile(t, home, ".rig.json", `{ "doppler": { "project": "owlwatch", "release": "release" } }`)
	t.Setenv("RIG_GLOBAL_CONFIG", global)

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	if got := attachedSource(t); !strings.Contains(got, "owlwatch/release") {
		t.Fatalf("with no repo block, source = %q; want the global owlwatch/release", got)
	}

	writeFile(t, repo, ".rig.json", `{ "doppler": { "project": "foxcraft", "release": "release" } }`)
	if got := attachedSource(t); !strings.Contains(got, "foxcraft/release") {
		t.Fatalf("with a repo block, source = %q; want the repo's foxcraft/release", got)
	}
}

func attachedSource(t *testing.T) string {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := attachSecretsReader(cmd, nil); err != nil {
		t.Fatal(err)
	}
	r := secrets.ReaderFrom(cmd.Context())
	if r == nil {
		t.Fatal("no secrets reader attached")
	}
	return r.Describe("FEEDZ_API_KEY")
}
