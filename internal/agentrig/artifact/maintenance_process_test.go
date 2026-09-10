package artifact

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func TestArtifactWriteProcessHelper(t *testing.T) {
	dir := os.Getenv("RIG_ARTIFACT_WRITE_DIR")
	if dir == "" {
		return
	}
	s := Store{Dir: dir}
	s.reflush = func(ctx context.Context, path string) error {
		return durable.Write(ctx, path, func(f *os.File) error {
			if _, err := f.Write([]byte("interrupted archive rewrite")); err != nil {
				return err
			}
			if err := f.Sync(); err != nil {
				return err
			}
			fmt.Println("archive-write-owned")
			_, err := bufio.NewReader(os.Stdin).ReadByte()
			return err
		})
	}
	if _, err := s.Build(t.Context(), Key([]byte("retained")), func(context.Context, string) error { return errors.New("existing archive was not reused") }); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupAfterArchiveWriterProcessDeath(t *testing.T) {
	s := testStore(t)
	ref, err := s.Build(t.Context(), Key([]byte("retained")), maintenanceBuild)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(s.Dir, ".capture-work-preserve")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "retained"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArtifactWriteProcessHelper$", "-test.count=1")
	cmd.Env = []string{"RIG_ARTIFACT_WRITE_DIR=" + s.Dir}
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
		if value := os.Getenv(key); value != "" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() {
		scan := bufio.NewScanner(stdout)
		if scan.Scan() {
			ready <- scan.Text()
		} else {
			ready <- ""
		}
	}()
	select {
	case line := <-ready:
		if line != "archive-write-owned" {
			t.Fatal("unexpected helper output", line)
		}
	case <-ctx.Done():
		t.Fatal("writer readiness timeout")
	}
	if _, err := s.CleanupInterruptedWrites(t.Context()); !errors.Is(err, storelock.ErrBusy) {
		t.Fatal("removed an active writer's file", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	u, err := s.Capacity(t.Context())
	if err != nil || u.InterruptedWrites != 1 || u.Archives != 1 || u.BuildWorkspaces != 1 {
		t.Fatal(u, err)
	}
	result, err := s.CleanupInterruptedWrites(t.Context())
	if err != nil || result.RemovedFiles != 1 {
		t.Fatal(result, err)
	}
	if err := s.Verify(t.Context(), ref); err != nil {
		t.Fatal("cleanup changed retained artifact", err)
	}
	if got, err := os.ReadFile(filepath.Join(work, "retained")); err != nil || string(got) != "keep" {
		t.Fatal("cleanup changed build workspace", err)
	}
}
