package commitartifact

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func waitCleanupMarker(ctx context.Context, path string) bool {
	for ctx.Err() == nil {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

type rejectedOutput struct{}

func (rejectedOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRetainedCommandCleanup(t *testing.T) {
	// Use a real executable on each OS, so Windows also exercises job ownership
	// rather than going through a shell with different process/pipe behavior.
	bin := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(bin, name), "testdata/processhelper.go")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, out)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := gitRepo{dir: t.TempDir()}
	for _, tc := range []struct{ name, mode, runner string }{
		{"command-exit", "return", "run"},
		{"command-cancel", "wait", "run"},
		{"command-overflow", "overflow", "run"},
		{"command-write-failure", "overflow", "write"},
		{"head-probe-failure", "head-overflow", "head"},
		{"stream-exit", "return", "stream"},
		{"stream-cancel", "wait", "stream"},
		{"stream-reject", "reject", "stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "child")
			t.Setenv("RIG_RETAINED_HELPER_MARKER", marker)
			t.Setenv("RIG_RETAINED_HELPER_MODE", tc.mode)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if tc.mode == "wait" {
				go func() {
					if waitCleanupMarker(ctx, marker) {
						cancel()
					}
				}()
			}
			start := time.Now()
			var err error
			if tc.runner == "run" {
				var output string
				output, err = repo.run(ctx, nil, "synthetic")
				if err != nil && output != "" {
					t.Fatal("returned partial output", output)
				}
			} else if tc.runner == "head" {
				if err := os.MkdirAll(filepath.Join(repo.dir, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
				_, err = SettledHead(ctx, repo.dir)
			} else if tc.runner == "write" {
				err = repo.runTo(ctx, nil, rejectedOutput{}, "synthetic")
			} else {
				err = repo.stream(ctx, nil, func(r io.Reader) error {
					if tc.mode == "reject" {
						var first [1]byte
						if _, err := io.ReadFull(r, first[:]); err != nil {
							return err
						}
						return ErrInvalid
					}
					_, err := io.Copy(io.Discard, r)
					return err
				}, "synthetic")
			}
			switch tc.mode {
			case "return":
				if err != nil {
					t.Fatal(err)
				}
			case "wait":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "overflow", "head-overflow":
				expected := artifact.ErrTooLarge
				if tc.runner == "write" {
					expected = io.ErrClosedPipe
				}
				if !errors.Is(err, expected) {
					t.Fatal(err)
				}
			case "reject":
				if !errors.Is(err, ErrInvalid) {
					t.Fatal(err)
				}
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("waited for helper instead of cleaning it up")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("helper did not start", err)
			}
			if err := os.WriteFile(marker+".released", nil, 0600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Second)
			if _, err := os.Stat(marker + ".late"); !os.IsNotExist(err) {
				t.Fatal("helper executed after operation returned", err)
			}
		})
	}
	t.Run("bidirectional-stream", func(t *testing.T) {
		t.Setenv("RIG_RETAINED_HELPER_MODE", "copy")
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		payload := strings.Repeat("binary\x00bytes\r\n", 128<<10)
		var got strings.Builder
		err := repo.stream(ctx, strings.NewReader(payload), func(r io.Reader) error {
			_, err := io.Copy(&got, r)
			return err
		}, "synthetic")
		if err != nil || got.String() != payload {
			t.Fatalf("stream changed bytes or deadlocked: %v", err)
		}
	})
	t.Run("clean-exit-status", func(t *testing.T) {
		t.Setenv("RIG_RETAINED_HELPER_MODE", "exit")
		_, err := repo.run(t.Context(), nil, "synthetic")
		if !gitExited(err, 1) {
			t.Fatal("lost clean exit status", err)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal("lost underlying exit", err)
		}
		for _, mixed := range []error{errors.Join(exit, errors.New("cleanup failed")), errors.Join(context.Canceled, exit)} {
			if gitExited(commandError("synthetic", mixed), 1) {
				t.Fatal("classified cleanup/cancellation as semantic exit")
			}
		}
	})
	t.Run("missing-command", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		err := repo.stream(ctx, nil, func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err }, "synthetic")
		if err == nil || ctx.Err() != nil {
			t.Fatal("startup failure did not close stream promptly", err)
		}
	})
}
