package gitrepo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if _, err := r.CommitSubtree(ctx, "saved", []string{"data"}, "retry"); !errors.Is(err, failure) {
		t.Fatalf("retry lost cleanup failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, ".git", "clauderig-idx-saved")); err != nil {
		t.Fatalf("removed potentially live index: %v", err)
	}
}

func TestSelectedInitRejectsConfigurationFailure(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "missing-config"))
	for _, failed := range []string{"config commit.gpgsign false", "config user.email", "config user.email clauderig@localhost", "config user.name", "config user.name clauderig"} {
		t.Run(failed, func(t *testing.T) {
			dir := t.TempDir()
			var injected error
			ctx := commandrun.WithRunner(t.Context(), func(_ context.Context, cmd *exec.Cmd) error {
				if injected != nil {
					t.Fatalf("ran %v after configuration failed", cmd.Args)
				}
				if strings.Join(cmd.Args[1:], " ") == failed {
					bad := exec.Command("git", "rev-parse", "--verify", "refs/heads/absent")
					bad.Dir = dir
					injected = bad.Run()
					if _, ok := injected.(*exec.ExitError); !ok {
						t.Fatalf("fixture must return direct Git exit: %v", injected)
					}
					return injected
				}
				return cmd.Run()
			})
			repo, err := Init(ctx, dir)
			if repo != nil || injected == nil || !errors.Is(err, injected) {
				t.Fatalf("Init hid configuration error: repo=%v err=%v injected=%v", repo, err, injected)
			}
		})
	}
}
