package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Ecosystem is the in-process interface every language adapter implements. The
// subprocess transport (SubprocessEcosystem) mirrors it method-for-method, so a
// built-in adapter and an external plugin are interchangeable.
type Ecosystem interface {
	// Info returns identity and capabilities.
	Info() EcosystemInfo
	// Detect reports whether this ecosystem applies to the repo at root.
	Detect(ctx context.Context, root string) (bool, error)
	// Discover enumerates the releasable packages this ecosystem owns.
	Discover(ctx context.Context, req DiscoverRequest) (DiscoverResponse, error)
	// SetVersion stamps a new version (and any dependency-range rewrites) into a
	// package's manifest, format-preserving.
	SetVersion(ctx context.Context, req SetVersionRequest) error
	// Publish publishes a package via its native package manager. Implementations
	// should be idempotent (skip already-published versions).
	Publish(ctx context.Context, req PublishRequest) (PublishResponse, error)
	// Artifacts builds the package's distributable files into req.OutputDir and
	// returns them. Separate from Publish: it produces, it does not ship. An
	// adapter with nothing to build (e.g. a Go module published by tag, with no
	// goreleaser config) returns Skipped.
	Artifacts(ctx context.Context, req ArtifactsRequest) (ArtifactsResponse, error)
	// ReleaseInit declares the ecosystem's release prerequisites — env tokens to
	// preflight and any build-config file to scaffold — so a release `init`
	// wizard can set the repo up without hardcoding per-ecosystem knowledge.
	ReleaseInit(ctx context.Context, req ReleaseInitRequest) (ReleaseInitResponse, error)
}

// Registry holds the available ecosystem adapters (built-in + discovered).
type Registry struct {
	mu    sync.RWMutex
	byID  map[string]Ecosystem
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byID: map[string]Ecosystem{}}
}

// Register adds an adapter, keyed by its Info().ID. A later registration with
// the same id wins (lets a user override a built-in with an external plugin).
func (r *Registry) Register(e Ecosystem) {
	id := e.Info().ID
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[id]; !exists {
		r.order = append(r.order, id)
	}
	r.byID[id] = e
}

// Get returns the adapter with the given id.
func (r *Registry) Get(id string) (Ecosystem, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.byID[id]
	return e, ok
}

// All returns the adapters in registration order.
func (r *Registry) All() []Ecosystem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Ecosystem, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// DetectAll returns the ids of every ecosystem that applies to the repo, sorted
// for stable output. A polyglot repo legitimately matches several.
func (r *Registry) DetectAll(ctx context.Context, root string) ([]string, error) {
	var ids []string
	for _, e := range r.All() {
		ok, err := e.Detect(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("detect %s: %w", e.Info().ID, err)
		}
		if ok {
			ids = append(ids, e.Info().ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// SubprocessEcosystem adapts an external command to the Ecosystem interface by
// speaking the JSON protocol. The command is invoked as `cmd <method>` with the
// request JSON on stdin and the response JSON on stdout.
type SubprocessEcosystem struct {
	host *Host
	info EcosystemInfo
}

// NewSubprocessEcosystem connects to an external adapter, calling its "info"
// method once to learn its identity and capabilities.
func NewSubprocessEcosystem(ctx context.Context, host *Host) (*SubprocessEcosystem, error) {
	var info EcosystemInfo
	if err := host.Call(ctx, MethodInfo, map[string]any{"apiVersion": APIVersion}, &info); err != nil {
		return nil, err
	}
	if info.APIVersion != APIVersion {
		return nil, fmt.Errorf("plugin %s speaks apiVersion %d, engine speaks %d", info.ID, info.APIVersion, APIVersion)
	}
	return &SubprocessEcosystem{host: host, info: info}, nil
}

func (s *SubprocessEcosystem) Info() EcosystemInfo { return s.info }

func (s *SubprocessEcosystem) Detect(ctx context.Context, root string) (bool, error) {
	var resp struct {
		Detected bool `json:"detected"`
	}
	err := s.host.Call(ctx, MethodDetect, map[string]any{"apiVersion": APIVersion, "repoRoot": root}, &resp)
	return resp.Detected, err
}

func (s *SubprocessEcosystem) Discover(ctx context.Context, req DiscoverRequest) (DiscoverResponse, error) {
	req.APIVersion = APIVersion
	var resp DiscoverResponse
	err := s.host.Call(ctx, MethodDiscover, req, &resp)
	return resp, err
}

func (s *SubprocessEcosystem) SetVersion(ctx context.Context, req SetVersionRequest) error {
	req.APIVersion = APIVersion
	return s.host.Call(ctx, MethodSetVersion, req, nil)
}

func (s *SubprocessEcosystem) Publish(ctx context.Context, req PublishRequest) (PublishResponse, error) {
	req.APIVersion = APIVersion
	var resp PublishResponse
	err := s.host.Call(ctx, MethodPublish, req, &resp)
	return resp, err
}

func (s *SubprocessEcosystem) Artifacts(ctx context.Context, req ArtifactsRequest) (ArtifactsResponse, error) {
	req.APIVersion = APIVersion
	var resp ArtifactsResponse
	err := s.host.Call(ctx, MethodArtifacts, req, &resp)
	return resp, err
}

func (s *SubprocessEcosystem) ReleaseInit(ctx context.Context, req ReleaseInitRequest) (ReleaseInitResponse, error) {
	req.APIVersion = APIVersion
	var resp ReleaseInitResponse
	err := s.host.Call(ctx, MethodReleaseInit, req, &resp)
	return resp, err
}

// ensure SubprocessEcosystem satisfies Ecosystem.
var _ Ecosystem = (*SubprocessEcosystem)(nil)

// decodeRaw is a helper used by adapters to decode their config block.
func decodeRaw(raw rawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}
