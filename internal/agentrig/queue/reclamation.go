package queue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

// Maintenance is a sequential, callback-scoped proof of exclusive worker and
// queue-state ownership. It is issued only after the current validated state is
// durably reflushed. Never retain it or start concurrent operations with it.
type Maintenance struct {
	dir                 string
	pending             int
	history             bool
	worker, transaction context.Context
}

func (m *Maintenance) Check() error {
	if m == nil || m.worker == nil || m.transaction == nil || !storelock.SameActiveLease(m.worker, m.worker) || !storelock.SameActiveLease(m.transaction, m.transaction) {
		return ErrOwner
	}
	return nil
}
func (m *Maintenance) Directory() string {
	if m == nil {
		return ""
	}
	return m.dir
}
func (m *Maintenance) HasHistory() (bool, error) {
	if err := m.Check(); err != nil {
		return false, err
	}
	return m.history, nil
}

func (m *Maintenance) HasPending() (bool, error) {
	if err := m.Check(); err != nil {
		return true, err
	}
	return m.pending != 0, nil
}

// Maintain excludes workers, manual sync and producers while fn performs
// explicit maintenance using an independent operation context. Binding/state
// validation and a successful durable reflush happen BEFORE the callback: an
// uncertain acknowledgement must not let cleanup delete output still needed by
// a queue restored after power loss. Uncertain/failed saves never authorize fn.
// No attempts, ownership, receipts, schema or logical queue state are changed.
// Missing queues are never initialized. All acquisitions are nonwaiting. The
// callback must not reenter queue operations or retain the proof after returning.
func (q *Queue) Maintain(ctx context.Context, binding Binding, fn func(*Maintenance) error) error {
	if q == nil || fn == nil {
		return fmt.Errorf("queue maintenance requires a queue and callback")
	}
	if binding != q.binding {
		return ErrBinding
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if storelock.SameActiveLease(ctx, ctx) {
		return ErrOwner
	}
	st, err := os.Stat(q.dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return ErrOwner
	}
	worker, release, err := storelock.Acquire(ctx, filepath.Join(q.dir, "worker"), 0)
	if err != nil {
		return err
	}
	defer release()
	transaction, release, err := storelock.Acquire(ctx, q.dir, 0)
	if err != nil {
		return err
	}
	defer release()
	state, err := q.load()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := q.persist(state); err != nil {
		return err
	}
	m := &Maintenance{dir: q.dir, pending: len(state.Batches), history: state.Next > 0, worker: worker, transaction: transaction}
	if err := m.Check(); err != nil {
		return err
	}
	return fn(m)
}
