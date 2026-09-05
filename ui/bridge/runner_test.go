package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCLI writes a script standing in for the clauderig binary and returns a
// runner wired to it.
func fakeCLI(t *testing.T, body string) *runner {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-in is POSIX-only")
	}
	p := filepath.Join(t.TempDir(), cliName)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &runner{resolve: func() (string, error) { return p, nil }}
}

func collect(t *testing.T, r *runner, a Action, arg ...string) ([]Line, error) {
	t.Helper()
	var id string
	if len(arg) > 0 {
		id = arg[0]
	}
	var mu sync.Mutex
	var lines []Line
	err := r.run(context.Background(), a, id, func(l Line) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, l)
	})
	return lines, err
}

func TestRunStreamsBothStreams(t *testing.T) {
	r := fakeCLI(t, "echo out-one; echo err-one 1>&2; echo out-two\n")

	lines, err := collect(t, r, ActionSync)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr []string
	for _, l := range lines {
		if l.Stream == "stderr" {
			stderr = append(stderr, l.Text)
		} else {
			stdout = append(stdout, l.Text)
		}
	}
	if strings.Join(stdout, ",") != "out-one,out-two" {
		t.Errorf("stdout = %v", stdout)
	}
	if len(stderr) != 1 || stderr[0] != "err-one" {
		t.Errorf("stderr = %v", stderr)
	}
}

// A nonzero exit is a real outcome — a refused sync, a merge with residual
// conflicts — and its output still has to reach the drawer.
func TestRunReportsFailureWithOutput(t *testing.T) {
	r := fakeCLI(t, "echo 'LEAK env.KEY'; echo 'refusing to push' 1>&2; exit 1\n")

	lines, err := collect(t, r, ActionSync)
	if err == nil {
		t.Fatal("nonzero exit should surface as an error")
	}
	if len(lines) != 2 {
		t.Fatalf("output lost on failure: %v", lines)
	}
}

// Two syncs at once would race on the staging repo's index.
func TestRunRejectsConcurrent(t *testing.T) {
	r := fakeCLI(t, "sleep 0.4\n")

	started := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		close(started)
		_, err := collect(t, r, ActionSync)
		firstDone <- err
	}()
	<-started

	// Give the first run time to take the lock.
	deadline := time.Now().Add(2 * time.Second)
	for !r.busy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !r.busy() {
		t.Fatal("first action never reported busy")
	}

	if _, err := collect(t, r, ActionPull); !errors.Is(err, ErrBusy) {
		t.Fatalf("second action err = %v, want ErrBusy", err)
	}

	// Channel, not a shared variable: the busy() spin is not a happens-before
	// edge, so reading the error any other way is itself a race.
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first action failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first action never finished")
	}
	// The lock is released, so the next action can run.
	if r.busy() {
		t.Error("still busy after completion")
	}
}

// The window is a webview; "run this command" must never be reachable from
// inside it. Only allowlisted verbs map to argv.
func TestOnlyAllowlistedActions(t *testing.T) {
	for _, a := range []Action{ActionSync, ActionPull, ActionMerge} {
		if !Allowed(a) {
			t.Errorf("%q should be allowed", a)
		}
	}
	for _, a := range []Action{"", "restore", "sync; rm -rf /", "--version", "account switch"} {
		if Allowed(a) {
			t.Errorf("%q must not be runnable", a)
		}
	}

	// An action's argument is the one place a string from the window reaches a
	// command line, so it is validated to a narrow id shape — nothing a shell
	// or a flag parser could reinterpret.
	for _, bad := range []string{"", "--force", "a b", "../../etc/passwd", "x;y", "$(id)", "-rf"} {
		if _, err := argvFor(ActionMaterialize, bad); err == nil {
			t.Errorf("materialize accepted argument %q", bad)
		}
	}
	if got, err := argvFor(ActionMaterialize, "cd98a139-f01b-4cb3-b43e-aca92dd4a001"); err != nil ||
		len(got) != 3 || got[0] != "peek" || got[1] != "materialize" {
		t.Errorf("argv = %v, err = %v", got, err)
	}
	// An action that takes no id must reject one.
	if _, err := argvFor(ActionSync, "something"); err == nil {
		t.Error("sync accepted an argument")
	}

	r := fakeCLI(t, "echo hi\n")
	if _, err := collect(t, r, "definitely-not-a-verb"); err == nil {
		t.Error("unknown action was executed")
	}
}

// lipgloss escapes would render as garbage in the drawer.
func TestOutputIsStrippedOfANSI(t *testing.T) {
	r := fakeCLI(t, "printf '\\033[32m✓ synced\\033[0m\\n'\n")

	lines, err := collect(t, r, ActionSync)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines", len(lines))
	}
	if strings.Contains(lines[0].Text, "\x1b") {
		t.Errorf("escape survived: %q", lines[0].Text)
	}
	if !strings.Contains(lines[0].Text, "✓ synced") {
		t.Errorf("stripping ate the text: %q", lines[0].Text)
	}
}

// The CLI pads its styled blocks with blank rows; they carry nothing here.
func TestBlankLinesDropped(t *testing.T) {
	r := fakeCLI(t, "echo real; echo ''; echo '   '; echo alsoreal\n")

	lines, err := collect(t, r, ActionSync)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("blank rows not dropped: %v", lines)
	}
}

func TestMissingBinaryIsAClearError(t *testing.T) {
	r := &runner{resolve: func() (string, error) { return "", errors.New("can't find the clauderig binary") }}
	if _, err := collect(t, r, ActionSync); err == nil || !strings.Contains(err.Error(), "clauderig binary") {
		t.Fatalf("err = %v", err)
	}
	// A failed resolve must still release the lock.
	if r.busy() {
		t.Error("runner stayed busy after a resolve failure")
	}
}

// A packaged app ships its own CLI; that copy must win over whatever is on PATH.
func TestResolvePrefersSibling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits differ on windows")
	}
	dir := t.TempDir()
	sibling := filepath.Join(dir, cliName)
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isExecutableFile(sibling) {
		t.Fatal("fixture is not executable")
	}
	// A non-executable file of the same name is not a candidate.
	plain := filepath.Join(t.TempDir(), cliName)
	if err := os.WriteFile(plain, []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isExecutableFile(plain) {
		t.Error("a non-executable file was treated as the binary")
	}
	if isExecutableFile(dir) {
		t.Error("a directory was treated as the binary")
	}
}
