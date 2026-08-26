//go:build !windows

// A named pipe under projects/ is only expressible where mkfifo exists. The
// build tag rather than a runtime skip: syscall.Mkfifo is undefined on Windows,
// so a guarded call still fails to COMPILE there.

package commands

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// Ranking opens each transcript's tail to date it, and os.Open on a FIFO blocks
// until a writer appears — so a uuid-named pipe under projects/ would hang the
// picker and every <Tab> that completes --session, with nothing on screen to
// explain it.
func TestLiveTranscripts_SkipsNonRegularFiles(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "projects", "-Users-j-Git-api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	real := "11111111-1111-1111-1111-111111111111"
	writeTranscript(t, home, "-Users-j-Git-api", real, "hello")

	fifo := filepath.Join(dir, "22222222-2222-2222-2222-222222222222.jsonl")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create a fifo here: %v", err)
	}

	// A bare os.Open on the fifo would block forever, so the test itself has to
	// be bounded: a hang here is the regression, not a slow machine.
	done := make(chan []sessionCandidate, 1)
	go func() {
		got, err := recentSessions(home, session.Index{}, 0)
		if err != nil {
			done <- nil
			return
		}
		done <- got
	}()
	select {
	case got := <-done:
		if len(got) != 1 || got[0].ID != real {
			t.Errorf("want only the real transcript, got %+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("recentSessions blocked — a non-regular file reached the reader")
	}
}
