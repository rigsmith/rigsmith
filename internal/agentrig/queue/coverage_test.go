package queue

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func prepareCoverage(t *testing.T, w *Worker) *Coverage {
	t.Helper()
	c, err := w.PrepareCoverage(t.Context(), fixtureBinding, "source-account")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCoverageExactEventsPreserveOtherProvenanceAndNewArrivals(t *testing.T) {
	q := fixture(t)
	a := enqueue(t, q, request("a", "one"))
	otherRequest := request("other")
	otherRequest.ProvenanceID = "other-account"
	other := enqueue(t, q, otherRequest)
	allRequest := request("all")
	allRequest.Flush = Flush{Mode: All}
	all := enqueue(t, q, allRequest)
	w := worker(t, q)
	c := prepareCoverage(t, w)
	candidates := c.Batches()
	if len(candidates) != 1 || candidates[0].Through != 3 || candidates[0].Flush.Mode != All || candidates[0].Attempts != 0 {
		t.Fatalf("wrong candidates: %+v", candidates)
	}
	// Detached snapshots cannot change membership or native intent in the ticket.
	candidates[0].Events[0].Generation = 999
	candidates[0].Events[0].Request.Flush.Paths[0] = "changed"
	if c.Batches()[0].Events[0].Request.Flush.Paths[0] != "one" {
		t.Fatal("aliased request")
	}
	late := enqueue(t, q, request("late", "one"))
	if late.BatchID == a.BatchID {
		t.Fatal("later arrival entered covered batch")
	}
	for _, generation := range []uint64{other.Generation, late.Generation, 999, 0} {
		if got, err := c.Acknowledge(t.Context(), []uint64{a.Generation, all.Generation, generation}); !errors.Is(err, ErrTransition) || len(got) != 0 {
			t.Fatalf("accepted foreign generation %d: %v %v", generation, got, err)
		}
	}
	if got, err := c.Acknowledge(t.Context(), []uint64{a.Generation}); err != nil || len(got) != 0 {
		t.Fatalf("partially covered batch acknowledged: %v %v", got, err)
	}
	for range 2 {
		got, err := c.Acknowledge(t.Context(), []uint64{all.Generation, a.Generation, a.Generation})
		if err != nil || !reflect.DeepEqual(got, []uint64{a.Generation, all.Generation}) {
			t.Fatalf("acknowledge: %v %v", got, err)
		}
	}
	reopened, err := Open(t.Context(), q.dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	left, err := reopened.Snapshot(t.Context())
	if err != nil || len(left) != 2 || left[0].ID != other.BatchID || left[1].ID != late.BatchID {
		t.Fatalf("lost unrelated work: %+v %v", left, err)
	}
	if duplicate := enqueue(t, reopened, request("a", "one")); !reflect.DeepEqual(duplicate, a) {
		t.Fatal("lost completed event receipt")
	}
	if _, err := reopened.Enqueue(t.Context(), request("a", "changed"), fixtureTime); !errors.Is(err, ErrDuplicate) {
		t.Fatal("changed completed request accepted", err)
	}
}

func TestCoverageCannotBypassRecovery(t *testing.T) {
	for _, phase := range []Phase{Queued, Captured, Committed, Pushed} {
		for _, blocked := range []bool{false, true} {
			if phase == Pushed && blocked {
				continue
			}
			t.Run(string(phase)+map[bool]string{true: "-blocked", false: "-pending"}[blocked], func(t *testing.T) {
				q := fixture(t)
				enqueue(t, q, request("old"))
				w := worker(t, q)
				b := next(t, w)
				for _, p := range []Phase{Captured, Committed, Pushed} {
					if phase == Queued {
						break
					}
					ref := string(p)
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
				if phase != Pushed {
					if err := w.Retry(t.Context(), b.ID, fixtureTime, "recovery-required", blocked); err != nil {
						t.Fatal(err)
					}
				}
				w.Close()
				w = worker(t, q)
				enqueue(t, q, request("later"))
				before, err := q.Snapshot(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if c, err := w.PrepareCoverage(t.Context(), fixtureBinding, "source-account"); !errors.Is(err, ErrEmpty) || c != nil {
					t.Fatal("bypassed recovery", c, err)
				}
				after, err := q.Snapshot(t.Context())
				if err != nil || !reflect.DeepEqual(after, before) {
					t.Fatal("changed recovery state", err)
				}
				other := request("other")
				other.ProvenanceID = "other-account"
				event := enqueue(t, q, other)
				c, err := w.PrepareCoverage(t.Context(), fixtureBinding, other.ProvenanceID)
				if err != nil || len(c.Batches()) != 1 || c.Batches()[0].ID != event.BatchID {
					t.Fatal("blocked unrelated provenance", err)
				}
			})
		}
	}
}

func TestCoverageOwnershipAndChangedCandidates(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	w := worker(t, q)
	wrong := fixtureBinding
	wrong.RemoteID = "different-remote"
	for _, binding := range []Binding{wrong, {}} {
		if c, err := w.PrepareCoverage(t.Context(), binding, "source-account"); !errors.Is(err, ErrBinding) || c != nil {
			t.Fatal(c, err)
		}
	}
	if c, err := w.PrepareCoverage(t.Context(), fixtureBinding, ""); !errors.Is(err, ErrBinding) || c != nil {
		t.Fatal(c, err)
	}
	c := prepareCoverage(t, w)
	b := next(t, w)
	if b.Attempts != 1 {
		t.Fatal("manual preparation consumed retry budget", b.Attempts)
	}
	if _, err := w.PrepareCoverage(t.Context(), fixtureBinding, "source-account"); !errors.Is(err, ErrTransition) {
		t.Fatal("allowed concurrent execution", err)
	}
	if got, err := c.Acknowledge(t.Context(), []uint64{1}); !errors.Is(err, ErrTransition) || len(got) != 0 {
		t.Fatal("acknowledged changed claim", got, err)
	}
	w.Close()
	replacement := worker(t, q)
	if got, err := c.Acknowledge(t.Context(), []uint64{1}); !errors.Is(err, ErrOwner) || len(got) != 0 {
		t.Fatal("old owner acknowledged", got, err)
	}
	finish(t, replacement, next(t, replacement))
	if _, err := c.Acknowledge(t.Context(), []uint64{1}); !errors.Is(err, ErrOwner) {
		t.Fatal("old owner used completed receipt", err)
	}
	var zero Coverage
	if _, err := zero.Acknowledge(t.Context(), []uint64{1}); !errors.Is(err, ErrOwner) {
		t.Fatal(err)
	}
}

func TestCoverageFailedAndUncertainWrites(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{true: "after-replacement", false: "before-replacement"}[after], func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("a"))
			w := worker(t, q)
			saves := 0
			save := func(dir string, data []byte) error {
				saves++
				if after {
					if err := saveFile(dir, data); err != nil {
						return err
					}
					return ErrUncertain
				}
				return os.ErrPermission
			}
			expected := error(os.ErrPermission)
			if after {
				expected = ErrUncertain
			}
			q.save = save
			for range 2 {
				if c, err := w.PrepareCoverage(t.Context(), fixtureBinding, "source-account"); !errors.Is(err, expected) || c != nil {
					t.Fatal("unconfirmed ticket escaped", c, err)
				}
			}
			if saves != 2 {
				t.Fatal("preparation retry did not reflush")
			}
			q.save = saveFile
			c := prepareCoverage(t, w)
			late := enqueue(t, q, request("late"))
			saves = 0
			q.save = save
			for range 2 {
				if got, err := c.Acknowledge(t.Context(), []uint64{1}); !errors.Is(err, expected) || len(got) != 0 {
					t.Fatal("unconfirmed acknowledgement escaped", got, err)
				}
			}
			if saves != 2 {
				t.Fatal("acknowledgement retry did not reflush")
			}
			q.save = saveFile
			got, err := c.Acknowledge(t.Context(), []uint64{1})
			if err != nil || !reflect.DeepEqual(got, []uint64{1}) {
				t.Fatal(got, err)
			}
			left, err := q.Snapshot(t.Context())
			if err != nil || len(left) != 1 || left[0].ID != late.BatchID {
				t.Fatalf("lost later event: %+v %v", left, err)
			}
		})
	}
}

func TestCoverageCancellationLeavesPendingWork(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	w := worker(t, q)
	c := prepareCoverage(t, w)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := c.Acknowledge(ctx, []uint64{1}); !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatal(got, err)
	}
	w.Close()
	w = worker(t, q)
	if b := next(t, w); b.ID != 1 || b.Phase != Queued || b.Attempts != 1 {
		t.Fatalf("abandoned capture lost work: %+v", b)
	}
}

func TestCoverageMultipleBatchesRetainPartialCoverage(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	w := worker(t, q)
	prepareCoverage(t, w)
	enqueue(t, q, request("b"))
	enqueue(t, q, request("c"))
	c := prepareCoverage(t, w)
	if len(c.Batches()) != 2 {
		t.Fatal("missing sealed batches")
	}
	got, err := c.Acknowledge(t.Context(), []uint64{1, 2})
	if err != nil || !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatal(got, err)
	}
	left, err := q.Snapshot(t.Context())
	if err != nil || len(left) != 1 || left[0].ID != 2 || len(left[0].Events) != 2 {
		t.Fatal("partial batch lost events", left, err)
	}
	got, err = c.Acknowledge(t.Context(), []uint64{1, 2, 3})
	if err != nil || !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatal(got, err)
	}
}

func TestCoverageProcessDeathPreservesSealedPendingBatch(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	cmd, ready := subprocess(t, q.dir, "coverage")
	expectLine(t, ready, "coverage-owned")
	if w, err := q.Worker(t.Context()); err == nil {
		w.Close()
		t.Fatal("stole coverage ownership")
	}
	late := enqueue(t, q, request("late"))
	if late.BatchID != 2 {
		t.Fatal("producer coalesced into in-flight manual capture")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	reopened, err := Open(t.Context(), q.dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	w := worker(t, reopened)
	first := next(t, w)
	if first.ID != 1 || first.Through != 1 || first.Phase != Queued || first.Attempts != 1 {
		t.Fatalf("bad recovery: %+v", first)
	}
	finish(t, w, first)
	if second := next(t, w); second.ID != late.BatchID {
		t.Fatal("lost later work", second)
	}
}

func TestCoverageProcessExitAtAcknowledgement(t *testing.T) {
	for _, mode := range []string{"coverage-ack-before", "coverage-ack-after"} {
		t.Run(mode, func(t *testing.T) {
			q := fixture(t)
			original := enqueue(t, q, request("a"))
			cmd, ready := subprocess(t, q.dir, mode)
			expectLine(t, ready, "coverage-interrupted")
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			q, err := Open(t.Context(), q.dir, fixtureBinding)
			if err != nil {
				t.Fatal(err)
			}
			late := enqueue(t, q, request("late"))
			w := worker(t, q)
			first := next(t, w)
			if mode == "coverage-ack-before" {
				if first.ID != original.BatchID {
					t.Fatal("unmarked publication discarded", first)
				}
				finish(t, w, first)
				first = next(t, w)
			}
			if first.ID != late.BatchID {
				t.Fatal("lost later event or repeated marked publication", first)
			}
			if got := enqueue(t, q, request("a")); !reflect.DeepEqual(got, original) {
				t.Fatal("lost producer receipt", got)
			}
		})
	}
}

func TestCoverageUpgradesLegacyQueueWithoutChangingWork(t *testing.T) {
	q := fixture(t)
	done := enqueue(t, q, request("done"))
	w := worker(t, q)
	finish(t, w, next(t, w))
	old := request("retained")
	old.ProvenanceID = "other-account"
	enqueue(t, q, old)
	b := next(t, w)
	if err := w.Progress(t.Context(), b.ID, Captured, "saved-capture"); err != nil {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Committed, "saved-commit"); err != nil {
		t.Fatal(err)
	}
	if err := w.Retry(t.Context(), b.ID, fixtureTime, "offline", false); err != nil {
		t.Fatal(err)
	}
	w.Close()
	enqueue(t, q, request("eligible"))
	// Pre-coverage payloads have the same fields as schema 1. Recreate a legacy
	// envelope containing completed receipts, retry metadata and retained refs.
	path := filepath.Join(q.dir, "queue.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	env.Version = 1
	data, err = json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	q, err = Open(t.Context(), q.dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(t.Context(), q.dir, fixtureBinding); err != nil {
		t.Fatal(err)
	}
	enqueue(t, q, request("eligible-second"))
	w = worker(t, q)
	s, err := q.load()
	if err != nil || s.version != 1 {
		t.Fatal("ordinary operations upgraded legacy state", err)
	}
	before, err := q.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	q.save = func(string, []byte) error { return os.ErrPermission }
	if c, err := w.PrepareCoverage(t.Context(), fixtureBinding, "source-account"); !errors.Is(err, os.ErrPermission) || c != nil {
		t.Fatal(c, err)
	}
	s, err = q.load()
	if err != nil || s.version != 1 {
		t.Fatal("failed upgrade changed legacy state", err)
	}
	q.save = saveFile
	c := prepareCoverage(t, w)
	s, err = q.load()
	if err != nil || s.version != 2 {
		t.Fatal("coverage did not upgrade schema", err)
	}
	after, err := q.Snapshot(t.Context())
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("upgrade changed work", after, err)
	}
	if got := enqueue(t, q, request("done")); !reflect.DeepEqual(got, done) {
		t.Fatal("upgrade lost receipt", got)
	}
	if got, err := c.Acknowledge(t.Context(), []uint64{3, 4}); err != nil || !reflect.DeepEqual(got, []uint64{3, 4}) {
		t.Fatal(got, err)
	}
	w.Close()
	q, err = Open(t.Context(), q.dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	remaining := next(t, worker(t, q))
	if remaining.ID != b.ID || remaining.Phase != Committed || remaining.CommitRef != "saved-commit" || remaining.CaptureRef != "saved-capture" || remaining.Attempts != 2 {
		t.Fatalf("upgrade lost recovery state: %+v", remaining)
	}
}

func TestCoverageRejectsSealInLegacyEnvelope(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	prepareCoverage(t, worker(t, q))
	path := filepath.Join(q.dir, "queue.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	env.Version = 1
	data, err = json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), q.dir, fixtureBinding); err == nil {
		t.Fatal("accepted schema-2 seal in schema-1 state")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("rewrote invalid state", err)
	}
}
