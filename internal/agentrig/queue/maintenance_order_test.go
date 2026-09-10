package queue

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestReceiptCompactionOldProducerOrder(t *testing.T) {
	for _, first := range []string{"enqueue", "compaction"} {
		t.Run(first, func(t *testing.T) {
			q := fixture(t)
			enqueue(t, q, request("done"))
			w := worker(t, q)
			finish(t, w, next(t, w))
			w.Close()
			producer, err := Open(t.Context(), q.dir, fixtureBinding)
			if err != nil {
				t.Fatal(err)
			}
			cutoff := fixtureTime.Add(time.Minute)
			entered := make(chan struct{})
			resume := make(chan struct{})
			pause := func(dir string, data []byte) error {
				close(entered)
				select {
				case <-resume:
				case <-t.Context().Done():
					return t.Context().Err()
				}
				return saveFile(dir, data)
			}
			if first == "enqueue" {
				producer.save = pause
			} else {
				q.save = pause
			}
			accepted := make(chan error, 1)
			compacted := make(chan error, 1)
			enqueueOld := func() { _, err := producer.Enqueue(t.Context(), request("late"), fixtureTime); accepted <- err }
			compact := func() { _, err := q.CompactReceipts(t.Context(), cutoff); compacted <- err }
			if first == "enqueue" {
				go enqueueOld()
			} else {
				go compact()
			}
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("first transaction did not reach save")
			}
			if first == "enqueue" {
				go compact()
			} else {
				go enqueueOld()
			}
			close(resume)
			if err := <-compacted; err != nil {
				t.Fatal(err)
			}
			err = <-accepted
			if first == "enqueue" && err != nil {
				t.Fatal("lost accepted old input", err)
			}
			if first == "compaction" && !errors.Is(err, ErrExpired) {
				t.Fatal("accepted expired input", err)
			}
			// Reopen through a fresh client to inspect persisted, rather than returned, work.
			q, err = Open(t.Context(), q.dir, fixtureBinding)
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := q.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if first == "enqueue" {
				if len(jobs) != 1 || len(jobs[0].Events) != 1 || jobs[0].Events[0].Request.EventID != "late" || jobs[0].Through != 2 {
					t.Fatal("accepted work missing", jobs)
				}
				if got, err := q.Enqueue(t.Context(), request("late"), fixtureTime); err != nil || !reflect.DeepEqual(got, jobs[0].Events[0]) {
					t.Fatal("pending retry no longer deduplicates", got, err)
				}
				w = worker(t, q)
				finish(t, w, next(t, w))
				w.Close()
			} else if len(jobs) != 0 {
				t.Fatal("expired input persisted", jobs)
			}
			if _, err := q.CompactReceipts(t.Context(), cutoff); err != nil {
				t.Fatal(err)
			}
			if _, err := q.Enqueue(t.Context(), request("late"), fixtureTime); !errors.Is(err, ErrExpired) {
				t.Fatal("replayed expired work", err)
			}
		})
	}
}
