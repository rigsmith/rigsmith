package queue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var fixtureBinding = Binding{Vendor: "fixture", StoreID: "canonical-store", RootID: "roots-v1", RemoteID: "remote-v1", ConfigID: "config-digest"}
var fixtureTime = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T) *Queue {
	t.Helper()
	q, err := Create(t.Context(), filepath.Join(t.TempDir(), "queue"), fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
func request(id string, paths ...string) Request {
	mode := Normal
	if len(paths) > 0 {
		mode = Selected
	}
	return Request{EventID: id, SessionID: "session-" + id, ProvenanceID: "source-account", Flush: Flush{Mode: mode, Paths: paths}}
}
func enqueue(t *testing.T, q *Queue, r Request) Event {
	t.Helper()
	e, err := q.Enqueue(t.Context(), r, fixtureTime)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func worker(t *testing.T, q *Queue) *Worker {
	t.Helper()
	w, err := q.Worker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	return w
}
func next(t *testing.T, w *Worker) Work {
	t.Helper()
	b, err := w.Next(t.Context(), fixtureTime.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func finish(t *testing.T, w *Worker, b Work) {
	t.Helper()
	for _, p := range []Phase{Captured, Committed, Pushed} {
		if b.Phase == Pushed {
			break
		}
		if b.Phase == Committed && (p == Captured || p == Committed) {
			continue
		}
		if b.Phase == Captured && p == Captured {
			continue
		}
		ref := string(p) + "-artifact"
		if p == Pushed {
			ref = ""
		}
		if err := w.Progress(t.Context(), b.ID, p, ref); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Acknowledge(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationsCoalesceAndPreserveArrivalsDuringCapture(t *testing.T) {
	q := fixture(t)
	a := enqueue(t, q, request("a", "two", "one"))
	enqueue(t, q, request("b", "two", "three"))
	duplicate, err := q.Enqueue(t.Context(), request("a", "one", "two", "one"), fixtureTime.Add(time.Hour))
	if err != nil || duplicate.Generation != a.Generation {
		t.Fatalf("dedupe: %+v %v", duplicate, err)
	}
	w := worker(t, q)
	first := next(t, w)
	if first.Through != 2 || len(first.Events) != 2 || !reflect.DeepEqual(first.Flush.Paths, []string{"one", "three", "two"}) {
		t.Fatalf("coalesced: %+v", first)
	}
	enqueue(t, q, request("c", "later"))
	finish(t, w, first)
	second := next(t, w)
	if second.ID != 3 || second.Through != 3 || len(second.Events) != 1 {
		t.Fatalf("later event acknowledged by earlier batch: %+v", second)
	}
	finish(t, w, second)
	if _, err := w.Next(t.Context(), fixtureTime); !errors.Is(err, ErrEmpty) {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(t.Context(), request("a", "different"), fixtureTime); !errors.Is(err, ErrDuplicate) {
		t.Fatal("conflicting ID accepted", err)
	}
	q2, err := Open(t.Context(), q.dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	e := enqueue(t, q2, request("a", "one", "two"))
	if e.Generation != 1 {
		t.Fatal("completed event was re-enqueued")
	}
	if jobs, err := q2.Snapshot(t.Context()); err != nil || len(jobs) != 0 {
		t.Fatalf("completed replay: %+v %v", jobs, err)
	}
}
func TestAllFlushProvenanceAndImmutableSnapshot(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a", "one"))
	r := request("b")
	r.Flush.Mode = All
	enqueue(t, q, r)
	other := request("c", "private")
	other.ProvenanceID = "other-account"
	enqueue(t, q, other)
	snapshots, err := q.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 || snapshots[0].Flush.Mode != All || len(snapshots[0].Flush.Paths) != 0 {
		t.Fatalf("grouping: %+v", snapshots)
	}
	snapshots[0].Events[0].Request.Flush.Paths[0] = "mutated"
	again, _ := q.Snapshot(t.Context())
	if again[0].Events[0].Request.Flush.Paths[0] != "one" {
		t.Fatal("snapshot aliases stored state")
	}
	w := worker(t, q)
	finish(t, w, next(t, w))
	if b := next(t, w); len(b.Events) != 1 || b.Events[0].Request.ProvenanceID != "other-account" {
		t.Fatal("provenance mixed")
	}
}
func TestRetryRecoveryRetainsArtifactAndBlocksLaterInput(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a", "original"))
	w := worker(t, q)
	b := next(t, w)
	if err := w.Progress(t.Context(), b.ID, Captured, "snapshot-a"); err != nil {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Committed, "commit-a"); err != nil {
		t.Fatal(err)
	}
	due := fixtureTime.Add(time.Minute)
	if err := w.Retry(t.Context(), b.ID, due, "offline", false); err != nil {
		t.Fatal(err)
	}
	enqueue(t, q, request("b", "new"))
	if _, err := w.Next(t.Context(), fixtureTime); !errors.Is(err, ErrEmpty) {
		t.Fatalf("backoff bypassed: %v", err)
	}
	w.Close()
	w2 := worker(t, q)
	retry := next(t, w2)
	if retry.Phase != Committed || retry.CaptureRef != "snapshot-a" || retry.CommitRef != "commit-a" || len(retry.Events) != 1 || retry.Attempts != 2 {
		t.Fatalf("retry recaptured/coalesced: %+v", retry)
	}
	if err := w.Progress(t.Context(), b.ID, Pushed, ""); !errors.Is(err, ErrOwner) {
		t.Fatalf("old worker accepted: %v", err)
	}
	finish(t, w2, retry)
	if b := next(t, w2); b.ID != 2 {
		t.Fatalf("lost later input: %+v", b)
	}
}
func TestBlockedJobsNeedExplicitUnblockAndQueuedClaimsNeverGrow(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	w := worker(t, q)
	b := next(t, w)
	if err := w.Retry(t.Context(), b.ID, fixtureTime, "source-missing", true); err != nil {
		t.Fatal(err)
	}
	enqueue(t, q, request("b"))
	if _, err := w.Next(t.Context(), fixtureTime); !errors.Is(err, ErrEmpty) {
		t.Fatal("new event cleared block", err)
	}
	if err := w.Unblock(t.Context(), b.ID); err != nil {
		t.Fatal(err)
	}
	enqueue(t, q, request("c"))
	recovered := next(t, w)
	if recovered.Through != 1 || len(recovered.Events) != 1 {
		t.Fatalf("previously claimed batch grew: %+v", recovered)
	}
	finish(t, w, recovered)
	later := next(t, w)
	if len(later.Events) != 2 || later.Through != 3 {
		t.Fatal("new pending events did not coalesce")
	}
}
func TestProgressCannotAcknowledgeUnpublishedOrWrongArtifacts(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	w := worker(t, q)
	b := next(t, w)
	if err := w.Acknowledge(t.Context(), b.ID); !errors.Is(err, ErrTransition) {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Committed, "commit"); !errors.Is(err, ErrTransition) {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Captured, ""); !errors.Is(err, ErrTransition) {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Captured, "capture"); err != nil {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Captured, "other"); !errors.Is(err, ErrTransition) {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Captured, "capture"); err != nil {
		t.Fatal(err)
	}
	b.Phase = Captured
	finish(t, w, b)
	if err := w.Acknowledge(t.Context(), b.ID); err != nil {
		t.Fatal("ack retry is not idempotent", err)
	}
}
func TestConcurrentProducersAndExclusiveWorker(t *testing.T) {
	q := fixture(t)
	w := worker(t, q)
	if w2, err := q.Worker(t.Context()); err == nil {
		w2.Close()
		t.Fatal("second worker accepted")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := range 24 {
		wg.Go(func() { _, err := q.Enqueue(t.Context(), request(fmt.Sprint(i)), fixtureTime); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	b := next(t, w)
	if len(b.Events) != 24 || b.Through != 24 {
		t.Fatalf("lost enqueue: %+v", b)
	}
}
func TestFailedAndUncertainWritesAreSafeToRetry(t *testing.T) {
	q := fixture(t)
	before, err := os.ReadFile(filepath.Join(q.dir, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	q.save = func(string, []byte) error { return fmt.Errorf("disk-full") }
	if _, err = q.Enqueue(t.Context(), request("a"), fixtureTime); err == nil {
		t.Fatal("failed write accepted")
	}
	after, _ := os.ReadFile(filepath.Join(q.dir, "queue.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed write changed queue")
	}
	q.save = func(dir string, b []byte) error {
		if err := saveFile(dir, b); err != nil {
			return err
		}
		return ErrUncertain
	}
	if _, err = q.Enqueue(t.Context(), request("a"), fixtureTime); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	q.save = saveFile
	e := enqueue(t, q, request("a"))
	if e.Generation != 1 {
		t.Fatal("uncertain enqueue duplicated")
	}
	w := worker(t, q)
	q.save = func(dir string, b []byte) error {
		if err := saveFile(dir, b); err != nil {
			return err
		}
		return ErrUncertain
	}
	if _, err = w.Next(t.Context(), fixtureTime); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	q.save = saveFile
	b := next(t, w)
	if b.Attempts != 1 {
		t.Fatal("uncertain claim was restarted")
	}
	finish(t, w, b)
}
func TestReadRefusesBindingSchemaCorruptionAndMissingState(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	path := filepath.Join(q.dir, "queue.json")
	original, _ := os.ReadFile(path)
	changed := fixtureBinding
	changed.ConfigID = "new-config"
	if _, err := Open(t.Context(), q.dir, changed); !errors.Is(err, ErrBinding) {
		t.Fatal(err)
	}
	for _, mode := range []string{"future", "checksum", "membership"} {
		t.Run(mode, func(t *testing.T) {
			var env envelope
			if err := json.Unmarshal(original, &env); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "future":
				env.Version++
			case "checksum":
				env.SHA256 = "wrong"
			case "membership":
				var s state
				json.Unmarshal(env.Payload, &s)
				s.Batches[0].EventIDs = nil
				env.Payload, _ = json.Marshal(s)
				sum := sha256.Sum256(env.Payload)
				env.SHA256 = hex.EncodeToString(sum[:])
			}
			bad, _ := json.Marshal(env)
			if err := os.WriteFile(path, bad, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := q.Snapshot(t.Context()); err == nil {
				t.Fatal("invalid state accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, bad) {
				t.Fatal("invalid state rewritten")
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(t.Context(), request("b"), fixtureTime); !os.IsNotExist(err) {
		t.Fatal("missing state reset", err)
	}
	if _, err := Create(t.Context(), q.dir, fixtureBinding); !os.IsNotExist(err) {
		t.Fatal("Create reset missing state", err)
	}
}
func TestCapacityCancellationAndInvalidRequests(t *testing.T) {
	q := fixture(t)
	info, _ := os.Stat(filepath.Join(q.dir, "queue.json"))
	q.limit = int(info.Size()) + 20
	if _, err := q.Enqueue(t.Context(), request("a"), fixtureTime); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	q.limit = 16 << 20
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatal("full queue lost prior state", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := q.Enqueue(ctx, request("a"), fixtureTime); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, r := range []Request{{}, request("a", ""), {EventID: "x", SessionID: "s", ProvenanceID: "p", Flush: Flush{Mode: All, Paths: []string{"p"}}}} {
		if _, err := q.Enqueue(t.Context(), r, fixtureTime); err == nil {
			t.Fatal("bad request accepted")
		}
	}
}

func TestFullQueueCanStillPublishAndAcknowledgeAcceptedWork(t *testing.T) {
	q := fixture(t)
	q.limit = 32 << 10
	accepted := 0
	for i := 0; i < 1000; i++ {
		_, err := q.Enqueue(t.Context(), request(fmt.Sprintf("capacity-%d", i)), fixtureTime)
		if errors.Is(err, ErrFull) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		accepted++
	}
	if accepted == 0 || accepted == 1000 {
		t.Fatal("capacity fixture did not fill queue")
	}
	w := worker(t, q)
	b := next(t, w)
	code := strings.Repeat("e", 4096)
	if err := w.Retry(t.Context(), b.ID, fixtureTime, code, false); err != nil {
		t.Fatal("no room to record retry", err)
	}
	b = next(t, w)
	for _, p := range []Phase{Captured, Committed} {
		if err := w.Progress(t.Context(), b.ID, p, strings.Repeat("r", 4096)); err != nil {
			t.Fatal("no room for progress", err)
		}
	}
	if err := w.Progress(t.Context(), b.ID, Pushed, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.Acknowledge(t.Context(), b.ID); err != nil {
		t.Fatal("full queue cannot drain", err)
	}
	jobs, err := q.Snapshot(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatal("accepted work stranded", err)
	}
}

func TestUncertainProgressAndAcknowledgementReflushOnRetry(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	w := worker(t, q)
	b := next(t, w)
	saves := 0
	q.save = func(dir string, data []byte) error {
		saves++
		if err := saveFile(dir, data); err != nil {
			return err
		}
		return ErrUncertain
	}
	if err := w.Progress(t.Context(), b.ID, Captured, "artifact"); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Captured, "artifact"); !errors.Is(err, ErrUncertain) || saves != 2 {
		t.Fatalf("progress retry skipped flush: %d %v", saves, err)
	}
	q.save = saveFile
	if err := w.Progress(t.Context(), b.ID, Committed, "commit"); err != nil {
		t.Fatal(err)
	}
	if err := w.Progress(t.Context(), b.ID, Pushed, ""); err != nil {
		t.Fatal(err)
	}
	saves = 0
	q.save = func(dir string, data []byte) error {
		saves++
		if err := saveFile(dir, data); err != nil {
			return err
		}
		return ErrUncertain
	}
	for range 2 {
		if err := w.Acknowledge(t.Context(), b.ID); !errors.Is(err, ErrUncertain) {
			t.Fatal(err)
		}
	}
	if saves != 2 {
		t.Fatal("ack retry skipped flush")
	}
}

func TestMetadataBoundsAccountForJSONEscaping(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("a"))
	w := worker(t, q)
	b := next(t, w)
	if err := w.Progress(t.Context(), b.ID, Captured, strings.Repeat("&", 4096)); !errors.Is(err, ErrTransition) {
		t.Fatal("escaped reference exceeded reserved capacity", err)
	}
	if err := w.Retry(t.Context(), b.ID, fixtureTime, strings.Repeat("&", 4096), false); err == nil {
		t.Fatal("escaped failure code exceeded reserved capacity")
	}
	finish(t, w, b)
}
