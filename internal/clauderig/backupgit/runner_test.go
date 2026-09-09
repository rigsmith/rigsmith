//go:build linux || darwin || windows

package backupgit

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/core/commandrun"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func TestCanonicalRunnerSupervisorEntrypoint(t *testing.T) {
	if len(os.Args) == 2 && os.Args[1] == "-test.run=^TestCanonicalRunnerSupervisorEntrypoint$" {
		os.Exit(process.ServeSupervisor())
	}
}

func TestSupervisedCanonicalGitRoundTrip(t *testing.T) {
	root := setup(t)
	data := []byte("original\r\nbytes\r\n$Id$\x00\xff\n")
	if err := os.WriteFile(filepath.Join(root, "data.jsonl"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Establish a legacy index whose conversion policy needs migration.
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "legacy"}} {
		if _, err := git(t.Context(), root, nil, args...); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ctx, release, err := storelock.Acquire(ctx, root, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx = process.WithSupervisor(ctx, os.Args[0], "-test.run=^TestCanonicalRunnerSupervisorEntrypoint$")
	var commands []string
	ctx = commandrun.WithRunner(ctx, func(ctx context.Context, cmd *exec.Cmd) error {
		commands = append(commands, strings.Join(cmd.Args[1:], " "))
		err := process.Run(ctx, cmd)
		// A completed command (including a semantic nonzero exit) must have
		// cleared its fence before the next command borrows this staging lease.
		_, nestedRelease, lockErr := storelock.Acquire(ctx, root, 0)
		if lockErr != nil {
			t.Fatalf("command returned with a dirty fence: %v (command: %v)", lockErr, err)
		}
		nestedRelease()
		return err
	})
	if err := Prepare(ctx, root); err != nil {
		t.Fatal(err)
	}
	staged, err := git(ctx, root, nil, "show", ":data.jsonl")
	if err != nil || !bytes.Equal(staged, data) {
		t.Fatalf("supervised attribute migration changed bytes: %q %v", staged, err)
	}
	r, err := gitrepo.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.Commit(ctx, "preserve bytes"); err != nil || !changed {
		t.Fatalf("commit: changed=%v err=%v", changed, err)
	}
	if changed, err := r.CommitSubtree(ctx, "config-history", []string{"data.jsonl"}, "snapshot"); err != nil || !changed {
		t.Fatalf("temporary-index commit: changed=%v err=%v", changed, err)
	}
	archive, err := r.ArchiveTar(ctx, "config-history", []string{"data.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(archive))
	header, err := tr.Next()
	for err == nil && header.Typeflag == tar.TypeXGlobalHeader {
		header, err = tr.Next()
	}
	if err != nil || header.Name != "data.jsonl" {
		t.Fatalf("archive header: %v %v", header, err)
	}
	body, err := io.ReadAll(tr)
	if err != nil || !bytes.Equal(body, data) {
		t.Fatalf("supervised archive changed bytes: %q %v", body, err)
	}
	for _, prefix := range []string{"check-attr ", "add --renormalize", "add --force", "-c commit.gpgsign=false commit ", "write-tree", "commit-tree ", "archive "} {
		found := false
		for _, command := range commands {
			found = found || strings.HasPrefix(command, prefix)
		}
		if !found {
			t.Errorf("command bypassed supervisor: %s", prefix)
		}
	}
	release()
	_, nextRelease, err := storelock.Acquire(t.Context(), root, 0)
	if err != nil {
		t.Fatalf("completed operation left store unavailable: %v", err)
	}
	nextRelease()
}
