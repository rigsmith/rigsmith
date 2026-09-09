package storelock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requireFenced(t *testing.T, ctx context.Context, dir string) {
	t.Helper()
	_, release, err := Acquire(ctx, dir, 0)
	if release != nil {
		release()
	}
	if !errors.Is(err, ErrFenced) {
		t.Fatalf("store was not fenced: %v", err)
	}
}

func TestFenceSurvivesLeaseRelease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	ctx, release := take(t, t.Context(), dir)
	fence, err := BeginFence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireFenced(t, ctx, dir) // Borrowing cannot bypass a dirty command.
	if _, err := BeginFence(ctx); !errors.Is(err, ErrFenced) {
		t.Fatalf("second command: %v", err)
	}
	release()
	requireFenced(t, t.Context(), dir)
	requireFenced(t, ctx, dir) // Nor can an expired context.
	if err := fence.Clear(); err == nil {
		t.Fatal("cleared without an active lease")
	}
}

func TestFenceVerifiedCompletionAndStaleIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	ctx, release := take(t, t.Context(), dir)
	first, err := BeginFence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Clear(); err != nil {
		t.Fatal(err)
	}
	second, err := BeginFence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Clear(); !errors.Is(err, ErrFenced) {
		t.Fatalf("old command cleared new fence: %v", err)
	}
	requireFenced(t, ctx, dir)
	if err := second.Clear(); err != nil {
		t.Fatal(err)
	}
	release()
	_, end := take(t, t.Context(), dir)
	end()
}

func TestFenceRejectsMissingExpiredAndCanceledLeases(t *testing.T) {
	if _, err := BeginFence(t.Context()); err == nil {
		t.Fatal("missing lease accepted")
	}
	ctx, release := take(t, t.Context(), filepath.Join(t.TempDir(), "store"))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := BeginFence(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release()
	if _, err := BeginFence(ctx); err == nil {
		t.Fatal("expired lease accepted")
	}
}

func TestFenceRejectsDamagedAndUnknownRecords(t *testing.T) {
	for _, data := range [][]byte{{0}, []byte("agentrig-command-fence-v2:unknown"), make([]byte, fenceSize), make([]byte, fenceSize+1)} {
		dir := filepath.Join(t.TempDir(), "store")
		path, err := lockPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		requireFenced(t, t.Context(), dir)
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = InheritedFence(f)
		f.Close()
		if !errors.Is(err, ErrFenced) {
			t.Fatalf("damaged record adopted: %v", err)
		}
	}
}

func TestFenceCrashHelper(t *testing.T) {
	dir := os.Getenv("RIG_FENCE_CRASH")
	if dir == "" {
		return
	}
	ctx, _, err := Acquire(context.Background(), filepath.Join(dir, "store"), 0)
	if err != nil {
		os.Exit(2)
	}
	if _, err := BeginFence(ctx); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0600); err != nil {
		os.Exit(4)
	}
	time.Sleep(time.Minute)
	os.Exit(5)
}

func TestFenceSurvivesProcessDeath(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFenceCrashHelper$")
	cmd.Env = append(os.Environ(), "RIG_FENCE_CRASH="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fenced worker did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	for range 2 {
		requireFenced(t, t.Context(), filepath.Join(dir, "store"))
	}
	// The ordinary acquisition path remains healthy for other stores.
	_, release := take(t, t.Context(), filepath.Join(dir, "other"))
	release()
}
