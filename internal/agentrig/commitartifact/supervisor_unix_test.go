//go:build linux || darwin

package commitartifact

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func retainedSupervisorContext(t *testing.T, ctx context.Context, dir string) context.Context {
	t.Helper()
	ctx, release, err := storelock.Acquire(ctx, dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return process.WithSupervisor(ctx, os.Args[0], "-test.run=^TestRetainedSupervisorEntrypoint$")
}

func TestRetainedSupervisorEntrypoint(t *testing.T) {
	if len(os.Args) != 2 || os.Args[1] != "-test.run=^TestRetainedSupervisorEntrypoint$" {
		return
	}
	os.Exit(process.ServeSupervisor())
}

func TestRetainedSupervisedCommandCleanup(t *testing.T) {
	// Race binaries otherwise sleep a second on every os.Exit. Head validation
	// starts many commands, so those synthetic delays can exhaust the cleanup
	// deadline even after every helper has stopped. Keep race instrumentation
	// and the existing deadlines; disable only this subprocess exit delay.
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	// Reuse all existing exit/cancel/output-failure scenarios through the real
	// retained command, transport and streaming builders (including WaitDelay).
	retainedCommandCleanup(t, retainedSupervisorContext)
}

func TestRetainedSupervisedGit(t *testing.T) {
	repo, seed := seedRepository(t, "sha1")
	ctx := retainedSupervisorContext(t, t.Context(), repo.dir)
	got, err := repo.run(ctx, nil, "rev-parse", "refs/heads/main")
	if err != nil || strings.TrimSpace(got) != seed {
		t.Fatalf("supervised ref: %q %v", got, err)
	}
	_, err = repo.run(ctx, nil, "show-ref", "--verify", "--quiet", "refs/heads/absent")
	if !gitExited(err, 1) {
		t.Fatalf("lost ordinary Git exit classification: %v", err)
	}
	const payload = "retained bytes\x00\xff\n"
	oid, err := repo.run(ctx, strings.NewReader(payload), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	var content []byte
	err = repo.stream(ctx, nil, func(r io.Reader) error {
		var err error
		content, err = io.ReadAll(r)
		return err
	}, "cat-file", "blob", strings.TrimSpace(oid))
	if err != nil || string(content) != payload {
		t.Fatalf("supervised blob: %q %v", content, err)
	}
}
