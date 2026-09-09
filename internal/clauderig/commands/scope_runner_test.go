package commands

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/commandrun"
	"github.com/rigsmith/rigsmith/core/gitrepo"
)

func TestScopeIgnoreRefusesFailedSelectedProbe(t *testing.T) {
	for _, target := range []string{"rev-parse", "check-ignore"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			if _, err := gitrepo.Init(t.Context(), root); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, ".gitignore")
			before := []byte("existing-rule\n")
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("uncertain cleanup")
			ctx := commandrun.WithRunner(t.Context(), func(_ context.Context, cmd *exec.Cmd) error {
				if cmd.Args[1] == target {
					return failure
				}
				return cmd.Run()
			})
			if _, err := ensureLocalIgnored(ctx, root); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != string(before) {
				t.Fatalf("ignore file changed after failed %s: %q %v", target, got, err)
			}
		})
	}
}
