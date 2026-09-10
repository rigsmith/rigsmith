package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

// receiptCompaction is an explicit producer replay contract, not an automatic
// retention policy. Retired accounts for gaps without reusing any generation.
type receiptCompaction struct {
	Before  time.Time
	Retired uint64
}

// Capacity describes serialized queue payload usage, not filesystem free space
// or artifact storage. EnqueueHeadroom excludes the reserved progress budget;
// each new request also needs event/batch overhead and, unless coalesced, a
// free batch slot.
type Capacity struct {
	PayloadBytes, StateLimit, EnqueueLimit, EnqueueHeadroom int
	OutstandingBatches, BatchLimit                          int
	PendingBatches, RunningBatches, BlockedBatches          int
	OutstandingEvents, CompletedReceipts, CompletedBatches  int
	RetiredReceipts                                         uint64
	ReplayBefore                                            time.Time
	Remedies                                                []string
}

// Capacity reads validated state without changing receipts, attempts or schema.
// Remedies describe explicit operations; this method never performs them.
func (q *Queue) Capacity(ctx context.Context) (Capacity, error) {
	var result Capacity
	err := q.transact(ctx, func(s *state) (bool, error) {
		var err error
		result, err = q.capacity(s)
		return false, err
	})
	if err != nil {
		return Capacity{}, err
	}
	return result, nil
}

func (q *Queue) capacity(s *state) (Capacity, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return Capacity{}, err
	}
	c := Capacity{PayloadBytes: len(data), StateLimit: q.limit, EnqueueLimit: q.limit / 2,
		EnqueueHeadroom: max(0, q.limit/2-len(data)), OutstandingBatches: len(s.Batches),
		BatchLimit: 128, CompletedBatches: len(s.Done)}
	for _, b := range s.Batches {
		c.OutstandingEvents += len(b.EventIDs)
		switch b.Status {
		case Pending:
			c.PendingBatches++
		case Running:
			c.RunningBatches++
		case Blocked:
			c.BlockedBatches++
		}
	}
	c.CompletedReceipts = len(s.Events) - c.OutstandingEvents
	if s.Compaction != nil {
		c.RetiredReceipts, c.ReplayBefore = s.Compaction.Retired, s.Compaction.Before
	}
	if c.PendingBatches+c.RunningBatches > 0 {
		c.Remedies = append(c.Remedies, "drain accepted work; existing retry deadlines still apply")
	}
	if c.BlockedBatches > 0 {
		c.Remedies = append(c.Remedies, "repair blocked work, then explicitly unblock it")
	}
	if c.CompletedReceipts > 0 {
		c.Remedies = append(c.Remedies, "agree a producer replay cutoff, then compact completed receipts")
	}
	return c, nil
}

type CompactionResult struct {
	RemovedReceipts, RemovedBatches int
	Capacity                        Capacity
}

// CompactReceipts retires only whole completed batches whose original enqueue
// times are strictly before before. The caller must first coordinate producers:
// retries retain their original timestamp, and work older than the cutoff is no
// longer submitted. Unknown events older than the durable cutoff get ErrExpired,
// never a success acknowledgement. No automatic age policy is implied.
//
// The cutoff can only advance. This method excludes workers/coverage operations
// and holds the queue transaction lock across compaction and schema-3 upgrade.
// Producers serialize on that transaction lock; all unfinished work is retained.
// Artifacts, staging, retry state and monotonic generation IDs remain untouched.
// A missing/corrupt queue is never initialized. ErrUncertain requires retrying
// the same cutoff: even a no-op retry reflushes before reporting success. Removal
// counts describe this successful attempt, not an earlier uncertain attempt.
func (q *Queue) CompactReceipts(ctx context.Context, before time.Time) (CompactionResult, error) {
	if before.IsZero() {
		return CompactionResult{}, fmt.Errorf("producer replay cutoff is required")
	}
	info, err := os.Stat(q.dir) // root aliases share the canonical queue/worker locks
	if err != nil {
		return CompactionResult{}, err
	}
	if !info.IsDir() {
		return CompactionResult{}, fmt.Errorf("queue maintenance requires a directory")
	}
	_, release, err := storelock.Acquire(ctx, filepath.Join(q.dir, "worker"), 0)
	if err != nil {
		return CompactionResult{}, err
	}
	defer release()
	var result CompactionResult
	err = q.transact(ctx, func(s *state) (bool, error) {
		if s.Compaction != nil && before.Before(s.Compaction.Before) {
			return false, fmt.Errorf("producer replay cutoff cannot move backward")
		}
		keep := make(map[uint64]bool)
		for _, e := range s.Events {
			if !e.EnqueuedAt.Before(before) {
				keep[e.BatchID] = true
			}
		}
		for id, e := range s.Events {
			if s.Done[e.BatchID] && !keep[e.BatchID] {
				delete(s.Events, id)
				result.RemovedReceipts++
			}
		}
		for id := range s.Done {
			if !keep[id] {
				delete(s.Done, id)
				result.RemovedBatches++
			}
		}
		if s.Compaction == nil {
			s.Compaction = &receiptCompaction{}
		}
		s.Compaction.Before = before.UTC()
		s.Compaction.Retired += uint64(result.RemovedReceipts)
		s.version = compactionVersion
		var err error
		result.Capacity, err = q.capacity(s)
		return true, err
	})
	if err != nil {
		return CompactionResult{}, err
	}
	return result, nil
}
