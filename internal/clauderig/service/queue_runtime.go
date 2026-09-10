package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/durable"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
)

const runtimeLimit = 1 << 20
const runtimeIdentityLimit = 1024

// QueueRuntime owns one local queue lifecycle and its private stores. Its root
// must be private, stable, and outside source/staging trees. Never copy, replace,
// reset or share its children. This is internal rollout plumbing, not hook wiring.
// Queue state keeps a lifecycle-specific binding; the adapter translates it to
// the capture-policy binding only after validating the persisted association.
type QueueRuntime struct {
	dir              string
	capture, binding queue.Binding
	id               string
	request          SyncRequest
	profiles         []string
	q                *queue.Queue
	save             func(context.Context, string, func(*os.File) error) error
}

type runtimeState struct {
	Version    int
	ID         string
	Location   string
	Capture    queue.Binding
	Identities map[string]Identity
}
type runtimeEnvelope struct {
	Payload json.RawMessage
	SHA256  string
}

// CreateQueueRuntime initializes only a new directory. Existing valid runtimes
// are reopened and reflushed. An incomplete initialization, missing queue, changed
// binding or corrupt descriptor is never repaired by creating empty state.
func CreateQueueRuntime(ctx context.Context, dir string, req SyncRequest, profiles []string) (*QueueRuntime, error) {
	r, err := prepareQueueRuntime(dir, req, profiles)
	if err != nil {
		return nil, err
	}
	r.capture, err = CaptureBinding(req, profiles)
	if err != nil {
		return nil, err
	}
	return createQueueRuntime(ctx, r)
}

func createQueueRuntime(ctx context.Context, r *QueueRuntime) (*QueueRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if storelock.SameActiveLease(ctx, ctx) {
		return nil, queue.ErrOwner
	}
	_, release, err := storelock.Acquire(ctx, r.dir, StoreWait)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := os.Mkdir(r.dir, 0700); err != nil {
		if !os.IsExist(err) {
			return nil, err
		}
		s, err := r.load()
		if err != nil {
			return nil, err
		}
		if err = r.attach(ctx, s); err != nil {
			return nil, err
		}
		if err = r.q.Maintain(ctx, r.binding, func(*queue.Maintenance) error { return nil }); err != nil {
			return nil, err
		}
		if err = r.persist(ctx, s); err != nil {
			return nil, err
		}
		return r, nil
	}
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	s := runtimeState{Version: 1, ID: hex.EncodeToString(id[:]), Location: artifact.Key([]byte(r.dir)), Capture: r.capture, Identities: map[string]Identity{}}
	r.id = s.ID
	r.binding = r.queueBinding(s.ID)
	r.q, err = queue.Create(ctx, filepath.Join(r.dir, "queue"), r.binding)
	if err != nil {
		return nil, err
	}
	if err = r.persist(ctx, s); err != nil {
		return nil, err
	}
	return r, nil
}

// OpenQueueRuntime validates existing metadata and queue state without creating
// either. Fresh configuration must match the saved capture policy.
func OpenQueueRuntime(ctx context.Context, dir string, req SyncRequest, profiles []string) (*QueueRuntime, error) {
	r, err := prepareQueueRuntime(dir, req, profiles)
	if err != nil {
		return nil, err
	}
	if err := r.refresh(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func prepareQueueRuntime(dir string, req SyncRequest, profiles []string) (*QueueRuntime, error) {
	mode := false
	binding, err := captureBinding(req, profiles, &mode)
	if err != nil {
		return nil, err
	}
	if req.Config.Remote == "" {
		return nil, queue.ErrBinding
	}
	root, err := canonicalCapturePath(dir)
	if err != nil {
		return nil, err
	}
	stage, err := canonicalCapturePath(req.StagingDir)
	if err != nil {
		return nil, err
	}
	roots, err := captureRoots(req, profiles)
	if err != nil {
		return nil, err
	}
	for _, path := range append(mapValues(roots), stage) {
		if overlapsCapture(root, path) {
			return nil, queue.ErrBinding
		}
	}
	return &QueueRuntime{dir: root, capture: binding, save: durable.Write, request: req, profiles: slices.Clone(profiles)}, nil
}

func (r *QueueRuntime) queueBinding(id string) queue.Binding {
	b := r.capture
	data, _ := json.Marshal([]string{"claude-queue-runtime-v1", r.dir, id, b.StoreID})
	b.StoreID = artifact.Key(data)
	return b
}

func (r *QueueRuntime) load() (runtimeState, error) {
	var s runtimeState
	// Managed children must never be redirected independently of their lifecycle.
	for _, name := range []string{".", "queue", "captures", "commits"} {
		info, err := os.Lstat(filepath.Join(r.dir, name))
		if os.IsNotExist(err) && (name == "captures" || name == "commits") {
			continue
		}
		if err != nil {
			return s, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !runtimePrivate(info) {
			return s, queue.ErrBinding
		}
	}
	path := filepath.Join(r.dir, "runtime.json")
	info, err := os.Lstat(path)
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() || info.Size() > runtimeLimit || !runtimePrivate(info) {
		return s, queue.ErrBinding
	}
	f, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, runtimeLimit+1))
	if err != nil {
		return s, err
	}
	if len(data) > runtimeLimit {
		return s, queue.ErrBinding
	}
	var e runtimeEnvelope
	if err := runtimeDecode(data, &e); err != nil {
		return s, err
	}
	if artifact.Key(e.Payload) != e.SHA256 {
		return s, queue.ErrBinding
	}
	if err := runtimeDecode(e.Payload, &s); err != nil {
		return s, err
	}
	id, err := hex.DecodeString(s.ID)
	if err != nil || len(id) != 32 || hex.EncodeToString(id) != s.ID || s.Version != 1 || s.Location != artifact.Key([]byte(r.dir)) || s.Identities == nil || len(s.Identities) > runtimeIdentityLimit {
		return s, queue.ErrBinding
	}
	_, bindingErr := artifactPhaseBinding(ArtifactCaptureRequest{Binding: s.Capture, Sync: r.request, Profiles: r.profiles}, queue.Committed)
	if bindingErr != nil {
		return s, bindingErr
	}
	if r.id != "" && (r.id != s.ID || r.capture != s.Capture) {
		return s, queue.ErrBinding
	}
	for key, identity := range s.Identities {
		hash, err := CaptureProvenance(identity)
		if err != nil || key != hash {
			return s, queue.ErrBinding
		}
	}
	return s, nil
}

func runtimeDecode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("invalid queue runtime metadata: %w", queue.ErrBinding)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return queue.ErrBinding
	}
	return nil
}

func (r *QueueRuntime) persist(ctx context.Context, s runtimeState) error {
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	data, err := json.Marshal(runtimeEnvelope{payload, artifact.Key(payload)})
	if err != nil {
		return err
	}
	if len(data) > runtimeLimit {
		return queue.ErrFull
	}
	return r.save(ctx, filepath.Join(r.dir, "runtime.json"), func(f *os.File) error { _, err := f.Write(data); return err })
}
func (r *QueueRuntime) attach(ctx context.Context, s runtimeState) error {
	r.capture = s.Capture
	b := r.queueBinding(s.ID)
	q, err := queue.Open(ctx, filepath.Join(r.dir, "queue"), b)
	if err != nil {
		return err
	}
	r.id, r.binding, r.q = s.ID, b, q
	return nil
}
func (r *QueueRuntime) refresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if storelock.SameActiveLease(ctx, ctx) {
		return queue.ErrOwner
	}
	// Opening a missing root must not create it through lock setup.
	if _, err := os.Stat(r.dir); err != nil {
		return err
	}
	_, release, err := storelock.Acquire(ctx, r.dir, StoreWait)
	if err != nil {
		return err
	}
	defer release()
	s, err := r.load()
	if err != nil {
		return err
	}
	return r.attach(ctx, s)
}

// Snapshot returns detached unfinished work without exposing producer mutators.
func (r *QueueRuntime) Snapshot(ctx context.Context) ([]queue.Work, error) {
	return r.q.Snapshot(ctx)
}

// RunOne executes one claimed batch using Adapter's lifecycle validation.
// The underlying queue is private: all producers must use runtime.Enqueue so
// attribution is durable before acceptance.
func (r *QueueRuntime) RunOne(ctx context.Context, at time.Time, adapter queue.Adapter) (queue.ExecutionResult, error) {
	return r.q.RunOne(ctx, at, adapter)
}

// Run forwards the worker loop's stop/drain contract. Callers provide Adapter,
// CheckStartup and OS supervision; this method never accepts producer events.
func (r *QueueRuntime) Run(ctx context.Context, adapter queue.Adapter, opts queue.RunOptions) (queue.RunResult, error) {
	return r.q.Run(ctx, adapter, opts)
}

// Enqueue durably saves validated producer attribution before accepting work.
// The caller must retain identity, EventID and timestamp across uncertain retries;
// never substitute a later login. A failed queue write can leave an unused identity.
// Identities are bounded and retained; there is no automatic provenance pruning.
func (r *QueueRuntime) Enqueue(ctx context.Context, identity Identity, request queue.Request, at time.Time) (queue.Event, error) {
	fail := queue.Event{}
	provenance, err := CaptureProvenance(identity)
	if err != nil {
		return fail, err
	}
	if request.ProvenanceID != "" && request.ProvenanceID != provenance {
		return fail, queue.ErrBinding
	}
	if identity.AccountUUID != "" {
		identity.AccountUUID = account.CanonicalUUID(identity.AccountUUID)
	}
	request.ProvenanceID = provenance
	s, release, err := r.lockState(ctx)
	if err != nil {
		return fail, err
	}
	defer release()
	_, existed := s.Identities[provenance]
	if old, ok := s.Identities[provenance]; ok {
		if !reflect.DeepEqual(old, identity) {
			return fail, queue.ErrBinding
		}
	} else {
		if len(s.Identities) >= runtimeIdentityLimit {
			return fail, queue.ErrFull
		}
		s.Identities[provenance] = identity
	}
	// Reflush even an existing identity after a possibly uncertain earlier save.
	if err := r.persist(ctx, s); err != nil {
		return fail, err
	}
	event, err := r.q.Enqueue(ctx, request, at)
	if !existed && err != nil && !errors.Is(err, queue.ErrUncertain) {
		delete(s.Identities, provenance)
		err = errors.Join(err, r.persist(ctx, s))
	}
	return event, err
}

// lockState centralizes the producer/resolver critical section. It returns a
// release only on success; failures leave no runtime lease held.
func (r *QueueRuntime) lockState(ctx context.Context) (runtimeState, func(), error) {
	fail := runtimeState{}
	if err := ctx.Err(); err != nil {
		return fail, nil, err
	}
	if storelock.SameActiveLease(ctx, ctx) {
		return fail, nil, queue.ErrOwner
	}
	if _, err := os.Stat(r.dir); err != nil {
		return fail, nil, err
	}
	_, release, err := storelock.Acquire(ctx, r.dir, StoreWait)
	if err != nil {
		return fail, nil, err
	}
	s, err := r.load()
	if err == nil {
		_, err = queue.Open(ctx, r.q.Directory(), r.binding)
	}
	if err != nil {
		release()
		return fail, nil, err
	}
	return s, release, nil
}

// QueueRuntimeInputs comes from fresh local resolution and existing Git/gh
// privacy/authentication checks. Paths and producer attribution are supplied by
// the runtime, never by the resolver. Limits apply equally to both private stores.
type QueueRuntimeInputs struct {
	Sync                     SyncRequest
	Profiles                 []string
	Remote                   ArtifactTransport
	MaxBytes, MaxStoredBytes int64
}

func (r *QueueRuntime) inputs(ctx context.Context, in QueueRuntimeInputs, provenance string) (QueueInputs, error) {
	fail := QueueInputs{}
	current, err := prepareQueueRuntime(r.dir, in.Sync, in.Profiles)
	if err != nil {
		return fail, err
	}
	if _, err := artifactPhaseBinding(ArtifactCaptureRequest{Binding: r.capture, Sync: current.request, Profiles: current.profiles}, queue.Committed); err != nil {
		return fail, err
	}
	if in.MaxBytes < 0 || in.MaxStoredBytes < 0 {
		return fail, queue.ErrBinding
	}
	s, release, err := r.lockState(ctx)
	if err != nil {
		return fail, err
	}
	defer release()
	identity := Identity{}
	if provenance != "" {
		var ok bool
		identity, ok = s.Identities[provenance]
		if !ok {
			return fail, queue.ErrBinding
		}
	}
	return QueueInputs{Sync: in.Sync, Profiles: in.Profiles, Identity: identity, Remote: in.Remote,
		Captures: artifact.Store{Dir: filepath.Join(r.dir, "captures"), MaxBytes: in.MaxBytes, MaxStoredBytes: in.MaxStoredBytes},
		Commits:  artifact.Store{Dir: filepath.Join(r.dir, "commits"), MaxBytes: in.MaxBytes, MaxStoredBytes: in.MaxStoredBytes}}, nil
}

type runtimeAdapter struct {
	runtime *QueueRuntime
	adapter QueueAdapter
}

func (a runtimeAdapter) Begin(ctx context.Context, b queue.Binding, w queue.Work) (queue.Execution, error) {
	if b != a.runtime.binding {
		return nil, queue.ErrBinding
	}
	return a.adapter.Begin(ctx, a.runtime.capture, w)
}

// Adapter resolves fresh configuration for each batch, restores the saved
// producer identity, and bridges lifecycle binding to capture-policy binding.
func (r *QueueRuntime) Adapter(s Service, resolve func(context.Context) (QueueRuntimeInputs, error)) queue.Adapter {
	return runtimeAdapter{r, QueueAdapter{Service: s, Resolve: func(ctx context.Context, b queue.Binding, p string) (QueueInputs, error) {
		if resolve == nil || b != r.capture {
			return QueueInputs{}, queue.ErrBinding
		}
		in, err := resolve(ctx)
		if err != nil {
			return QueueInputs{}, err
		}
		return r.inputs(ctx, in, p)
	}}}
}

// CheckStartup preserves the shared-history startup gate with the runtime's
// fixed stores. Callers still provide OS command supervision to Queue.Run.
func (r *QueueRuntime) CheckStartup(ctx context.Context, s Service, in QueueRuntimeInputs) error {
	inputs, err := r.inputs(ctx, in, "")
	if err != nil {
		return err
	}
	return s.checkQueueStartup(ctx, r.capture, inputs, true)
}

// ScopeID identifies this immutable runtime binding without exposing stores or
// queue mutators. Saved producer requests must match it before admission.
func (r *QueueRuntime) ScopeID() string {
	data, _ := json.Marshal(r.binding)
	return artifact.Key(data)
}

// Capacity reports queue metadata headroom; it does not count archive disk use.
func (r *QueueRuntime) Capacity(ctx context.Context) (queue.Capacity, error) {
	return r.q.Capacity(ctx)
}

// RetryBlocked explicitly permits a repaired batch to run again. Worker
// ownership preserves saved phases and excludes an active execution attempt.
// No staging process fence or timed backoff is cleared.
func (r *QueueRuntime) RetryBlocked(ctx context.Context, id uint64) error {
	_, release, err := r.lockState(ctx)
	if err != nil {
		return err
	}
	release() // Never wait for worker ownership while holding runtime ownership.
	worker, err := r.q.Worker(ctx)
	if err != nil {
		return err
	}
	defer worker.Close()
	return worker.Unblock(ctx, id)
}
