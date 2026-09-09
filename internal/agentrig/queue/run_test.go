package queue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

type runnerOutcome struct {
	result RunResult
	err    error
}
type runningFixture struct {
	cancel  context.CancelFunc
	done    chan struct{}
	outcome runnerOutcome
}

func startRunner(t *testing.T, q *Queue, f Adapter, opts RunOptions) *runningFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	r := &runningFixture{cancel: cancel, done: make(chan struct{})}
	go func() { defer close(r.done); r.outcome.result, r.outcome.err = q.Run(ctx, f, opts) }()
	t.Cleanup(func() { cancel(); r.wait(t) })
	return r
}
func (r *runningFixture) wait(t *testing.T) runnerOutcome {
	t.Helper()
	select {
	case <-r.done:
		return r.outcome
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not stop")
		return runnerOutcome{}
	}
}
func awaitRunner(t *testing.T, q *Queue) {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		_, free, err := storelock.Acquire(t.Context(), filepath.Join(q.dir, "runner"), 0)
		if errors.Is(err, storelock.ErrBusy) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		free()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("runner never acquired ownership")
}

func TestRunnerDrainsSeparateBatchesAndKeepsExactReceipts(t *testing.T) {
	q := fixture(t)
	a := enqueue(t, q, request("a"))
	f := &executionFixture{}
	f.capture = func(_ context.Context, b Work) (string, error) {
		if b.ID == a.BatchID {
			enqueue(t, q, request("later"))
		}
		return "capture", nil
	}
	var observed []ExecutionResult
	result, err := q.Run(t.Context(), f, RunOptions{Drain: true, Observe: func(r ExecutionResult, err error) {
		if err != nil {
			t.Fatal(err)
		}
		w := worker(t, q)
		w.Close() // cleanup and execution ownership already released
		observed = append(observed, r)
	}})
	if err != nil || result.CompletedBatches != 2 || len(observed) != 2 || observed[0].BatchID == observed[1].BatchID {
		t.Fatalf("drain: %+v %v, %+v", result, err, observed)
	}
	if got := enqueue(t, q, request("a")); got.Generation != a.Generation {
		t.Fatal("lost producer receipt")
	}
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatal(jobs, err)
	}
}

func TestRunnerIdleReleasesWorkerAndPollsIndependentEnqueues(t *testing.T) {
	for _, hint := range []string{"none", "closed", "wake"} {
		t.Run(hint, func(t *testing.T) {
			q := fixture(t)
			stop := make(chan struct{})
			wake := make(chan struct{}, 1)
			var wakeInput <-chan struct{}
			if hint != "none" {
				wakeInput = wake
			}
			if hint == "closed" {
				close(wake)
			}
			interval := 20 * time.Millisecond
			if hint == "wake" {
				interval = time.Hour
			}
			f := &executionFixture{}
			r := startRunner(t, q, f, RunOptions{Stop: stop, Wake: wakeInput, PollInterval: interval, Observe: func(ExecutionResult, error) { close(stop) }})
			awaitRunner(t, q)
			before, err := os.ReadFile(filepath.Join(q.dir, "queue.json"))
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(60 * time.Millisecond)
			after, err := os.ReadFile(filepath.Join(q.dir, "queue.json"))
			if err != nil || string(before) != string(after) {
				t.Fatal("idle loop rewrote queue state", err)
			}
			if _, err := q.Run(t.Context(), &executionFixture{}, RunOptions{Drain: true}); !errors.Is(err, storelock.ErrBusy) || !strings.Contains(err.Error(), "queue runner") {
				t.Fatal("duplicate runner", err)
			}
			w := worker(t, q)
			w.Close()
			independent, err := Open(t.Context(), q.dir, fixtureBinding)
			if err != nil {
				t.Fatal(err)
			}
			enqueue(t, independent, request("external"))
			if hint == "wake" {
				wake <- struct{}{}
			}
			out := r.wait(t)
			if out.err != nil || out.result.CompletedBatches != 1 {
				t.Fatalf("run: %+v", out)
			}
		})
	}
}

func TestRunnerGracefulStopFinishesOnlyCurrentBatch(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	stop := make(chan struct{})
	f := &executionFixture{capture: func(ctx context.Context, _ Work) (string, error) {
		enqueue(t, q, request("later"))
		close(stop)
		if ctx.Err() != nil {
			t.Fatal("graceful stop canceled active capture")
		}
		return "capture", nil
	}}
	f.close = func() {
		if w, err := q.Worker(t.Context()); !errors.Is(err, storelock.ErrBusy) {
			if w != nil {
				w.Close()
			}
			t.Fatal("released worker before cleanup", err)
		}
	}
	out, err := q.Run(t.Context(), f, RunOptions{Stop: stop})
	if err != nil || out.CompletedBatches != 1 || !reflect.DeepEqual(f.calls, []string{"begin", "capture", "commit", "push", "close"}) {
		t.Fatal(out, err, f.calls)
	}
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 0 {
		t.Fatal(jobs, err)
	}
	if out, err = q.Run(t.Context(), &executionFixture{}, RunOptions{Drain: true}); err != nil || out.CompletedBatches != 1 {
		t.Fatal("restart", out, err)
	}
}

func TestRunnerCancellationKeepsSavedPhaseForRestart(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	entered := make(chan struct{})
	f := &executionFixture{push: func(ctx context.Context, _ Work) error { close(entered); <-ctx.Done(); return ctx.Err() }}
	r := startRunner(t, q, f, RunOptions{})
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("never reached push")
	}
	r.cancel()
	out := r.wait(t)
	if !errors.Is(out.err, context.Canceled) || out.result.CompletedBatches != 0 {
		t.Fatal(out)
	}
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].Phase != Committed || jobs[0].CommitRef != "commit-ref" {
		t.Fatal(jobs, err)
	}
	retry := &executionFixture{}
	result, err := q.Run(t.Context(), retry, RunOptions{Drain: true})
	if err != nil || result.CompletedBatches != 1 || !reflect.DeepEqual(retry.calls, []string{"begin", "push", "close"}) {
		t.Fatal(result, err, retry.calls)
	}
}

func TestRunnerDrainReportsDelayedAndBlockedWithoutOvertaking(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "delayed", true: "blocked"}[blocked], func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("a"))
			w := worker(t, q)
			b := next(t, w)
			deadline := time.Now().Add(time.Hour)
			if err := w.Retry(t.Context(), b.ID, deadline, "offline", blocked); err != nil {
				t.Fatal(err)
			}
			w.Close()
			enqueue(t, q, request("successor"))
			other := request("other")
			other.ProvenanceID = "other-account"
			enqueue(t, q, other)
			f := &executionFixture{}
			out, err := q.Run(t.Context(), f, RunOptions{Drain: true})
			var pending *DrainPending
			if !errors.Is(err, ErrUndrained) || !errors.As(err, &pending) || out.CompletedBatches != 1 || pending.Remaining != 2 {
				t.Fatal(out, err)
			}
			if blocked && (pending.Blocked != 1 || !pending.NextAttempt.IsZero()) {
				t.Fatal(pending)
			}
			if !blocked && (pending.Blocked != 0 || !pending.NextAttempt.Equal(deadline)) {
				t.Fatal(pending)
			}
			jobs, err := q.Snapshot(t.Context())
			if err != nil || jobs[0].Attempts != 1 || jobs[1].Attempts != 0 {
				t.Fatal(jobs, err)
			}
		})
	}
}

func TestRunnerWaitsUntilDurableRetryAndReplaysOnlyPush(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	stop := make(chan struct{})
	deadline := time.Time{}
	pushes := 0
	f := &executionFixture{push: func(_ context.Context, b Work) error {
		pushes++
		if pushes == 1 {
			deadline = time.Now().Add(200 * time.Millisecond)
			return &ExecutionFailure{Code: "offline", RetryAt: deadline, Cause: errors.New("offline")}
		}
		if time.Now().Before(deadline) || b.Attempts != 2 {
			t.Error("retry before deadline or wrong attempts")
		}
		return nil
	}}
	var recorded bool
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out, err := q.Run(ctx, f, RunOptions{Stop: stop, PollInterval: time.Hour, Observe: func(r ExecutionResult, err error) {
		if err != nil {
			recorded = r.FailureRecorded
		} else {
			close(stop)
		}
	}})
	if err != nil || out.CompletedBatches != 1 || !recorded || !reflect.DeepEqual(f.calls, []string{"begin", "capture", "commit", "push", "close", "begin", "push", "close"}) {
		t.Fatal(out, err, f.calls)
	}
}

func TestRunnerStopsOnUnknownOrUncertainFailure(t *testing.T) {
	for _, mode := range []string{"unknown", "marker", "retry-marker", "past-retry"} {
		t.Run(mode, func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("a"))
			cause := errors.New("unclassified failure")
			f := &executionFixture{capture: func(context.Context, Work) (string, error) {
				switch mode {
				case "unknown":
					return "", cause
				case "past-retry":
					return "", &ExecutionFailure{Code: "offline", RetryAt: fixtureTime, Cause: cause}
				default:
					q.save = func(dir string, data []byte) error {
						if err := saveFile(dir, data); err != nil {
							return err
						}
						return ErrUncertain
					}
					if mode == "retry-marker" {
						return "", &ExecutionFailure{Code: "offline", RetryAt: time.Now().Add(time.Hour), Cause: cause}
					}
					return "retained-capture", nil
				}
			}}
			var progress ExecutionResult
			out, err := q.Run(t.Context(), f, RunOptions{Drain: true, Observe: func(r ExecutionResult, _ error) { progress = r }})
			if err == nil || out.CompletedBatches != 0 {
				t.Fatal(out, err)
			}
			if mode != "past-retry" && progress.FailureRecorded {
				t.Fatal("uncertain/unknown error marked durable", progress)
			}
			if mode == "marker" || mode == "retry-marker" {
				if !errors.Is(err, ErrUncertain) {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(f.calls, []string{"begin", "capture", "close"}) {
				t.Fatal("continued after failure", f.calls)
			}
			q.save = saveFile
			w := worker(t, q)
			w.Close()
		})
	}
}

func TestRunnerRecordedBlockAllowsIndependentProvenance(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("blocked"))
	other := request("other")
	other.ProvenanceID = "other-account"
	enqueue(t, q, other)
	f := &executionFixture{begin: func(_ context.Context, _ Binding, b Work) error {
		if b.ID == 1 {
			return &ExecutionFailure{Code: "repair-required", RetryAt: time.Now(), Blocked: true}
		}
		return nil
	}}
	var recorded bool
	result, err := q.Run(t.Context(), f, RunOptions{Drain: true, Observe: func(r ExecutionResult, err error) {
		if r.BatchID == 1 {
			recorded = r.FailureRecorded && err != nil
		}
	}})
	var pending *DrainPending
	if !errors.As(err, &pending) || pending.Remaining != 1 || pending.Blocked != 1 || result.CompletedBatches != 1 || !recorded {
		t.Fatal(result, err, recorded)
	}
}

func TestRunnerStopBeforeClaimAndBusyDrainLeaveAttemptsUntouched(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	stop := make(chan struct{})
	close(stop)
	f := &executionFixture{}
	if _, err := q.Run(t.Context(), f, RunOptions{Stop: stop}); err != nil {
		t.Fatal(err)
	}
	w := worker(t, q)
	if _, err := q.Run(t.Context(), f, RunOptions{Drain: true}); !errors.Is(err, storelock.ErrBusy) {
		t.Fatal("drain replaced foreground owner", err)
	}
	w.Close()
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].Attempts != 0 || len(f.calls) != 0 {
		t.Fatal(jobs, err, f.calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := q.Run(ctx, f, RunOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
