package queue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Adapter opens one sequential execution under worker ownership. Begin must
// validate the binding, provenance and any saved artifact references before
// acting, and acquire the staging lease using ctx. It must release any resources
// itself on error. The returned Execution holds that lease until Close.
//
// Adapters must preserve requested sources, seal immutable durable artifacts,
// and make external effects replayable by batch ID. Queue phase markers cannot
// close a crash window between an external effect and its marker by themselves.
type Adapter interface {
	Begin(context.Context, Binding, Work) (Execution, error)
}

// Execution operates on a single sealed batch. Capture returns an immutable,
// durable capture reference; Commit returns a retained commit reference. Push
// succeeds only after confirming remote publication of that commit. A local-only
// commit cannot count as Push success. Each method receives a detached snapshot.
//
// Implementations retain their staging context internally; the driver uses the
// original context for queue transactions so it never borrows the staging lease
// to acquire a different store. Close must finish/terminate child processes
// before releasing staging ownership, including after cancellation or failure.
type Execution interface {
	Capture(context.Context, Work) (string, error)
	Commit(context.Context, Work) (string, error)
	Push(context.Context, Work) error
	Close()
}

// ExecutionFailure explicitly classifies an adapter failure for durable retry
// or blocking. Code, RetryAt and Blocked are persisted, never Cause or its text.
// An ordinary error stops execution without choosing a retry policy; the next
// worker recovers the claim at its last durable phase. Caller cancellation stops
// without classification; an adapter's own timeout may use a classified retry.
type ExecutionFailure struct {
	Code    string
	RetryAt time.Time
	Blocked bool
	Cause   error
}

func (e *ExecutionFailure) Error() string { return "queue execution failed: " + e.Code }
func (e *ExecutionFailure) Unwrap() error { return e.Cause }

// ExecutionResult reports only confirmed queue progress. A persistence error may
// have advanced the on-disk phase further; reopen and inspect before retrying.
type ExecutionResult struct {
	BatchID      uint64
	Phase        Phase
	Acknowledged bool
}

// RunOne owns a worker for at most one ready batch, advancing only unfinished
// phases. It does not start a daemon, sleep through backoff, or enable producers.
// A later call reopens persisted state, so an offline committed batch goes
// straight to Push rather than recapturing live sources. ErrEmpty means no batch
// is eligible at now. Concurrent calls cannot execute through the same worker.
func (q *Queue) RunOne(ctx context.Context, now time.Time, adapter Adapter) (result ExecutionResult, err error) {
	if adapter == nil || now.IsZero() {
		return result, fmt.Errorf("queue execution requires an adapter and claim time")
	}
	w, err := q.Worker(ctx)
	if err != nil {
		return result, err
	}
	defer w.Close()
	b, err := w.Next(ctx, now)
	if err != nil {
		return result, err
	}
	result.BatchID, result.Phase = b.ID, b.Phase
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// Publication was already confirmed; acknowledging it requires no replay of
	// vendor effects or acquisition of a staging lease.
	if b.Phase != Pushed {
		execution, err := adapter.Begin(ctx, q.binding, cloneWork(b))
		if err != nil {
			return result, w.executionFailed(ctx, b.ID, err)
		}
		if execution == nil {
			return result, fmt.Errorf("queue adapter returned no execution")
		}
		defer execution.Close() // runs before worker ownership is released
		for b.Phase != Pushed {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			var ref string
			var phase Phase
			switch b.Phase {
			case Queued:
				phase = Captured
				ref, err = execution.Capture(ctx, cloneWork(b))
			case Captured:
				phase = Committed
				ref, err = execution.Commit(ctx, cloneWork(b))
			case Committed:
				phase = Pushed
				err = execution.Push(ctx, cloneWork(b))
			default:
				return result, ErrTransition
			}
			if err != nil {
				return result, w.executionFailed(ctx, b.ID, err)
			}
			// Never advance to another external effect after a failed/uncertain
			// marker write, and never reclassify a persistence error as an
			// adapter failure that could overwrite its uncertain outcome.
			if err = w.Progress(ctx, b.ID, phase, ref); err != nil {
				return result, err
			}
			b.Phase, result.Phase = phase, phase
			if phase == Captured {
				b.CaptureRef = ref
			} else if phase == Committed {
				b.CommitRef = ref
			}
		}
	}
	if err := w.Acknowledge(ctx, b.ID); err != nil {
		return result, err
	}
	result.Acknowledged = true
	return result, nil
}

func (w *Worker) executionFailed(ctx context.Context, id uint64, err error) error {
	if ctx.Err() != nil {
		return err
	}
	var failure *ExecutionFailure
	if errors.As(err, &failure) && failure != nil {
		if saveErr := w.Retry(ctx, id, failure.RetryAt, failure.Code, failure.Blocked); saveErr != nil {
			return errors.Join(err, saveErr)
		}
	}
	return err
}

func cloneWork(b Work) Work {
	b.Flush.Paths = slices.Clone(b.Flush.Paths)
	b.Events = slices.Clone(b.Events)
	for i := range b.Events {
		b.Events[i].Request.Flush.Paths = slices.Clone(b.Events[i].Request.Flush.Paths)
	}
	return b
}
