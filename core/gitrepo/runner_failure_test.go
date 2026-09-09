package gitrepo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/commandrun"
)

func TestIgnoredRunnerFailureCannotAuthorizeFileWrites(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "conflict", "keep this content")
	failure := errors.New("unverified cleanup")
	calls := 0
	ctx := commandrun.WithRunner(t.Context(), func(context.Context, *exec.Cmd) error { calls++; return failure })
	r := &Repo{Dir: dir}
	if r.IsIgnored(ctx, "conflict") {
		t.Fatal("failed probe reported ignored")
	}
	if err := r.ResolveWith(ctx, "conflict", []byte("unsafe replacement")); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "conflict")); err != nil || string(data) != "keep this content" {
		t.Fatalf("mutated after boolean hid failure: %q %v", data, err)
	}
	newDir := filepath.Join(dir, "new")
	if _, err := Init(ctx, newDir); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := os.Stat(newDir); !os.IsNotExist(err) {
		t.Fatalf("created directory after failure: %v", err)
	}
	if calls != 1 {
		t.Fatalf("started another command after failure: %d", calls)
	}
}

func TestUncertainTemporaryIndexIsPreserved(t *testing.T) {
	r, err := Init(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, r.Dir, "data", "original")
	if _, err := r.Commit(t.Context(), "fixture"); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("helper may still be using the index")
	ctx := commandrun.WithRunner(t.Context(), func(_ context.Context, cmd *exec.Cmd) error {
		if len(cmd.Args) > 1 && cmd.Args[1] == "add" {
			if err := cmd.Run(); err != nil {
				return err
			}
			return failure
		}
		return cmd.Run()
	})
	if _, err := r.CommitSubtree(ctx, "saved", []string{"data"}, "history"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, ".git", "clauderig-idx-saved")); err != nil {
		t.Fatalf("removed potentially live index: %v", err)
	}
}
