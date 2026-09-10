package commitartifact

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

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func TestCleanupWorkspaceProcessHelper(t *testing.T) {
	stage := os.Getenv("RIG_CLEANUP_STAGE")
	if stage == "" {
		t.Skip("subprocess helper")
	}
	_, release, err := storelock.Acquire(t.Context(), stage, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	// Model a surviving external writer: it owns staging but no artifact lock.
	path := os.Getenv("RIG_CLEANUP_WORK")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("child-owned"); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("workspace-writer-owned")
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
}

func TestCleanupWorkspacesAfterWriterDeath(t *testing.T) {
	req := cleanupFixture(t)
	scratch := cleanupPut(t, req.Captures.Dir, ".capture-work-child/file")
	sealed := cleanupPut(t, req.Captures.Dir, "retained.capture")
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCleanupWorkspaceProcessHelper$")
	cmd.Env = []string{"RIG_CLEANUP_STAGE=" + req.StagingDir, "RIG_CLEANUP_WORK=" + scratch}
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			if scanner.Text() == "workspace-writer-owned" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child exited before ownership")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	result, err := CleanupWorkspaces(t.Context(), req)
	if !errors.Is(err, storelock.ErrBusy) || result.RemovedWorkspaces != 0 {
		t.Fatal("live external writer did not block", result, err)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	waited = true
	result, err = CleanupWorkspaces(t.Context(), req)
	if err != nil || result.RemovedWorkspaces != 1 {
		t.Fatal(result, err)
	}
	if b, err := os.ReadFile(sealed); err != nil || string(b) != "retained fixture" {
		t.Fatal("lost sealed archive", err)
	}
}
