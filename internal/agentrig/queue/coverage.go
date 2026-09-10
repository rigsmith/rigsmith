package queue

import (
	"context"
	"reflect"
	"slices"
)

// Coverage holds the exact candidate batches sealed before a synchronous
// capture. It is valid only in the Worker session that prepared it. Losing this
// object (including process death) leaves its work pending, ready for ordinary
// execution or a new coverage attempt; it is not a publication receipt.
type Coverage struct {
	worker  *Worker
	batches []Work
}

// PrepareCoverage freezes the unattempted pending prefix for one provenance.
// The caller must derive binding and provenance from its actual capture inputs,
// acquire worker ownership before staging ownership, and call this before
// reading sources. Queue operations use the independent operation context, not
// a borrowed staging lease. Producers remain free to enqueue newer batches.
//
// Attempted, delayed, blocked and retained batches stop the prefix: manual sync
// cannot bypass their recovery or substitute live input for a saved artifact.
// No capture, acknowledgement, phase change or attempt increment happens here.
// ErrEmpty means no candidate work. An uncertain save returns no usable ticket;
// retry preparation before starting capture.
func (w *Worker) PrepareCoverage(ctx context.Context, binding Binding, provenanceID string) (*Coverage, error) {
	if binding != w.q.binding || !identifier(provenanceID) {
		return nil, ErrBinding
	}
	c := &Coverage{worker: w}
	err := w.update(ctx, func(s *state) (bool, error) {
		for _, b := range s.Batches {
			if b.Status == Running {
				return false, ErrTransition
			}
		}
		for i := range s.Batches {
			b := &s.Batches[i]
			if provenance(s, *b) != provenanceID {
				continue
			}
			if b.Status != Pending || b.Phase != Queued || b.Attempts != 0 || !b.NotBefore.IsZero() || b.FailureCode != "" {
				break
			}
			b.CoverageSealed = true
			c.batches = append(c.batches, work(s, *b))
		}
		if len(c.batches) == 0 {
			return false, ErrEmpty
		}
		// The additive schema upgrade runs only while both ownership locks
		// exclude an older executor. Older binaries then fail closed on Open.
		s.version = max(s.version, formatVersion)
		// Reflush even if a preceding uncertain preparation already sealed them.
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Batches returns detached candidates. A vendor must establish coverage of each
// event's requested sources and flush intent, then confirm remote publication.
// Successful sync, a high-water mark, or an empty error alone is not proof.
func (c *Coverage) Batches() []Work {
	batches := make([]Work, len(c.batches))
	for i, b := range c.batches {
		batches[i] = cloneWork(b)
	}
	return batches
}

// Acknowledge records only complete candidate batches whose every event is in
// generations, returning their acknowledged generations in batch/event order.
// A partially covered batch stays pending in full. Generations outside this
// ticket are rejected, even if lower than a covered generation. The queue trusts
// the vendor's coverage/publication evidence; it cannot inspect native sources.
//
// Call only after confirmed remote publication, never for dry runs, local-only
// commits, skipped/missing inputs or failed publication. On ErrUncertain, retry
// the same ticket and generations while retaining worker ownership. On process
// death, remaining pending work is safe to replay; no publication is inferred.
func (c *Coverage) Acknowledge(ctx context.Context, generations []uint64) ([]uint64, error) {
	if c == nil || c.worker == nil {
		return nil, ErrOwner
	}
	known := make(map[uint64]bool)
	for _, b := range c.batches {
		for _, e := range b.Events {
			known[e.Generation] = true
		}
	}
	covered := make(map[uint64]bool, len(generations))
	for _, generation := range generations {
		if !known[generation] {
			return nil, ErrTransition
		}
		covered[generation] = true
	}
	var acknowledged []uint64
	err := c.worker.update(ctx, func(s *state) (bool, error) {
		// A sequential coverage operation cannot be interleaved with worker
		// execution, including a claim outside this ticket's provenance.
		for _, b := range s.Batches {
			if b.Status == Running {
				return false, ErrTransition
			}
		}
		for _, candidate := range c.batches {
			if s.Done[candidate.ID] {
				continue
			}
			i := slices.IndexFunc(s.Batches, func(b batch) bool { return b.ID == candidate.ID })
			if i < 0 || !s.Batches[i].CoverageSealed || !reflect.DeepEqual(work(s, s.Batches[i]), candidate) {
				return false, ErrTransition
			}
		}
		for _, candidate := range c.batches {
			if slices.ContainsFunc(candidate.Events, func(e Event) bool { return !covered[e.Generation] }) {
				continue
			}
			if !s.Done[candidate.ID] {
				i := slices.IndexFunc(s.Batches, func(b batch) bool { return b.ID == candidate.ID })
				s.Done[candidate.ID] = true
				s.Batches = slices.Delete(s.Batches, i, i+1)
			}
			for _, e := range candidate.Events {
				acknowledged = append(acknowledged, e.Generation)
			}
		}
		// Reflush completed receipts on retry after uncertain durability.
		return len(acknowledged) > 0, nil
	})
	if err != nil {
		return nil, err
	}
	return acknowledged, nil
}
