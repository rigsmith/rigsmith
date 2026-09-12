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
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

func queueBytes(t *testing.T, q *Queue) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(q.dir, "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReceiptCompactionPreservesEveryUnfinishedPhase(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("retire"))
	w := worker(t, q)
	finish(t, w, next(t, w))
	old := enqueue(t, q, request("mixed-old"))
	cutoff := fixtureTime.Add(time.Minute)
	if _, err := q.Enqueue(t.Context(), request("mixed-new"), cutoff); err != nil {
		t.Fatal(err)
	}
	finish(t, w, next(t, w))
	for _, phase := range []Phase{Queued, Captured, Committed, Pushed} {
		r := request(string(phase))
		r.ProvenanceID = string(phase)
		enqueue(t, q, r)
		b := next(t, w)
		for _, p := range []Phase{Captured, Committed, Pushed} {
			if phase == Queued {
				break
			}
			ref := string(p) + "-saved"
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
			if err := w.Retry(t.Context(), b.ID, fixtureTime.Add(time.Hour), "repair-required", true); err != nil {
				t.Fatal(err)
			}
		}
	}
	w.Close() // even an abandoned running/pushed record must remain unchanged
	before, err := q.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err := q.CompactReceipts(t.Context(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedReceipts != 1 || result.RemovedBatches != 1 || result.Capacity.RetiredReceipts != 1 || result.Capacity.CompletedReceipts != 2 || result.Capacity.BlockedBatches != 3 || result.Capacity.RunningBatches != 1 {
		t.Fatalf("bad compaction: %+v", result)
	}
	after, err := q.Snapshot(t.Context())
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("changed unfinished work", after, err)
	}
	if got, err := q.Enqueue(t.Context(), old.Request, old.EnqueuedAt); err != nil || !reflect.DeepEqual(got, old) {
		t.Fatal("split completed batch", got, err)
	}
	for _, b := range before {
		e := b.Events[0]
		if got, err := q.Enqueue(t.Context(), e.Request, e.EnqueuedAt); err != nil || !reflect.DeepEqual(got, e) {
			t.Fatal("lost unfinished receipt", got, err)
		}
	}
	if _, err := q.Enqueue(t.Context(), request("retire"), fixtureTime); !errors.Is(err, ErrExpired) {
		t.Fatal("recreated retired event", err)
	}
	if _, err := q.Enqueue(t.Context(), request("never-accepted"), fixtureTime); !errors.Is(err, ErrExpired) {
		t.Fatal("accepted obsolete input", err)
	}
	if e, err := q.Enqueue(t.Context(), request("boundary"), cutoff); err != nil || e.Generation != 8 {
		t.Fatal("reused generation or excluded boundary", e, err)
	}
}

func TestReceiptCompactionRestoresCapacityAfterDrain(t *testing.T) {
	q := fixture(t)
	q.limit = 32 << 10
	accepted := 0
	w := worker(t, q)
	for i := 0; i < 1000; i++ {
		_, err := q.Enqueue(t.Context(), request(fmt.Sprintf("fill-%d", i)), fixtureTime)
		if errors.Is(err, ErrFull) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		accepted++
		finish(t, w, next(t, w))
	}
	if accepted == 0 || accepted == 1000 {
		t.Fatal("capacity fixture did not fill")
	}
	w.Close()
	before := queueBytes(t, q)
	c, err := q.Capacity(t.Context())
	if err != nil || c.CompletedReceipts != accepted || c.OutstandingEvents != 0 || c.OutstandingBatches != 0 || c.EnqueueLimit != q.limit/2 || c.StateLimit != q.limit || len(c.Remedies) != 1 {
		t.Fatal(c, err)
	}
	if !bytes.Equal(before, queueBytes(t, q)) {
		t.Fatal("capacity observation wrote queue")
	}
	result, err := q.CompactReceipts(t.Context(), fixtureTime.Add(time.Minute))
	if err != nil || result.RemovedReceipts != accepted || result.Capacity.EnqueueHeadroom <= c.EnqueueHeadroom || result.Capacity.CompletedReceipts != 0 {
		t.Fatal(result, err)
	}
	q2, err := Open(t.Context(), q.dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	q2.limit = q.limit // restored headroom must work under the original budget
	if _, err := q2.Enqueue(t.Context(), request("fill-0"), fixtureTime); !errors.Is(err, ErrExpired) {
		t.Fatal("restart replayed receipt", err)
	}
	if e, err := q2.Enqueue(t.Context(), request("fresh"), fixtureTime.Add(time.Hour)); err != nil || e.Generation != uint64(accepted+1) {
		t.Fatal(e, err)
	}
	w2 := worker(t, q2)
	finish(t, w2, next(t, w2))
}

func TestReceiptCompactionPersistenceAndExclusion(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("done"))
	w := worker(t, q)
	finish(t, w, next(t, w))
	cutoff := fixtureTime.Add(time.Minute)
	before := queueBytes(t, q)
	if _, err := q.CompactReceipts(t.Context(), cutoff); !errors.Is(err, storelock.ErrBusy) {
		t.Fatal("maintenance bypassed worker", err)
	}
	w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := q.CompactReceipts(ctx, cutoff); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	q.save = func(string, []byte) error { return os.ErrPermission }
	if result, err := q.CompactReceipts(t.Context(), cutoff); !errors.Is(err, os.ErrPermission) || !reflect.DeepEqual(result, CompactionResult{}) {
		t.Fatal(result, err)
	}
	if !bytes.Equal(before, queueBytes(t, q)) {
		t.Fatal("failed compaction changed bytes")
	}
	saves := 0
	q.save = func(dir string, data []byte) error {
		saves++
		if err := saveFile(dir, data); err != nil {
			return err
		}
		return ErrUncertain
	}
	if result, err := q.CompactReceipts(t.Context(), cutoff); !errors.Is(err, ErrUncertain) || !reflect.DeepEqual(result, CompactionResult{}) {
		t.Fatal(result, err)
	}
	q.save = func(dir string, data []byte) error { saves++; return saveFile(dir, data) }
	result, err := q.CompactReceipts(t.Context(), cutoff)
	if err != nil || saves != 2 || result.RemovedReceipts != 0 || result.Capacity.RetiredReceipts != 1 {
		t.Fatal("uncertain retry did not reflush", result, saves, err)
	}
	before = queueBytes(t, q)
	if _, err := q.CompactReceipts(t.Context(), fixtureTime); err == nil {
		t.Fatal("moved cutoff backwards")
	}
	if _, err := q.CompactReceipts(t.Context(), time.Time{}); err == nil {
		t.Fatal("accepted zero cutoff")
	}
	if !bytes.Equal(before, queueBytes(t, q)) {
		t.Fatal("invalid cutoff changed bytes")
	}
}

func TestReceiptCompactionPreservesLegacySeals(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("done"))
			w := worker(t, q)
			finish(t, w, next(t, w))
			w.Close()
			pending := enqueue(t, q, request("pending"))
			if version == 2 {
				// Simulate a pending batch sealed by an older build.
				if err := q.transact(t.Context(), func(s *state) (bool, error) {
					s.version = 2
					s.Batches[0].CoverageSealed = true
					return true, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			s, err := q.load()
			if err != nil {
				t.Fatal(err)
			}
			s.version = version
			if err := q.persist(s); err != nil {
				t.Fatal(err)
			}
			if _, err := q.Capacity(t.Context()); err != nil {
				t.Fatal(err)
			}
			s, _ = q.load()
			if s.version != version {
				t.Fatal("read upgraded state")
			}
			if _, err := q.CompactReceipts(t.Context(), fixtureTime.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			s, err = q.load()
			if err != nil || (version == 2 && !s.Batches[0].CoverageSealed) {
				t.Fatal("compaction lost coverage seal", err)
			}
			w = worker(t, q)
			finish(t, w, next(t, w))
			w.Close()
			s, err = q.load()
			if err != nil || s.version != 3 || s.Compaction == nil || s.Compaction.Retired != 1 || !s.Done[pending.BatchID] {
				t.Fatal("worker downgraded compaction", s, err)
			}
			if _, err := q.CompactReceipts(t.Context(), fixtureTime.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			s, err = q.load()
			if err != nil || s.Compaction.Retired != 2 || s.Next != 2 {
				t.Fatal(s, err)
			}
		})
	}
}

func TestReceiptCompactionRejectsMalformedState(t *testing.T) {
	for _, mode := range []string{"legacy-metadata", "missing-metadata", "zero-cutoff", "overflow-count", "missing-generation", "checksum"} {
		t.Run(mode, func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("keep"))
			if _, err := q.CompactReceipts(t.Context(), fixtureTime.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			var env envelope
			if err := json.Unmarshal(queueBytes(t, q), &env); err != nil {
				t.Fatal(err)
			}
			var s state
			if err := json.Unmarshal(env.Payload, &s); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "legacy-metadata":
				env.Version = 2
			case "missing-metadata":
				s.Compaction = nil
			case "zero-cutoff":
				s.Compaction.Before = time.Time{}
			case "overflow-count":
				s.Compaction.Retired = ^uint64(0)
			case "missing-generation":
				s.Next++
			}
			env.Payload, _ = json.Marshal(s)
			hash := sha256.Sum256(env.Payload)
			env.SHA256 = hex.EncodeToString(hash[:])
			if mode == "checksum" {
				env.SHA256 = "bad"
			}
			bad, _ := json.Marshal(env)
			if err := os.WriteFile(filepath.Join(q.dir, "queue.json"), bad, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := q.Capacity(t.Context()); err == nil {
				t.Fatal("read invalid state")
			}
			if _, err := q.CompactReceipts(t.Context(), fixtureTime.Add(time.Hour)); err == nil {
				t.Fatal("compacted invalid state")
			}
			if !bytes.Equal(bad, queueBytes(t, q)) {
				t.Fatal("changed malformed state")
			}
		})
	}
	q, err := newQueue(filepath.Join(t.TempDir(), "absent"), fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CompactReceipts(t.Context(), fixtureTime); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(q.dir); !os.IsNotExist(err) {
		t.Fatal("created absent queue", err)
	}
}

func TestReceiptCompactionProcessExit(t *testing.T) {
	for _, mode := range []string{"compact-before", "compact-after"} {
		t.Run(mode, func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("done"))
			w := worker(t, q)
			finish(t, w, next(t, w))
			enqueue(t, q, request("retained"))
			b := next(t, w)
			if err := w.Progress(t.Context(), b.ID, Captured, "retained-capture"); err != nil {
				t.Fatal(err)
			}
			if err := w.Retry(t.Context(), b.ID, fixtureTime.Add(time.Hour), "offline", false); err != nil {
				t.Fatal(err)
			}
			w.Close()
			before, err := q.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			cmd, ready := subprocess(t, q.dir, mode)
			expectLine(t, ready, "compaction-interrupted")
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			q, err = Open(t.Context(), q.dir, fixtureBinding)
			if err != nil {
				t.Fatal(err)
			}
			c, err := q.Capacity(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "compact-after" {
				want = 0
			}
			if c.CompletedReceipts != want {
				t.Fatal("wrong save boundary", c)
			}
			if _, err := q.CompactReceipts(t.Context(), fixtureTime.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			after, err := q.Snapshot(t.Context())
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("changed saved work", after, err)
			}
			if _, err := q.Enqueue(t.Context(), request("done"), fixtureTime); !errors.Is(err, ErrExpired) {
				t.Fatal("replayed retired event", err)
			}
			w = worker(t, q)
			finish(t, w, next(t, w))
		})
	}
}

func TestReceiptCompactionSerializesProducers(t *testing.T) {
	q := fixture(t)
	enqueue(t, q, request("done"))
	w := worker(t, q)
	finish(t, w, next(t, w))
	w.Close()
	producer, err := Open(t.Context(), q.dir, fixtureBinding)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	resume := make(chan struct{})
	q.save = func(dir string, data []byte) error {
		close(entered)
		select {
		case <-resume:
		case <-t.Context().Done():
			return t.Context().Err()
		}
		return saveFile(dir, data)
	}
	compacted := make(chan error, 1)
	cutoff := fixtureTime.Add(time.Minute)
	go func() { _, err := q.CompactReceipts(t.Context(), cutoff); compacted <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("maintenance did not reach save")
	}
	// The maintenance transaction owns both locks, without impersonating a worker.
	if w, err := producer.Worker(t.Context()); !errors.Is(err, storelock.ErrBusy) {
		if w != nil {
			w.Close()
		}
		close(resume)
		t.Fatal("maintenance did not exclude execution", err)
	}
	accepted := make(chan error, 1)
	go func() {
		_, err := producer.Enqueue(t.Context(), request("later"), cutoff)
		accepted <- err
	}()
	close(resume)
	if err := <-compacted; err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	if _, err := producer.Enqueue(t.Context(), request("done"), fixtureTime); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	jobs, err := producer.Snapshot(t.Context())
	if err != nil || len(jobs) != 1 || len(jobs[0].Events) != 1 || jobs[0].Events[0].Request.EventID != "later" || jobs[0].Through != 2 {
		t.Fatal("lost producer arrival", jobs, err)
	}
}
