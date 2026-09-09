// Package queue stores vendor-neutral capture intent and publication progress.
// It does not execute captures, install hooks, or infer identity from live accounts.
package queue

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

var (
	ErrBinding    = errors.New("queue belongs to a different configuration or store")
	ErrDuplicate  = errors.New("event ID was already used for different work")
	ErrEmpty      = errors.New("no queue work is ready")
	ErrOwner      = errors.New("worker no longer owns this queue")
	ErrTransition = errors.New("invalid queue progress transition")
	ErrFull       = errors.New("queue state limit reached; existing work was retained")
	ErrUncertain  = durable.ErrUncertain
)

// Binding contains stable, credential-free identifiers, not mutable config pointers.
// The adapter must include remote, source roots and capture policy in ConfigID and
// validate them again before executing. A changed binding never redirects old jobs.
// RootID can identify a set of roots; RemoteID explicitly identifies a remote or a
// local-only destination. This storage layer does not interpret either identifier.
type Binding struct{ Vendor, StoreID, RootID, RemoteID, ConfigID string }

type Flush struct {
	Mode  string
	Paths []string
}

const (
	Normal   = "normal"
	Selected = "selected"
	All      = "all"
)

// Request contains no transcript bytes, tokens, or raw errors. EventID must remain
// stable across a producer retry, including an uncertain persistence outcome.
// ProvenanceID identifies the source's attribution, including explicit unknown
// provenance; it must never be inferred from the worker's current login.
type Request struct {
	EventID, SessionID, ProvenanceID string
	Flush                            Flush
}
type Event struct {
	Generation, BatchID uint64
	EnqueuedAt          time.Time
	Request             Request
}
type Phase string

const (
	Queued    Phase = "queued"
	Captured  Phase = "captured"
	Committed Phase = "committed"
	Pushed    Phase = "pushed"
)

type Status string

const (
	Pending Status = "pending"
	Running Status = "running"
	Blocked Status = "blocked"
)

// Work is a detached snapshot. Through covers only Events in this batch; later
// enqueues are never acknowledged by completing it. Refs name durable artifacts
// verified by the adapter, not proof that this package captured or published them.
type Work struct {
	ID, Through           uint64
	Events                []Event
	Flush                 Flush
	Phase                 Phase
	Status                Status
	CaptureRef, CommitRef string
	Attempts              uint64
	NotBefore             time.Time
	FailureCode           string
}
type batch struct {
	ID                    uint64
	EventIDs              []string
	Phase                 Phase
	Status                Status
	Owner                 string
	CaptureRef, CommitRef string
	Attempts              uint64
	NotBefore             time.Time
	FailureCode           string
	// CoverageSealed freezes membership before an external synchronous capture.
	// It does not consume a worker attempt or change the saved execution phase.
	CoverageSealed bool `json:",omitempty"`
}
type state struct {
	// version preserves an older schema until coverage upgrades it under both
	// worker ownership and the queue transaction lock. It is not payload data.
	version int
	Binding Binding
	Next    uint64
	Owner   string
	Events  map[string]Event
	Batches []batch
	Done    map[uint64]bool
}

// Queue is safe for independent goroutines/processes. Transactions use a short
// queue lock; executing a batch must not hold that lock and delay producers.
// Pass an independent operation context, not a borrowed staging-store context.
type Queue struct {
	dir     string
	binding Binding
	save    func(string, []byte) error
	limit   int
}

func newQueue(dir string, binding Binding) (*Queue, error) {
	for _, v := range []string{binding.Vendor, binding.StoreID, binding.RootID, binding.RemoteID, binding.ConfigID} {
		if !identifier(v) {
			return nil, fmt.Errorf("queue binding requires nonempty bounded identifiers")
		}
	}
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("queue directory is empty")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &Queue{dir: dir, binding: binding, save: saveFile, limit: 16 << 20}, nil
}

func (q *Queue) Enqueue(ctx context.Context, req Request, at time.Time) (Event, error) {
	req, err := normalize(req)
	if err != nil {
		return Event{}, err
	}
	if at.IsZero() {
		return Event{}, fmt.Errorf("enqueue time is required")
	}
	var event Event
	err = q.transact(ctx, func(s *state) (bool, error) {
		if old, ok := s.Events[req.EventID]; ok {
			if !reflect.DeepEqual(old.Request, req) {
				return false, ErrDuplicate
			}
			event = old
			return true, nil // reflush after an earlier uncertain publication
		}
		if s.Next == ^uint64(0) {
			return false, ErrFull
		}
		s.Next++
		event = Event{Generation: s.Next, BatchID: s.Next, EnqueuedAt: at.UTC(), Request: req}
		s.Events[req.EventID] = event
		// Never merge new input into a captured artifact, an active claim or a blocked
		// batch. Preserve any retry deadline on an otherwise coalescible queued batch.
		for i := len(s.Batches) - 1; i >= 0; i-- {
			b := &s.Batches[i]
			if provenance(s, *b) == req.ProvenanceID && b.Status == Pending && b.Phase == Queued && b.Attempts == 0 && !b.CoverageSealed {
				event.BatchID = b.ID
				s.Events[req.EventID] = event
				b.EventIDs = append(b.EventIDs, req.EventID)
				return true, q.enqueueCapacity(s)
			}
		}
		s.Batches = append(s.Batches, batch{ID: event.Generation, EventIDs: []string{req.EventID}, Phase: Queued, Status: Pending})
		return true, q.enqueueCapacity(s)
	})
	if err != nil {
		return Event{}, err
	}
	return event, nil
}

// Reserve half the state budget for progress, retry metadata and acknowledgements.
// With at most 128 outstanding batches and bounded references/failure codes,
// accepted work can still drain when enqueue capacity has been exhausted.
func (q *Queue) enqueueCapacity(s *state) error {
	if len(s.Batches) > 128 {
		return ErrFull
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(data) > q.limit/2 {
		return ErrFull
	}
	return nil
}

func (q *Queue) Snapshot(ctx context.Context) ([]Work, error) {
	var out []Work
	err := q.transact(ctx, func(s *state) (bool, error) {
		for _, b := range s.Batches {
			out = append(out, work(s, b))
		}
		return false, nil
	})
	return out, err
}

// Worker holds OS-owned exclusive queue ownership until Close or process exit.
// A single sequential executor must drive it; Next retries return the same active
// batch, including after an uncertain save. It is not a background executor. A future adapter
// must also hold staging ownership while executing and manage its child processes.
type Worker struct {
	mu      sync.Mutex
	q       *Queue
	token   string
	release func()
	closed  bool
}

func (q *Queue) Worker(ctx context.Context) (*Worker, error) {
	_, release, err := storelock.Acquire(ctx, filepath.Join(q.dir, "worker"), 0)
	if err != nil {
		return nil, err
	}
	w := &Worker{q: q, token: rand.Text(), release: release}
	err = q.transact(ctx, func(s *state) (bool, error) {
		s.Owner = w.token
		for i := range s.Batches {
			b := &s.Batches[i]
			if b.Status == Running {
				b.Status = Pending
				b.Owner = ""
			}
		}
		return true, nil
	})
	if err != nil {
		release()
		return nil, err
	}
	return w, nil
}
func (w *Worker) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		w.release()
	}
}
func (w *Worker) update(ctx context.Context, fn func(*state) (bool, error)) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrOwner
	}
	return w.q.transact(ctx, func(s *state) (bool, error) {
		if s.Owner != w.token {
			return false, ErrOwner
		}
		return fn(s)
	})
}
func (w *Worker) Next(ctx context.Context, now time.Time) (Work, error) {
	var result Work
	if now.IsZero() {
		return result, fmt.Errorf("claim time is required")
	}
	err := w.update(ctx, func(s *state) (bool, error) {
		for _, b := range s.Batches {
			if b.Status == Running {
				result = work(s, b)
				return true, nil // reflush an uncertain claim
			}
		}
		seen := map[string]bool{}
		for i := range s.Batches {
			b := &s.Batches[i]
			p := provenance(s, *b)
			prior := seen[p]
			seen[p] = true
			// A blocked or delayed earlier batch cannot be overtaken within its provenance.
			if prior || b.Status != Pending || b.NotBefore.After(now) {
				continue
			}
			if b.Attempts == ^uint64(0) {
				return false, ErrFull
			}
			b.Status = Running
			b.Owner = w.token
			b.Attempts++
			result = work(s, *b)
			return true, nil
		}
		return false, ErrEmpty
	})
	if err != nil {
		return Work{}, err
	}
	return result, nil
}
func (w *Worker) Progress(ctx context.Context, id uint64, phase Phase, ref string) error {
	return w.update(ctx, func(s *state) (bool, error) {
		b, err := owned(s, id, w.token)
		if err != nil {
			return false, err
		}
		if phase == b.Phase {
			if (phase == Captured && ref == b.CaptureRef) || (phase == Committed && ref == b.CommitRef) || (phase == Pushed && ref == "") {
				return true, nil
			}
			return false, ErrTransition
		}
		switch {
		case b.Phase == Queued && phase == Captured && identifier(ref):
			b.CaptureRef = ref
		case b.Phase == Captured && phase == Committed && identifier(ref):
			b.CommitRef = ref
		case b.Phase == Committed && phase == Pushed && ref == "":
		default:
			return false, ErrTransition
		}
		b.Phase = phase
		return true, nil
	})
}
func (w *Worker) Retry(ctx context.Context, id uint64, notBefore time.Time, code string, block bool) error {
	if !identifier(code) || notBefore.IsZero() {
		return fmt.Errorf("retry needs a time and bounded failure code")
	}
	return w.update(ctx, func(s *state) (bool, error) {
		status := Pending
		if block {
			status = Blocked
		}
		for _, b := range s.Batches {
			if b.ID == id && b.Status == status && b.Owner == "" && b.NotBefore.Equal(notBefore) && b.FailureCode == code {
				return true, nil
			}
		}
		b, err := owned(s, id, w.token)
		if err != nil {
			return false, err
		}
		if b.Phase == Pushed {
			return false, ErrTransition
		}
		b.Status = Pending
		if block {
			b.Status = Blocked
		}
		b.Owner = ""
		b.NotBefore = notBefore.UTC()
		b.FailureCode = code
		return true, nil
	})
}

// Unblock requires an explicit caller decision; new events never clear a block.
// Retrying an already-cleared pending batch reflushes the state before succeeding.
func (w *Worker) Unblock(ctx context.Context, id uint64) error {
	return w.update(ctx, func(s *state) (bool, error) {
		for i := range s.Batches {
			b := &s.Batches[i]
			if b.ID == id && b.Status == Blocked {
				b.Status = Pending
				b.NotBefore = time.Time{}
				b.FailureCode = ""
				return true, nil
			}
			if b.ID == id && b.Status == Pending && b.Owner == "" && b.Attempts > 0 && b.NotBefore.IsZero() && b.FailureCode == "" {
				return true, nil
			}
		}
		return false, ErrTransition
	})
}
func (w *Worker) Acknowledge(ctx context.Context, id uint64) error {
	return w.update(ctx, func(s *state) (bool, error) {
		if s.Done[id] {
			return true, nil
		}
		b, err := owned(s, id, w.token)
		if err != nil {
			return false, err
		}
		if b.Phase != Pushed {
			return false, ErrTransition
		}
		s.Done[id] = true
		s.Batches = slices.DeleteFunc(s.Batches, func(b batch) bool { return b.ID == id })
		return true, nil
	})
}
func owned(s *state, id uint64, owner string) (*batch, error) {
	for i := range s.Batches {
		b := &s.Batches[i]
		if b.ID == id && b.Status == Running && b.Owner == owner {
			return b, nil
		}
	}
	return nil, ErrOwner
}
func provenance(s *state, b batch) string { return s.Events[b.EventIDs[0]].Request.ProvenanceID }
func work(s *state, b batch) Work {
	r := Work{ID: b.ID, Phase: b.Phase, Status: b.Status, CaptureRef: b.CaptureRef, CommitRef: b.CommitRef, Attempts: b.Attempts, NotBefore: b.NotBefore, FailureCode: b.FailureCode, Flush: Flush{Mode: Normal}}
	for _, id := range b.EventIDs {
		e := s.Events[id]
		e.Request.Flush.Paths = slices.Clone(e.Request.Flush.Paths)
		r.Events = append(r.Events, e)
		r.Through = max(r.Through, e.Generation)
		if e.Request.Flush.Mode == All {
			r.Flush = Flush{Mode: All}
		}
		if e.Request.Flush.Mode == Selected && r.Flush.Mode != All {
			r.Flush.Mode = Selected
			r.Flush.Paths = append(r.Flush.Paths, e.Request.Flush.Paths...)
		}
	}
	slices.Sort(r.Flush.Paths)
	r.Flush.Paths = slices.Compact(r.Flush.Paths)
	return r
}
func identifier(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || len(s) > 4096 || strings.ContainsRune(s, 0) {
		return false
	}
	// Capacity reserves are in serialized bytes: JSON escaping must not expand
	// a bounded artifact reference or failure code beyond its reserved space.
	encoded, _ := json.Marshal(s)
	return len(encoded) <= 4098 // 4 KiB plus the surrounding quotes
}
func normalize(r Request) (Request, error) {
	if !identifier(r.EventID) || !identifier(r.SessionID) || !identifier(r.ProvenanceID) {
		return r, fmt.Errorf("event, session and provenance identifiers are required")
	}
	r.Flush.Paths = slices.Clone(r.Flush.Paths)
	slices.Sort(r.Flush.Paths)
	r.Flush.Paths = slices.Compact(r.Flush.Paths)
	switch r.Flush.Mode {
	case Normal, All:
		if len(r.Flush.Paths) != 0 {
			return r, fmt.Errorf("only selected flush accepts paths")
		}
		r.Flush.Paths = nil
	case Selected:
		if len(r.Flush.Paths) == 0 {
			return r, fmt.Errorf("selected flush requires paths")
		}
		for _, p := range r.Flush.Paths {
			if !identifier(p) {
				return r, fmt.Errorf("invalid flush path")
			}
		}
	default:
		return r, fmt.Errorf("invalid flush mode")
	}
	b, _ := json.Marshal(r)
	if len(b) > 64<<10 {
		return r, fmt.Errorf("request exceeds 64 KiB")
	}
	return r, nil
}
