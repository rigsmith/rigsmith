package queue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

var ErrUndrained = errors.New("queue still has pending work")

// DrainPending describes a drain that reached no eligible work, not an empty
// queue. Remaining includes blocked and delayed batches plus their successors.
// NextAttempt is the earliest eligible provenance's retry time, if one exists.
// Callers must repair/unblock or retry later; this never discards accepted work.
type DrainPending struct {
	Remaining, Blocked int
	NextAttempt        time.Time
}

func (e *DrainPending) Error() string {
	return fmt.Sprintf("%s: %d batches (%d blocked)", ErrUndrained, e.Remaining, e.Blocked)
}
func (e *DrainPending) Unwrap() error { return ErrUndrained }

// RunOptions controls an in-process worker loop, not OS service installation.
// Stop requests graceful shutdown after the current batch and its cleanup finish.
// Cancel ctx for immediate shutdown through the adapter's cancellation contract.
// Wake is an optional hint; polling discovers independent-process enqueues too.
// Closed Wake channels are disabled. PollInterval defaults to one second.
//
// Drain processes ready work until idle, returning DrainPending if blocked or
// delayed batches remain. It does not wait through backoff. Producers must be
// stopped externally for a complete backlog drain; concurrent input may extend
// the run or arrive after the final empty observation. No generation watermark
// acknowledges work. Stop and context cancellation also apply while draining.
type RunOptions struct {
	// CheckStartup runs once under runner ownership, before claiming any work,
	// including an empty drain. It receives the independent operation context
	// and queue binding, and must own any staging reads and child cleanup. A
	// failure returns directly without changing attempts or retry state. This
	// observation does not replace per-batch validation or OS supervision.
	CheckStartup func(context.Context, Binding) error
	Stop, Wake   <-chan struct{}
	PollInterval time.Duration
	Drain        bool
	// Observe runs synchronously after execution cleanup and worker release.
	// It must return promptly and must not reenter Run. Errors can contain raw
	// adapter diagnostics; this package never persists them or logs them itself.
	Observe func(ExecutionResult, error)
}

// RunResult counts only batches with confirmed durable acknowledgement.
// A successful graceful stop is not a claim that the backlog is empty.
type RunResult struct{ CompletedBatches uint64 }

// Run owns a separate runner lease to exclude duplicate loops while releasing
// worker/staging ownership between batches and throughout idle/backoff waits.
// RunOne and manual sync remain available while the loop is idle. Persisted
// execution phases survive cancellation, failure and restart. Unknown errors and
// failed/uncertain queue writes stop the loop instead of consuming retry attempts.
// Classified, durably recorded failures allow other provenance to continue.
//
// The caller supplies the adapter and independent operation context. It remains
// responsible for platform supervision: this loop alone cannot terminate Unix
// helpers after abrupt process death or install a startup/restart mechanism.
func (q *Queue) Run(ctx context.Context, adapter Adapter, opts RunOptions) (result RunResult, err error) {
	if adapter == nil || opts.PollInterval < 0 {
		return result, fmt.Errorf("queue runner requires an adapter and nonnegative poll interval")
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = time.Second
	}
	_, release, err := storelock.Acquire(ctx, filepath.Join(q.dir, "runner"), 0)
	if err != nil {
		return result, fmt.Errorf("acquire queue runner ownership: %w", err)
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	select {
	case <-opts.Stop:
		return result, nil
	default:
	}
	if opts.CheckStartup != nil {
		if err := opts.CheckStartup(ctx, q.binding); err != nil {
			return result, fmt.Errorf("queue startup check: %w", err)
		}
	}
	wake := opts.Wake
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		select {
		case <-opts.Stop:
			return result, nil
		default:
		}
		jobs, err := q.Snapshot(ctx)
		if err != nil {
			return result, err
		}
		now := time.Now()
		ready, pending := runnerState(jobs, now)
		if !ready && opts.Drain {
			if pending.Remaining != 0 {
				return result, pending
			}
			return result, nil
		}
		if ready {
			// Stop is observed again after the state read, before a new claim.
			select {
			case <-opts.Stop:
				return result, nil
			default:
			}
			progress, runErr := q.RunOne(ctx, now, adapter)
			if progress.BatchID == 0 && (errors.Is(runErr, ErrEmpty) || errors.Is(runErr, storelock.ErrBusy)) {
				if opts.Drain && errors.Is(runErr, storelock.ErrBusy) {
					return result, runErr
				}
				// Another foreground operation won ownership or consumed the work.
			} else {
				if progress.Acknowledged {
					result.CompletedBatches++
				}
				if opts.Observe != nil {
					opts.Observe(progress, runErr)
				}
				if runErr != nil {
					if !progress.FailureRecorded {
						return result, runErr
					}
					var failure *ExecutionFailure
					if !errors.As(runErr, &failure) || failure == nil {
						return result, runErr
					}
					// Refuse deadlines already due when this attempt began; time
					// spent executing/persisting may legitimately exhaust a delay.
					if !failure.Blocked && !failure.RetryAt.After(now) {
						return result, runErr
					}
				}
				continue
			}
		}
		delay := opts.PollInterval
		if !pending.NextAttempt.IsZero() {
			delay = min(delay, max(0, time.Until(pending.NextAttempt)))
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-opts.Stop:
			timer.Stop()
			return result, nil
		case _, open := <-wake:
			timer.Stop()
			if !open {
				wake = nil
			}
		case <-timer.C:
		}
	}
}

// Only the first batch per provenance is eligible, matching Worker.Next. A
// running batch prompts RunOne to acquire ownership and recover abandoned work;
// a live owner is never replaced, and its busy result is retried after a wait.
func runnerState(jobs []Work, now time.Time) (bool, *DrainPending) {
	pending := &DrainPending{Remaining: len(jobs)}
	seen := make(map[string]bool)
	ready := false
	for _, b := range jobs {
		if b.Status == Blocked {
			pending.Blocked++
		}
		p := b.Events[0].Request.ProvenanceID
		prior := seen[p]
		seen[p] = true
		if b.Status == Running {
			ready = true
		}
		if prior || b.Status != Pending {
			continue
		}
		if !b.NotBefore.After(now) {
			ready = true
			continue
		}
		if pending.NextAttempt.IsZero() || b.NotBefore.Before(pending.NextAttempt) {
			pending.NextAttempt = b.NotBefore
		}
	}
	return ready, pending
}
