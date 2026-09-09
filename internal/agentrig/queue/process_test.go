package queue

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestQueueProcessHelper(t *testing.T) {
	dir := os.Getenv("RIG_QUEUE_TEST_DIR")
	if dir == "" {
		return
	}
	q, err := Open(t.Context(), dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	mode := os.Getenv("RIG_QUEUE_TEST_MODE")
	switch mode {
	case "coverage":
		prepareCoverage(t, worker(t, q))
		fmt.Println("coverage-owned")
		_, _ = bufio.NewReader(os.Stdin).ReadByte()
	case "coverage-ack-before", "coverage-ack-after":
		c := prepareCoverage(t, worker(t, q))
		// This synthetic boundary represents publication already confirmed by
		// the vendor, followed by process death around the queue receipt write.
		q.save = func(dir string, data []byte) error {
			if mode == "coverage-ack-after" {
				if err := saveFile(dir, data); err != nil {
					t.Fatal(err)
				}
			}
			fmt.Println("coverage-interrupted")
			os.Exit(0)
			return nil
		}
		if _, err := c.Acknowledge(t.Context(), []uint64{1}); err != nil {
			t.Fatal(err)
		}
	case "owner":
		w := worker(t, q)
		b := next(t, w)
		if err := w.Progress(t.Context(), b.ID, Captured, "durable-snapshot"); err != nil {
			t.Fatal(err)
		}
		if err := w.Progress(t.Context(), b.ID, Committed, "local-commit"); err != nil {
			t.Fatal(err)
		}
		fmt.Println("owned")
		_, _ = bufio.NewReader(os.Stdin).ReadByte()
	case "before", "after":
		q.save = func(dir string, data []byte) error {
			if mode == "after" {
				if err := saveFile(dir, data); err != nil {
					t.Fatal(err)
				}
			} else {
				f, err := os.CreateTemp(dir, ".queue-*")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.Write(data[:len(data)/2]); err != nil {
					t.Fatal(err)
				}
				if err = f.Sync(); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
			// Exit before Enqueue returns: the parent sees no enqueue acknowledgement.
			fmt.Println("interrupted")
			os.Exit(0)
			return nil
		}
		enqueue(t, q, request("interrupted"))
	default:
		for i := range 4 {
			enqueue(t, q, request(fmt.Sprintf("%s-%d", mode, i)))
		}
		fmt.Println("enqueued")
	}
}
func subprocess(t *testing.T, dir, mode string) (*exec.Cmd, <-chan string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestQueueProcessHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "RIG_QUEUE_TEST_DIR="+dir, "RIG_QUEUE_TEST_MODE="+mode)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan string, 8)
	go func() {
		defer close(ready)
		scan := bufio.NewScanner(out)
		for scan.Scan() {
			ready <- scan.Text()
		}
	}()
	return cmd, ready
}
func expectLine(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("process said %q, want %q", got, want)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("process readiness timeout")
	}
}
func TestProcessDeathRecoversCommittedBatchWithoutLosingNewEvents(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	cmd, ready := subprocess(t, q.dir, "owner")
	expectLine(t, ready, "owned")
	if w, err := q.Worker(t.Context()); err == nil {
		w.Close()
		t.Fatal("live worker was replaced")
	}
	enqueue(t, q, request("b"))
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	reopened, err := Open(t.Context(), q.dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	w := worker(t, reopened)
	b := next(t, w)
	if b.ID != 1 || b.Through != 1 || b.Phase != Committed || b.CaptureRef != "durable-snapshot" || b.CommitRef != "local-commit" {
		t.Fatalf("bad crash replay: %+v", b)
	}
	finish(t, w, b)
	later := next(t, w)
	if later.ID != 2 {
		t.Fatalf("lost later event: %+v", later)
	}
}
func TestProcessExitAtPublicationBoundaries(t *testing.T) {
	for _, mode := range []string{"before", "after"} {
		t.Run(mode, func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("existing"))
			cmd, ready := subprocess(t, q.dir, mode)
			expectLine(t, ready, "interrupted")
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			q2, err := Open(t.Context(), q.dir, fixtureBinding)
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := q2.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			count := len(jobs[0].Events)
			want := 1
			if mode == "after" {
				want = 2
			}
			if count != want {
				t.Fatalf("published events=%d, want %d", count, want)
			}
			e := enqueue(t, q2, request("interrupted"))
			if e.Generation != 2 {
				t.Fatal("retry duplicated or lost generation")
			}
			leftovers, _ := filepath.Glob(filepath.Join(q.dir, ".queue-*"))
			if mode == "before" && len(leftovers) == 0 {
				t.Fatal("interrupted temp fixture missing")
			}
		})
	}
}
func TestIndependentProcessesDoNotLoseEnqueues(t *testing.T) {
	q := fixture(t)
	var cmds []*exec.Cmd
	var channels []<-chan string
	for i := range 3 {
		cmd, ch := subprocess(t, q.dir, fmt.Sprint(i))
		cmds = append(cmds, cmd)
		channels = append(channels, ch)
	}
	for i, ch := range channels {
		expectLine(t, ch, "enqueued")
		if err := cmds[i].Wait(); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || len(jobs[0].Events) != 12 || jobs[0].Through != 12 {
		t.Fatalf("cross-process enqueue loss: %+v %v", jobs, err)
	}
}
