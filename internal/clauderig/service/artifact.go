package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// ArtifactCaptureRequest supplies the producer's binding and sealed batch plus
// freshly resolved inputs to validate against them. Identity is source attribution
// captured by the producer, including an explicit empty/unknown identity; this
// operation never reads the worker's live login. Profiles is the explicit set of
// Desktop profiles. It never discovers additional profiles at execution time.
// Current support requires every event to name an existing CLI session transcript.
// Desktop-only event routing remains separate.
type ArtifactCaptureRequest struct {
	Store    artifact.Store
	Binding  queue.Binding
	Work     queue.Work
	Sync     SyncRequest
	Identity Identity
	Profiles []string
}

// CaptureBinding fingerprints resolved configuration, source paths, staging path,
// selected profiles and the sealed-capture policy. It contains no plaintext
// configuration or remote credentials. Producers persist this value; execution
// recomputes it from freshly resolved inputs. Changed inputs never redirect work.
func CaptureBinding(req SyncRequest, profiles []string) (queue.Binding, error) {
	return captureBinding(req, profiles, nil)
}

// resolvedMode is used only after capture: the binding already pins the mode,
// so materializing its sealed bytes must not depend on today's storage marker.
func captureBinding(req SyncRequest, profiles []string, resolvedMode *bool) (queue.Binding, error) {
	if req.Config == nil || req.DryRun || req.AllowMergeTool || req.ResolveFlush != nil {
		return queue.Binding{}, fmt.Errorf("queued capture requires resolved non-interactive inputs")
	}
	profiles = slices.Clone(profiles)
	if len(profiles) == 0 {
		profiles = nil
	}
	slices.Sort(profiles)
	for i, name := range profiles {
		if err := desktop.ValidName(name); err != nil {
			return queue.Binding{}, err
		}
		if i > 0 && profiles[i-1] == name {
			return queue.Binding{}, fmt.Errorf("duplicate profile")
		}
	}
	stage, err := canonicalCapturePath(req.StagingDir)
	if err != nil {
		return queue.Binding{}, err
	}
	roots, err := captureRoots(req, profiles)
	if err != nil {
		return queue.Binding{}, err
	}
	var chunked bool
	if resolvedMode != nil {
		chunked = *resolvedMode
	} else {
		chunked, err = transcript.Enabled(req.StagingDir)
		if err != nil {
			return queue.Binding{}, err
		}
	}
	if req.Config.ChunkTranscripts != nil {
		chunked = *req.Config.ChunkTranscripts
	}
	cfg, err := json.Marshal(struct {
		Policy   string
		Chunked  bool
		Config   any
		Machine  any
		Profiles []string
	}{"claude-sealed-capture-v2", chunked, req.Config, req.Machine, profiles})
	if err != nil {
		return queue.Binding{}, err
	}
	rootsJSON, _ := json.Marshal(roots)
	return queue.Binding{Vendor: "claude", StoreID: artifact.Key([]byte(stage)), RootID: artifact.Key(rootsJSON), RemoteID: artifact.Key([]byte(req.Config.Remote)), ConfigID: artifact.Key(cfg)}, nil
}

// CaptureProvenance names the explicit source identity, not the worker's account.
// Invalid attribution is refused rather than silently converted to unknown.
func CaptureProvenance(identity Identity) (string, error) {
	if identity.AccountUUID != "" {
		id := account.CanonicalUUID(identity.AccountUUID)
		if id == "" {
			return "", fmt.Errorf("invalid source account")
		}
		identity.AccountUUID = id
	}
	if f := scanIdentity(&devices.Account{AccountUUID: identity.AccountUUID, OrganizationUUID: identity.OrganizationUUID, Email: identity.Email}); f != nil {
		return "", fmt.Errorf("invalid source identity: %s", f.Path)
	}
	b, _ := json.Marshal(identity)
	return artifact.Key(b), nil
}

// CaptureArtifact implements the capture/sealing portion of a Claude queue
// adapter. It never publishes or changes the shared staging checkout. Retrying
// the same binding and event membership reuses a verified artifact without
// reading sources. An absent/corrupt artifact is never silently substituted when
// the caller already holds a capture reference: use Store.Verify in that case.
func (s Service) CaptureArtifact(ctx context.Context, req ArtifactCaptureRequest) (string, error) {
	return s.captureArtifact(ctx, ctx, req)
}

// operation is the independent context for private stores; staging may borrow
// the queue execution's lease. Never pass staging to a different store.
func (s Service) captureArtifact(operation, staging context.Context, req ArtifactCaptureRequest) (string, error) {
	ctx := operation
	req, key, err := prepareArtifactRequest(req, queue.Queued)
	if err != nil {
		return "", err
	}
	binding := req.Binding
	roots, err := captureRoots(req.Sync, req.Profiles)
	if err != nil {
		return "", err
	}
	store, err := canonicalCapturePath(req.Store.Dir)
	if err != nil {
		return "", err
	}
	stage, err := canonicalCapturePath(req.Sync.StagingDir)
	if err != nil {
		return "", err
	}
	for _, path := range append(mapValues(roots), stage) {
		if overlapsCapture(store, path) {
			return "", fmt.Errorf("artifact store must be outside source and staging roots")
		}
	}
	// All Claude artifact services take staging before private artifact stores.
	// This also lets a queue execution retain staging across phase markers.
	_, release, err := storelock.Acquire(staging, stage, StoreWait)
	if err != nil {
		return "", err
	}
	defer release()
	req.Store.Dir = store
	return req.Store.BuildWithMetadata(ctx, key, func(ctx context.Context, tree string, meta *artifact.Metadata) error {
		current, err := CaptureBinding(req.Sync, req.Profiles)
		if err != nil {
			return err
		}
		if current != binding {
			return queue.ErrBinding
		}
		if _, err = os.Stat(filepath.Join(stage, ".git")); err == nil {
			repo, err := gitrepo.Open(ctx, stage)
			if err != nil {
				return err
			}
			if !repo.Unborn(ctx) {
				meta.BaseReference, err = repo.Head(ctx)
				if err != nil {
					return err
				}
			}
			if repo.InMerge(ctx) {
				return fmt.Errorf("%w: queued capture requires a settled staging merge", commitartifact.ErrConflict)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if meta.BaseReference != "" {
			// Persist ancestry before the capture can refer to it. Canonical
			// staging stays leased until both the source tree and seed are fixed.
			meta.SeedReference, err = commitartifact.RetainSeed(ctx, req.Store, stage, meta.BaseReference)
			if err != nil {
				return err
			}
		}
		budget := req.Store.MaxBytes
		if budget == 0 {
			budget = artifact.DefaultMaxBytes
		}
		if budget < 0 {
			return artifact.ErrTooLarge
		}
		if err = copyCaptureTree(ctx, stage, tree, &budget); err != nil {
			return err
		}
		frozen := filepath.Join(filepath.Dir(tree), "sources")
		if err = os.Mkdir(frozen, 0700); err != nil {
			return err
		}
		inputs := &captureInputs{sources: map[string]string{}, profiles: req.Profiles, attributionSessions: map[string]bool{}}
		for _, event := range req.Work.Events {
			inputs.attributionSessions[event.Request.SessionID] = true
		}
		var cliFiles []string
		for _, root := range adapter.Roots(req.Sync.Config, req.Profiles) {
			if !root.Enabled {
				continue
			}
			dst := filepath.Join(frozen, root.ID)
			inputs.sources[root.ID] = dst
			loc := roots[root.ID]
			if _, err := os.Stat(loc); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return err
			}
			paths, links, err := allowlist.Walk(loc, root.Allowlist)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if err = os.Mkdir(dst, 0700); err != nil {
				return err
			}
			for _, rel := range paths {
				if err = copyCaptureFile(ctx, filepath.Join(loc, filepath.FromSlash(rel)), filepath.Join(dst, filepath.FromSlash(rel)), &budget); err != nil {
					return err
				}
			}
			for _, link := range links {
				from := filepath.Join(dst, filepath.FromSlash(link.Rel))
				to := filepath.Join(dst, filepath.FromSlash(link.Target))
				// The walk returns files and aliases, not directories. Preserve an
				// empty (or entirely excluded) target so the frozen alias remains
				// a directory link when the capture engine walks it again.
				if err = os.MkdirAll(to, 0700); err != nil {
					return err
				}
				target, err := filepath.Rel(filepath.Dir(from), to)
				if err != nil {
					return err
				}
				if err = os.MkdirAll(filepath.Dir(from), 0700); err != nil {
					return err
				}
				if err = os.Symlink(target, from); err != nil {
					return err
				}
			}
			if root.ID == "cli" {
				cliFiles = paths
			}
		}
		required := map[string]bool{}
		for _, event := range req.Work.Events {
			var found string
			for _, rel := range cliFiles {
				if strings.HasPrefix(rel, "projects/") && filepath.Base(rel) == event.Request.SessionID+".jsonl" {
					if found != "" {
						return fmt.Errorf("ambiguous requested session")
					}
					found = rel
				}
			}
			if found == "" {
				return fmt.Errorf("%w: requested session source is missing", ErrCaptureSourceUnavailable)
			}
			required[found] = true
			for _, path := range event.Request.Flush.Paths {
				abs, err := canonicalCapturePath(path)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(roots["cli"], abs)
				if err != nil || !slices.Contains(cliFiles, filepath.ToSlash(rel)) {
					return fmt.Errorf("%w: requested flush source is unavailable", ErrCaptureSourceUnavailable)
				}
				required[filepath.ToSlash(rel)] = true
			}
		}
		// Delete only private seeded copies, forcing these requests to be read from
		// frozen sources rather than accidentally acknowledging old staged bytes.
		for rel := range required {
			dest := filepath.Join(tree, "cli", filepath.FromSlash(rel))
			if err = os.RemoveAll(dest); err != nil {
				return err
			}
			if err = os.RemoveAll(dest + transcript.Suffix); err != nil {
				return err
			}
		}
		syncReq := req.Sync
		syncReq.StagingDir = tree
		syncReq.Config.Retention.HistoryDays = 0
		syncReq.Flush = FlushIntent{Mode: FlushAll}
		pinned := s
		pinned.ReadIdentity = func() (Identity, error) { return req.Identity, nil }
		report, err := pinned.capture(ctx, syncReq, inputs)
		if err != nil {
			return err
		}
		if report.LedgerError != "" {
			return fmt.Errorf("capture ledger could not be recorded")
		}
		entries := ledger.LoadAll(tree)
		for id := range inputs.attributionSessions {
			entry, ok := entries[id]
			if !ok {
				return fmt.Errorf("requested session ledger entry missing")
			}
			if req.Identity.AccountUUID != "" && entry.Account != req.Identity.AccountUUID {
				return queue.ErrBinding
			}
		}
		for rel := range required {
			f, err := transcript.Open(filepath.Join(tree, "cli", filepath.FromSlash(rel)))
			if err != nil {
				return fmt.Errorf("requested session was not captured: %w", err)
			}
			_, err = io.Copy(io.Discard, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
}

func captureRoots(req SyncRequest, profiles []string) (map[string]string, error) {
	roots := map[string]string{}
	for _, root := range adapter.Roots(req.Config, profiles) {
		if !root.Enabled {
			continue
		}
		if root.ID == "" || root.ID == "." || root.ID == ".." || strings.EqualFold(root.ID, ".git") || strings.ContainsAny(root.ID, "/\\:") {
			return nil, fmt.Errorf("invalid capture root ID")
		}
		if _, ok := roots[root.ID]; ok {
			return nil, fmt.Errorf("duplicate capture root ID")
		}
		loc, status := root.ResolveOn(req.Machine)
		if status != pathmap.StatusResolved {
			return nil, fmt.Errorf("unresolved capture root")
		}
		path, err := canonicalCapturePath(loc)
		if err != nil {
			return nil, err
		}
		roots[root.ID] = path
	}
	return roots, nil
}
func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out
}
func overlapsCapture(a, b string) bool {
	// Conservatively reject case-only overlap even on a case-sensitive volume.
	// Canonical path spellings can retain aliases on case-folding filesystems.
	a, b = strings.ToLower(a), strings.ToLower(b)
	rel, err := filepath.Rel(a, b)
	if err == nil && filepath.IsLocal(rel) {
		return true
	}
	rel, err = filepath.Rel(b, a)
	return err == nil && filepath.IsLocal(rel)
}
func canonicalCapturePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("capture path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(abs)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", err
		}
		suffix = append(suffix, filepath.Base(abs))
		abs = parent
	}
}
func copyCaptureTree(ctx context.Context, src, dst string, budget *int64) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0700)
		}
		return copyCaptureFile(ctx, path, filepath.Join(dst, rel), budget)
	})
}
func copyCaptureFile(ctx context.Context, src, dst string, budget *int64) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("capture source must be a regular file")
	}
	if info.Size() > *budget {
		return artifact.ErrTooLarge
	}
	*budget -= info.Size()
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	actual, err := in.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return ErrCaptureSourceChanged
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|info.Mode()&0100)
	if err != nil {
		return err
	}
	_, err = io.CopyN(out, &captureReader{ctx, in}, info.Size())
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return ErrCaptureSourceChanged
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

type captureReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *captureReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

// prepareArtifactRequest detaches and validates the immutable queue inputs for
// either capture or commit without consulting the worker login.
func prepareArtifactRequest(req ArtifactCaptureRequest, phase queue.Phase) (ArtifactCaptureRequest, string, error) {
	// Deep-copy caller-owned configuration before reading it throughout the build.
	raw, err := json.Marshal(req.Sync.Config)
	if err != nil {
		return req, "", err
	}
	req.Sync.Config = nil
	if err = json.Unmarshal(raw, &req.Sync.Config); err != nil {
		return req, "", err
	}
	machine, err := json.Marshal(req.Sync.Machine)
	if err != nil {
		return req, "", err
	}
	req.Sync.Machine.Tokens = nil
	if err = json.Unmarshal(machine, &req.Sync.Machine); err != nil {
		return req, "", err
	}
	req.Profiles = slices.Clone(req.Profiles)
	slices.Sort(req.Profiles)
	binding, err := artifactPhaseBinding(req, phase)
	if err != nil {
		return req, "", err
	}
	if binding != req.Binding {
		return req, "", queue.ErrBinding
	}
	provenance, err := CaptureProvenance(req.Identity)
	if err != nil {
		return req, "", err
	}
	req.Identity.AccountUUID = account.CanonicalUUID(req.Identity.AccountUUID)
	if req.Work.ID == 0 || req.Work.Phase != phase || len(req.Work.Events) == 0 {
		return req, "", queue.ErrTransition
	}
	workJSON, err := json.Marshal(req.Work)
	if err != nil {
		return req, "", err
	}
	req.Work = queue.Work{}
	if err = json.Unmarshal(workJSON, &req.Work); err != nil {
		return req, "", err
	}
	previous := uint64(0)
	for _, event := range req.Work.Events {
		if event.BatchID != req.Work.ID || event.Generation <= previous || event.Request.ProvenanceID != provenance || event.Request.SessionID == "" {
			return req, "", queue.ErrBinding
		}
		previous = event.Generation
	}
	if req.Work.Through != previous || req.Work.Events[0].Generation != req.Work.ID {
		return req, "", queue.ErrBinding
	}
	identity, err := json.Marshal(struct {
		Binding queue.Binding
		Events  []queue.Event
	}{binding, req.Work.Events})
	if err != nil {
		return req, "", err
	}
	return req, artifact.Key(identity), nil
}

// Captured bytes already pin auto mode through ConfigID. Check both possible
// resolved values against that digest while still validating every current
// configuration/path/provenance input. Do not read the now-irrelevant live marker.
func artifactPhaseBinding(req ArtifactCaptureRequest, phase queue.Phase) (queue.Binding, error) {
	if phase != queue.Captured && phase != queue.Committed {
		return CaptureBinding(req.Sync, req.Profiles)
	}
	for _, mode := range []bool{false, true} {
		binding, err := captureBinding(req.Sync, req.Profiles, &mode)
		if err != nil {
			return queue.Binding{}, err
		}
		if binding == req.Binding {
			return binding, nil
		}
	}
	return queue.Binding{}, queue.ErrBinding
}
