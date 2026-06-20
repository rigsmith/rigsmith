package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// fakeDotnetEchoingArg installs a `dotnet` on PATH whose `list` output is the
// project path it was handed (argv[2]), with an artificial delay that finishes
// EARLIER-started projects LAST — so a fan-out that appended results as they
// completed would scramble the order, and only index-keyed writes survive.
func fakeDotnetEchoingArg(t *testing.T, total int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake dotnet uses a POSIX shell script")
	}
	dir := t.TempDir()
	// argv after "dotnet": list <fullpath> package ...; basename is a 0-padded
	// index, so sleeping (total-index) reverses completion vs start order.
	body := fmt.Sprintf("#!/bin/sh\nn=${2##*/}\nsleep \"0.$(printf '%%02d' $((%d - 10#$n)))\"\nprintf '%%s' \"$2\"\n", total)
	if err := os.WriteFile(filepath.Join(dir, "dotnet"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestDotnetListAcross_PreservesProjectOrder(t *testing.T) {
	const n = 12 // > the concurrency cap, so completion genuinely interleaves
	fakeDotnetEchoingArg(t, n)

	projects := make([]detect.ProjectInfo, n)
	for i := range projects {
		projects[i] = detect.ProjectInfo{FullPath: fmt.Sprintf("/p/%02d", i)}
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background()) // RunE always has one; a bare command doesn't
	cmd.SetErr(&bytes.Buffer{})          // non-TTY writer ⇒ static line, no animation

	outs := dotnetListAcross(cmd, t.TempDir(), projects, "--format", "json")

	if len(outs) != n {
		t.Fatalf("got %d outputs, want %d", len(outs), n)
	}
	for i, out := range outs {
		if want := projects[i].FullPath; out != want {
			t.Errorf("outs[%d] = %q, want %q (order not preserved)", i, out, want)
		}
	}
}

func TestStartReviewProgress_NonTTYAnnouncesOnce(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)

	var done int64
	stop := startReviewProgress(cmd, 5, &done)
	stop()

	if got := buf.String(); got == "" || !bytes.Contains(buf.Bytes(), []byte("5 .NET projects")) {
		t.Errorf("expected a one-line scope notice naming the count, got %q", got)
	}
}

func TestStartReviewProgress_SingleProjectIsSilent(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)

	var done int64
	startReviewProgress(cmd, 1, &done)() // not worth announcing one project

	if buf.Len() != 0 {
		t.Errorf("expected no output for a single project, got %q", buf.String())
	}
}
