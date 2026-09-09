package gitrepo

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/commandrun"
)

func TestCommandRunnerGitRoundTrip(t *testing.T) {
	for _, selected := range []bool{false, true} {
		name := "default"
		if selected {
			name = "selected"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			var commands []string
			if selected {
				ctx = commandrun.WithRunner(ctx, func(_ context.Context, cmd *exec.Cmd) error {
					if cmd.Cancel != nil {
						t.Fatal("independent cancellation bypasses runner ownership")
					}
					commands = append(commands, strings.Join(cmd.Args[1:], " "))
					return cmd.Run()
				})
			}
			r, err := Init(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			data := "line\r\n\x00binary\xff\n"
			write(t, r.Dir, ".gitattributes", "* -text\n")
			write(t, r.Dir, "data", data)
			write(t, r.Dir, ".gitignore", "ignored\n")
			if changed, err := r.Commit(ctx, "fixture"); err != nil || !changed {
				t.Fatalf("commit: changed=%v err=%v", changed, err)
			}
			if !r.IsIgnored(ctx, "ignored") || r.IsIgnored(ctx, "data") {
				t.Fatal("check-ignore exit semantics changed")
			}
			if code, err := gitExitCode(ctx, r.Dir, "diff", "--quiet"); err != nil || code != 0 {
				t.Fatalf("exit status: %d %v", code, err)
			}
			blob, err := runGitStdin(ctx, r.Dir, data, nil, "hash-object", "-w", "--stdin")
			if err != nil {
				t.Fatal(err)
			}
			got, err := runGit(ctx, r.Dir, "cat-file", "blob", strings.TrimSpace(blob))
			if err != nil || got != data {
				t.Fatalf("stdin/stdout bytes changed: %q %v", got, err)
			}
			got, err = runGitEnv(ctx, r.Dir, []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=fixture.value", "GIT_CONFIG_VALUE_0=from environment"}, "config", "fixture.value")
			if err != nil || strings.TrimSpace(got) != "from environment" {
				t.Fatalf("environment override: %q %v", got, err)
			}
			archive, err := r.ArchiveTar(ctx, "HEAD", []string{"data"})
			if err != nil {
				t.Fatal(err)
			}
			tr := tar.NewReader(bytes.NewReader(archive))
			header, err := tr.Next()
			for err == nil && header.Typeflag == tar.TypeXGlobalHeader {
				header, err = tr.Next()
			}
			if err != nil || header.Name != "data" {
				t.Fatalf("archive header: %v %v", header, err)
			}
			body, err := io.ReadAll(tr)
			if err != nil || string(body) != data {
				t.Fatalf("archive bytes changed: %q %v", body, err)
			}
			if selected {
				for _, prefix := range []string{"init ", "add ", "-c commit.gpgsign=false commit ", "check-ignore ", "diff ", "hash-object ", "cat-file ", "config fixture.value", "archive "} {
					found := false
					for _, cmd := range commands {
						found = found || strings.HasPrefix(cmd, prefix)
					}
					if !found {
						t.Errorf("command bypassed selected runner: %s", prefix)
					}
				}
			}
		})
	}
}

func TestCommandRunnerDoesNotTreatCleanupFailureAsGitExit(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = dir
	exit, ok := cmd.Run().(*exec.ExitError)
	if !ok {
		t.Fatal("fixture did not produce a Git exit error")
	}
	cleanup := errors.New("cleanup unverified")
	for _, failure := range []error{exit, errors.Join(exit, cleanup)} {
		ctx := commandrun.WithRunner(t.Context(), func(context.Context, *exec.Cmd) error { return failure })
		code, err := gitExitCode(ctx, dir, "unused")
		if failure == exit {
			if code != exit.ExitCode() || err != nil {
				t.Fatalf("ordinary exit lost: %d %v", code, err)
			}
		} else if code != -1 || err != failure {
			t.Fatalf("cleanup failure became semantic exit: %d %v", code, err)
		}
		_, err = runGit(ctx, dir, "unused")
		if !errors.Is(err, failure) {
			t.Fatalf("Git diagnostic lost runner failure: %v", err)
		}
	}
}

func TestCommandRunnerRejectsUnsupportedExecution(t *testing.T) {
	ctx := commandrun.WithRunner(t.Context(), func(context.Context, *exec.Cmd) error {
		t.Fatal("unsupported operation reached runner")
		return nil
	})
	r := &Repo{Dir: t.TempDir()}
	if _, err := r.ShowPrefix(ctx, "HEAD", "data", 10); err == nil {
		t.Fatal("prefix streaming bypassed selected runner")
	}
	if err := runGitInteractive(ctx, r.Dir, "--version"); err == nil {
		t.Fatal("interactive Git bypassed selected runner")
	}
}

func TestCommandRunnerSemanticProbesPreserveCleanupFailure(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "left", "before")
	write(t, dir, "right", "after")
	cmd := exec.Command("git", "diff", "--no-index", "--quiet", "left", "right")
	cmd.Dir = dir
	exit, ok := cmd.Run().(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatal("fixture did not produce Git exit 1")
	}
	cleanup := errors.New("cleanup unverified")
	for _, failure := range []error{exit, errors.Join(exit, cleanup), fmt.Errorf("cleanup uncertain: %w", exit)} {
		ctx := commandrun.WithRunner(t.Context(), func(context.Context, *exec.Cmd) error { return failure })
		r := &Repo{Dir: dir}
		deleteErr := r.DeleteRef(ctx, "refs/heads/missing")
		ancestor, mergeErr := r.MergeBase(ctx, "left", "right")
		if ancestor != "" {
			t.Fatalf("unexpected ancestor: %q", ancestor)
		}
		for _, err := range []error{deleteErr, mergeErr} {
			if failure == exit {
				if err != nil {
					t.Fatalf("ordinary exit 1 lost: %v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatalf("cleanup failure became benign answer: %v", err)
			}
		}
	}
}
