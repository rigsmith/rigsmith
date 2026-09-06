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

type executionFixture struct {
	begin   func(context.Context, Binding, Work) error
	capture func(context.Context, Work) (string, error)
	commit  func(context.Context, Work) (string, error)
	push    func(context.Context, Work) error
	close   func()
	calls   []string
}

func (f *executionFixture) Begin(ctx context.Context, binding Binding, b Work) (Execution, error) {
	f.calls = append(f.calls, "begin")
	if f.begin != nil {
		if err := f.begin(ctx, binding, b); err != nil {
			return nil, err
		}
	}
	return f, nil
}
func (f *executionFixture) Capture(ctx context.Context, b Work) (string, error) {
	f.calls = append(f.calls, "capture")
	if f.capture != nil {
		return f.capture(ctx, b)
	}
	return "capture-ref", nil
}
func (f *executionFixture) Commit(ctx context.Context, b Work) (string, error) {
	f.calls = append(f.calls, "commit")
	if f.commit != nil {
		return f.commit(ctx, b)
	}
	return "commit-ref", nil
}
func (f *executionFixture) Push(ctx context.Context, b Work) error {
	f.calls = append(f.calls, "push")
	if f.push != nil {
		return f.push(ctx, b)
	}
	return nil
}
func (f *executionFixture) Close() {
	f.calls = append(f.calls, "close")
	if f.close != nil {
		f.close()
	}
}

func TestRunOneResumesOnlyUnfinishedPhases(t *testing.T) {
	for _, phase := range []Phase{Queued, Captured, Committed, Pushed} {
		t.Run(string(phase), func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("a", "source"))
			if phase != Queued {
				w := worker(t, q)
				b := next(t, w)
				for _, p := range []Phase{Captured, Committed, Pushed} {
					ref := string(p) + "-ref"
					if p == Pushed {
						ref = ""
					}
					if err := w.Progress(t.Context(), b.ID, p, ref); err != nil {
						t.Fatal(err)
					}
					if p == phase {
						break
					}
				}
				w.Close()
			}
			f := &executionFixture{}
			f.begin = func(ctx context.Context, binding Binding, b Work) error {
				if binding != fixtureBinding || b.Phase != phase || b.Events[0].Request.EventID != "a" {
					t.Fatalf("wrong binding/work: %+v %+v", binding, b)
				}
				b.Events[0].Request.Flush.Paths[0] = "changed-by-adapter"
				return nil
			}
			f.commit = func(ctx context.Context, b Work) (string, error) {
				if b.CaptureRef == "" || b.Events[0].Request.Flush.Paths[0] != "source" {
					t.Fatalf("lost reference or aliased input: %+v", b)
				}
				return "commit-ref", nil
			}
			f.push = func(ctx context.Context, b Work) error {
				if b.CommitRef == "" {
					t.Fatal("push without reference")
				}
				return nil
			}
			got, err := q.RunOne(t.Context(), fixtureTime, f)
			if err != nil || !got.Acknowledged || got.Phase != Pushed || got.BatchID != 1 {
				t.Fatalf("result %+v %v", got, err)
			}
			var want []string
			switch phase {
			case Queued:
				want = []string{"begin", "capture", "commit", "push", "close"}
			case Captured:
				want = []string{"begin", "commit", "push", "close"}
			case Committed:
				want = []string{"begin", "push", "close"}
			}
			if !reflect.DeepEqual(f.calls, want) {
				t.Fatalf("calls %v want %v", f.calls, want)
			}
			if _, err := q.RunOne(t.Context(), fixtureTime, f); !errors.Is(err, ErrEmpty) {
				t.Fatal(err)
			}
		})
	}
}

func TestRunOneOfflineRetryKeepsCommitAndLaterEvents(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	f := &executionFixture{}
	f.capture = func(ctx context.Context, b Work) (string, error) { enqueue(t, q, request("b")); return "sealed-a", nil }
	cause := errors.New("sensitive diagnostic must not persist")
	f.push = func(context.Context, Work) error {
		return &ExecutionFailure{Code: "offline", RetryAt: fixtureTime.Add(time.Hour), Cause: cause}
	}
	result, err := q.RunOne(t.Context(), fixtureTime, f)
	if !errors.Is(err, cause) || result.Phase != Committed || result.Acknowledged {
		t.Fatalf("offline: %+v %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(q.dir, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), cause.Error()) {
		t.Fatal("raw adapter error persisted")
	}
	if _, err := q.RunOne(t.Context(), fixtureTime, f); !errors.Is(err, ErrEmpty) {
		t.Fatal("backoff overtaken", err)
	}
	f.calls = nil
	f.push = func(_ context.Context, b Work) error {
		if b.CaptureRef != "sealed-a" || b.CommitRef != "commit-ref" || b.Through != 1 {
			t.Fatalf("changed sealed batch: %+v", b)
		}
		return nil
	}
	if _, err := q.RunOne(t.Context(), fixtureTime.Add(time.Hour), f); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, []string{"begin", "push", "close"}) {
		t.Fatal("offline retry recaptured", f.calls)
	}
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].Events[0].Request.EventID != "b" {
		t.Fatalf("new event acknowledged: %+v %v", jobs, err)
	}
}

func TestRunOneStopsAtUncertainPhaseMarker(t *testing.T) {
	for _, phase := range []Phase{Captured, Committed, Pushed} {
		t.Run(string(phase), func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("a"))
			arm := func() {
				q.save = func(dir string, data []byte) error {
					if err := saveFile(dir, data); err != nil {
						return err
					}
					return ErrUncertain
				}
			}
			f := &executionFixture{}
			switch phase {
			case Captured:
				f.capture = func(context.Context, Work) (string, error) { arm(); return "capture-ref", nil }
			case Committed:
				f.commit = func(context.Context, Work) (string, error) { arm(); return "commit-ref", nil }
			case Pushed:
				f.push = func(context.Context, Work) error { arm(); return nil }
			}
			result, err := q.RunOne(t.Context(), fixtureTime, f)
			if !errors.Is(err, ErrUncertain) || result.Acknowledged {
				t.Fatalf("uncertain: %+v %v", result, err)
			}
			want := map[Phase][]string{Captured: {"begin", "capture", "close"}, Committed: {"begin", "capture", "commit", "close"}, Pushed: {"begin", "capture", "commit", "push", "close"}}[phase]
			if !reflect.DeepEqual(f.calls, want) {
				t.Fatal("continued past uncertain marker", f.calls)
			}
			q.save = saveFile
			retry := &executionFixture{}
			if result, err = q.RunOne(t.Context(), fixtureTime, retry); err != nil || !result.Acknowledged {
				t.Fatalf("retry %+v %v", result, err)
			}
			want = map[Phase][]string{Captured: {"begin", "commit", "push", "close"}, Committed: {"begin", "push", "close"}, Pushed: nil}[phase]
			if !reflect.DeepEqual(retry.calls, want) {
				t.Fatal("replayed confirmed effect", retry.calls)
			}
		})
	}
}

func TestRunOneBindingFailureBlocksBeforeEffects(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	f := &executionFixture{begin: func(context.Context, Binding, Work) error {
		return &ExecutionFailure{Code: "configuration-changed", RetryAt: fixtureTime, Blocked: true, Cause: ErrBinding}
	}}
	if _, err := q.RunOne(t.Context(), fixtureTime, f); !errors.Is(err, ErrBinding) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, []string{"begin"}) {
		t.Fatal(f.calls)
	}
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].Status != Blocked || jobs[0].Phase != Queued {
		t.Fatalf("not blocked: %+v %v", jobs, err)
	}
	enqueue(t, q, request("b"))
	if _, err := q.RunOne(t.Context(), fixtureTime, f); !errors.Is(err, ErrEmpty) {
		t.Fatal("new input cleared block", err)
	}
}

func TestRunOneHoldsWorkerAcrossStagingAndAllowsProducers(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	stage := filepath.Join(t.TempDir(), "stage")
	var release func()
	f := &executionFixture{}
	f.begin = func(ctx context.Context, _ Binding, _ Work) error {
		var err error
		_, release, err = storelock.Acquire(ctx, stage, 0)
		return err
	}
	f.capture = func(ctx context.Context, b Work) (string, error) {
		enqueue(t, q, request("b"))
		if _, err := q.RunOne(ctx, fixtureTime, &executionFixture{}); !errors.Is(err, storelock.ErrBusy) {
			t.Fatal("second executor entered", err)
		}
		if _, free, err := storelock.Acquire(ctx, stage, 0); !errors.Is(err, storelock.ErrBusy) {
			if free != nil {
				free()
			}
			t.Fatal("staging lease missing", err)
		}
		return "capture-ref", nil
	}
	f.close = func() {
		if w, err := q.Worker(t.Context()); !errors.Is(err, storelock.ErrBusy) {
			if w != nil {
				w.Close()
			}
			t.Fatal("worker released before staging cleanup", err)
		}
		release()
	}
	if _, err := q.RunOne(t.Context(), fixtureTime, f); err != nil {
		t.Fatal(err)
	}
	_, free, err := storelock.Acquire(t.Context(), stage, 0)
	if err != nil {
		t.Fatal("staging leaked", err)
	}
	free()
	w := worker(t, q)
	w.Close()
}

func TestRunOneCancellationNeverStartsNextEffect(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := &executionFixture{capture: func(context.Context, Work) (string, error) { cancel(); return "capture-ref", nil }}
	if _, err := q.RunOne(ctx, fixtureTime, f); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, []string{"begin", "capture", "close"}) {
		t.Fatal(f.calls)
	}
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].Phase != Queued {
		t.Fatalf("unconfirmed capture lost: %+v %v", jobs, err)
	}
	// The adapter must recognize/reuse the sealed capture by batch ID on replay.
	if _, err := q.RunOne(t.Context(), fixtureTime, &executionFixture{}); err != nil {
		t.Fatal(err)
	}
}

func TestRunOneFailedMarkerDoesNotDiscardExternalEffect(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	failure := errors.New("disk unavailable before replacement")
	calls := 0
	f := &executionFixture{capture: func(context.Context, Work) (string, error) {
		calls++
		q.save = func(string, []byte) error { return failure }
		return "artifact-by-batch-id", nil
	}}
	if _, err := q.RunOne(t.Context(), fixtureTime, f); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls, []string{"begin", "capture", "close"}) {
		t.Fatal(f.calls)
	}
	q.save = saveFile
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || jobs[0].Phase != Queued {
		t.Fatalf("failed marker: %+v %v", jobs, err)
	}
	f.capture = func(_ context.Context, b Work) (string, error) {
		calls++
		if b.ID != 1 {
			t.Fatal("lost external idempotency key")
		}
		return "artifact-by-batch-id", nil
	}
	if _, err := q.RunOne(t.Context(), fixtureTime, f); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("unconfirmed effect must be checked/reused by adapter")
	}
}
