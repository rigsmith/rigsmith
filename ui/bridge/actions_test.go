package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// recorder captures the event stream the window would receive.
type recorder struct {
	mu     sync.Mutex
	names  []string
	lines  []Line
	done   Done
	doneCh chan struct{}
}

func newRecorder() *recorder { return &recorder{doneCh: make(chan struct{})} }

func (r *recorder) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
	switch v := data.(type) {
	case Line:
		r.lines = append(r.lines, v)
	case Done:
		r.done = v
		close(r.doneCh)
	}
}

func (r *recorder) wait(t *testing.T) Done {
	t.Helper()
	select {
	case <-r.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("action never emitted Done")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

// actionsWith builds a service whose runner points at a stand-in CLI.
func actionsWith(t *testing.T, body string) (*Actions, *recorder) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-in is POSIX-only")
	}
	p := filepath.Join(t.TempDir(), cliName)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	a := NewActionsWithEmit(rec.emit)
	a.run.resolve = func() (string, error) { return p, nil }
	return a, rec
}

// The window's whole contract: a start, the lines as they arrive, then a done.
func TestActionEmitsStartLinesDone(t *testing.T) {
	a, rec := actionsWith(t, "echo one; echo two\n")

	if err := a.Run(context.Background(), "sync"); err != nil {
		t.Fatal(err)
	}
	done := rec.wait(t)

	if !done.OK || done.Action != "sync" || done.Error != "" {
		t.Fatalf("done = %+v", done)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.names) < 3 || rec.names[0] != EventActionStart || rec.names[len(rec.names)-1] != EventActionDone {
		t.Fatalf("event order wrong: %v", rec.names)
	}
	if len(rec.lines) != 2 {
		t.Errorf("lines = %v", rec.lines)
	}
}

// A nonzero exit is a real outcome, not a crash: Done says so and the CLI's own
// output has already streamed.
func TestActionReportsFailure(t *testing.T) {
	a, rec := actionsWith(t, "echo 'refusing to push' 1>&2; exit 1\n")

	if err := a.Run(context.Background(), "sync"); err != nil {
		t.Fatalf("Run should accept the action and report through Done: %v", err)
	}
	done := rec.wait(t)
	if done.OK || done.Error == "" {
		t.Fatalf("done = %+v", done)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.lines) != 1 || rec.lines[0].Stream != "stderr" {
		t.Errorf("stderr line not streamed: %v", rec.lines)
	}
}

// Run's context belongs to the frontend call and dies when it returns. A sync
// must not be killed by that — this is the bug the goroutine's own background
// context exists to prevent.
func TestActionSurvivesCallerContextCancellation(t *testing.T) {
	a, rec := actionsWith(t, "sleep 0.3; echo finished\n")

	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Run(ctx, "sync"); err != nil {
		t.Fatal(err)
	}
	cancel() // exactly what Wails does once the bound call returns

	done := rec.wait(t)
	if !done.OK {
		t.Fatalf("action died with its caller's context: %+v", done)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.lines) != 1 || rec.lines[0].Text != "finished" {
		t.Errorf("command did not run to completion: %v", rec.lines)
	}
}

// An unknown verb is rejected synchronously, so the window can show it inline
// rather than waiting for an event that never comes.
func TestActionRejectsUnknownVerb(t *testing.T) {
	a, rec := actionsWith(t, "echo hi\n")

	if err := a.Run(context.Background(), "restore"); err == nil {
		t.Fatal("unallowlisted verb was accepted")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.names) != 0 {
		t.Errorf("rejected action still emitted events: %v", rec.names)
	}
}

func TestActionRejectsWhenBusy(t *testing.T) {
	a, rec := actionsWith(t, "sleep 0.4\n")

	if err := a.Run(context.Background(), "sync"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !a.run.busy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if err := a.Run(context.Background(), "pull"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second action err = %v, want ErrBusy", err)
	}
	if !a.Busy(context.Background()) {
		t.Error("Busy should report true mid-action")
	}
	rec.wait(t)
	if a.Busy(context.Background()) {
		t.Error("Busy should clear once the action finishes")
	}
}
