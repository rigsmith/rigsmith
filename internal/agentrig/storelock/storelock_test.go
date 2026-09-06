package storelock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func take(t *testing.T, ctx context.Context, dir string) (context.Context, func()) {
	t.Helper()
	ctx, release, err := Acquire(ctx, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return ctx, release
}

func busy(t *testing.T, dir string) {
	t.Helper()
	_, release, err := Acquire(t.Context(), dir, 0)
	if release != nil {
		release()
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("contender: %v", err)
	}
}

func TestOwnershipAndNestedLifetimes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	ctx, release := take(t, t.Context(), dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("acquisition created clone destination: %v", err)
	}
	busy(t, dir)
	nested, endNested := take(t, ctx, dir)
	release()
	release()
	busy(t, dir) // nested borrower keeps ownership alive
	endNested()
	_, next := take(t, t.Context(), dir)
	_, unexpected, err := Acquire(nested, dir, 0) // released context is not a bypass
	if unexpected != nil {
		unexpected()
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("released lease: %v", err)
	}
	next()
	_, final := take(t, nested, dir)
	final()
	path, err := lockPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistent lock removed: %v", err)
	}
}

func TestCanonicalAliasesAndSeparateStores(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "repo")
	ctx, release := take(t, t.Context(), dir)
	// A different store can run independently, but nesting it is disallowed.
	_, other := take(t, t.Context(), filepath.Join(parent, "other"))
	other()
	if _, end, err := Acquire(ctx, filepath.Join(parent, "other"), 0); err == nil {
		end()
		t.Fatal("nested distinct store accepted")
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	busy(t, dir) // creating the destination does not change identity
	link := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(parent, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	busy(t, filepath.Join(link, "repo"))
	rootLink := filepath.Join(t.TempDir(), "store")
	if err := os.Symlink(dir, rootLink); err != nil {
		t.Fatal(err)
	}
	busy(t, rootLink)
	_, end := take(t, ctx, rootLink)
	end()
	release()
	// Alias parent must also coordinate a store that does not exist yet.
	missing := filepath.Join(parent, "not-created", "repo")
	_, end = take(t, t.Context(), missing)
	busy(t, filepath.Join(link, "not-created", "repo"))
	end()
}

func TestCaseAliasesOnCaseFoldingFilesystems(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Store")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(dir), "store")
	a, err := os.Stat(alias)
	if os.IsNotExist(err) {
		t.Skip("case-sensitive filesystem")
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(dir)
	if err != nil || !os.SameFile(a, b) {
		t.Fatal("case alias fixture is not the same directory")
	}
	ctx, _ := take(t, t.Context(), dir)
	busy(t, alias)
	_, end := take(t, ctx, alias)
	end()
}

func TestWaitCancellationAndLiveOwnerAge(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	_, release := take(t, t.Context(), dir)
	path, err := lockPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	busy(t, dir) // ownership does not expire with file age
	_, end, err := Acquire(t.Context(), dir, 30*time.Millisecond)
	if end != nil {
		end()
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("bounded wait: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, end, err = Acquire(ctx, dir, time.Hour)
	if end != nil {
		end()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	release()
	ctx, end = take(t, t.Context(), dir)
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, r, err := Acquire(canceled, dir, 0); !errors.Is(err, context.Canceled) {
		if r != nil {
			r()
		}
		t.Fatalf("canceled nested acquisition: %v", err)
	}
	end()
}

// The helper runs in a separate process so crash release and contention test
// OS ownership, not merely an in-process mutex. Readiness uses a pipe handshake.
func TestStoreLockProcessHelper(t *testing.T) {
	dir := os.Getenv("RIG_TEST_STORE_LOCK")
	if dir == "" {
		return
	}
	mode := os.Getenv("RIG_TEST_STORE_MODE")
	wait := time.Duration(0)
	if mode == "wait" {
		_, end, err := Acquire(context.Background(), dir, 0)
		if end != nil {
			end()
		}
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("waiter did not encounter owner: %v", err)
		}
		fmt.Println("waiting")
		wait = 10 * time.Second
	}
	_, release, err := Acquire(context.Background(), dir, wait)
	if mode == "busy" {
		if !errors.Is(err, ErrBusy) {
			if release != nil {
				release()
			}
			t.Fatalf("expected busy: %v", err)
		}
		fmt.Println("busy")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("owned")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
}

func child(t *testing.T, dir, mode string) (*exec.Cmd, <-chan string, func()) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStoreLockProcessHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "RIG_TEST_STORE_LOCK="+dir, "RIG_TEST_STORE_MODE="+mode)
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
	line := make(chan string, 8)
	go func() {
		defer close(line)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line <- strings.TrimSpace(scanner.Text())
		}
	}()
	return cmd, line, func() { stdin.Close() }
}

func expect(t *testing.T, lines <-chan string, want string) {
	t.Helper()
	select {
	case got := <-lines:
		if got != want {
			t.Fatalf("helper said %q, want %q", got, want)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("helper did not become ready")
	}
}

func TestProcessContentionCrashAndWaiter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	owner, ready, _ := child(t, dir, "hold")
	expect(t, ready, "owned")
	busy(t, dir)
	contender, ready, _ := child(t, dir, "busy")
	expect(t, ready, "busy")
	if err := contender.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = owner.Wait()
	ctx, release, err := Acquire(t.Context(), dir, time.Second)
	if err != nil {
		t.Fatalf("crashed owner stranded lock: %v", err)
	}
	defer release()
	waiter, ready, closeInput := child(t, dir, "wait")
	expect(t, ready, "waiting")
	release()
	expect(t, ready, "owned")
	busy(t, dir)
	closeInput()
	if err := waiter.Wait(); err != nil {
		t.Fatal(err)
	}
	_, end := take(t, ctx, dir)
	end()
}

func TestInvalidPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"", " ", file, filepath.Join(file, "child")} {
		if _, release, err := Acquire(t.Context(), dir, 0); err == nil {
			release()
			t.Fatalf("accepted %q", dir)
		}
	}
}
