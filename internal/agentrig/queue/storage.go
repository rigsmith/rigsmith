package queue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
)

const formatVersion = 1

type envelope struct {
	Version int
	Payload json.RawMessage
	SHA256  string
}

// Create bootstraps a new private queue directory. If it already exists, this
// validates and reflushes its existing state before succeeding. A missing state
// file is an error, never an empty queue reset.
// A failed first initialization can leave an uninitialized directory requiring
// explicit inspection/removal before Create is retried.
func Create(ctx context.Context, dir string, binding Binding) (*Queue, error) {
	q, err := newQueue(dir, binding)
	if err != nil {
		return nil, err
	}
	if err = q.create(ctx); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) create(ctx context.Context) error {
	_, release, err := storelock.Acquire(ctx, q.dir, 15*time.Second)
	if err != nil {
		return err
	}
	defer release()
	s := &state{Binding: q.binding, Events: map[string]Event{}, Done: map[uint64]bool{}}
	if err = os.Mkdir(q.dir, 0700); err != nil {
		if !os.IsExist(err) {
			return err
		}
		if s, err = q.load(); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return q.persist(s)
}

// Open never initializes or repairs state. Binding/schema/corruption failures
// leave every byte in place. Temporary files from interrupted saves are ignored.
func Open(ctx context.Context, dir string, binding Binding) (*Queue, error) {
	q, err := newQueue(dir, binding)
	if err != nil {
		return nil, err
	}
	err = q.transact(ctx, func(*state) (bool, error) { return false, nil })
	if err != nil {
		return nil, err
	}
	return q, nil
}
func (q *Queue) transact(ctx context.Context, fn func(*state) (bool, error)) error {
	_, release, err := storelock.Acquire(ctx, q.dir, 15*time.Second)
	if err != nil {
		return err
	}
	defer release()
	s, err := q.load()
	if err != nil {
		return err
	}
	changed, err := fn(s)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return q.persist(s)
}
func (q *Queue) load() (*state, error) {
	path := filepath.Join(q.dir, "queue.json")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("queue state must be a regular file")
	}
	if info.Size() > int64(q.limit) {
		return nil, ErrFull
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(q.limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > q.limit {
		return nil, ErrFull
	}
	var env envelope
	if err = decode(data, &env); err != nil {
		return nil, fmt.Errorf("invalid queue envelope: %w", err)
	}
	if env.Version != formatVersion {
		return nil, fmt.Errorf("unsupported queue schema %d", env.Version)
	}
	hash := sha256.Sum256(env.Payload)
	if env.SHA256 != hex.EncodeToString(hash[:]) {
		return nil, fmt.Errorf("queue checksum mismatch")
	}
	var s state
	if err = decode(env.Payload, &s); err != nil {
		return nil, fmt.Errorf("invalid queue state: %w", err)
	}
	if s.Binding != q.binding {
		return nil, ErrBinding
	}
	if err = validate(&s); err != nil {
		return nil, fmt.Errorf("invalid queue state: %w", err)
	}
	return &s, nil
}
func decode(b []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}
func (q *Queue) persist(s *state) error {
	if err := validate(s); err != nil {
		return err
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	data, err := json.Marshal(envelope{Version: formatVersion, Payload: payload, SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		return err
	}
	if len(data) > q.limit {
		return ErrFull
	}
	return q.save(q.dir, data)
}
func validate(s *state) error {
	if s.Events == nil || s.Done == nil || uint64(len(s.Events)) != s.Next {
		return fmt.Errorf("event generation count mismatch")
	}
	generations := map[uint64]bool{}
	assigned := map[string]bool{}
	active := map[uint64]bool{}
	doneMembers := map[uint64]bool{}
	for id, e := range s.Events {
		normalized, err := normalize(e.Request)
		if err != nil || !reflect.DeepEqual(normalized, e.Request) || id != e.Request.EventID || e.EnqueuedAt.IsZero() || e.Generation == 0 || e.Generation > s.Next || e.BatchID == 0 || e.BatchID > e.Generation || generations[e.Generation] {
			return fmt.Errorf("invalid event")
		}
		generations[e.Generation] = true
	}
	previous := uint64(0)
	running := 0
	for _, b := range s.Batches {
		if b.ID <= previous || len(b.EventIDs) == 0 || s.Done[b.ID] {
			return fmt.Errorf("invalid batch identity")
		}
		previous = b.ID
		active[b.ID] = true
		provenanceID := ""
		last := uint64(0)
		for i, id := range b.EventIDs {
			e, ok := s.Events[id]
			if !ok || assigned[id] || e.BatchID != b.ID || e.Generation <= last || (i == 0 && e.Generation != b.ID) {
				return fmt.Errorf("invalid batch membership")
			}
			if i > 0 && provenanceID != e.Request.ProvenanceID {
				return fmt.Errorf("mixed batch provenance")
			}
			provenanceID = e.Request.ProvenanceID
			last = e.Generation
			assigned[id] = true
		}
		switch b.Status {
		case Running:
			running++
			if b.Owner == "" || b.Owner != s.Owner || b.Attempts == 0 {
				return fmt.Errorf("invalid claim owner")
			}
		case Pending, Blocked:
			if b.Owner != "" {
				return fmt.Errorf("unclaimed batch has owner")
			}
		default:
			return fmt.Errorf("invalid batch status")
		}
		if b.Status == Blocked && !identifier(b.FailureCode) {
			return fmt.Errorf("blocked batch lacks failure code")
		}
		switch b.Phase {
		case Queued:
			if b.CaptureRef != "" || b.CommitRef != "" {
				return fmt.Errorf("queued batch has artifact refs")
			}
		case Captured:
			if !identifier(b.CaptureRef) || b.CommitRef != "" {
				return fmt.Errorf("invalid capture progress")
			}
		case Committed, Pushed:
			if !identifier(b.CaptureRef) || !identifier(b.CommitRef) {
				return fmt.Errorf("invalid publication progress")
			}
		default:
			return fmt.Errorf("invalid phase")
		}
	}
	if running > 1 {
		return fmt.Errorf("multiple active claims")
	}
	for id, e := range s.Events {
		if !assigned[id] {
			if !s.Done[e.BatchID] {
				return fmt.Errorf("unacknowledged event was dropped")
			}
			doneMembers[e.BatchID] = true
		}
	}
	for id, done := range s.Done {
		if !done || id == 0 || id > s.Next || active[id] || !doneMembers[id] {
			return fmt.Errorf("invalid acknowledgement")
		}
	}
	return nil
}

// saveFile publishes the complete queue snapshot using the shared durability layer.
func saveFile(dir string, data []byte) error {
	return durable.Write(context.Background(), filepath.Join(dir, "queue.json"), func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}
