package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/adapter"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/backupgit"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// CoverageSyncResult preserves ordinary sync progress even when subsequent
// remote confirmation or queue acknowledgement fails. Acknowledged contains only
// durably completed generations; partial batches remain pending in full.
type CoverageSyncResult struct {
	Sync         SyncResult
	Acknowledged []uint64
}

// SyncWithCoverage composes manual sync with an existing queue. It holds worker
// ownership before staging, observes live identity once at the usual capture
// point, and acknowledges only fresh, retained session groups in a confirmed
// remote snapshot. Ordinary Sync and installed hooks remain unchanged.
//
// The queue must be outside source/staging roots and match freshly resolved
// configuration. The caller must have verified remote privacy, as for Sync.
// Dry runs and local-only syncs never prepare or acknowledge work.
// No worker/producer is installed. External merge tools remain unsupported here
// until their process lifetime can be fenced by the worker lifecycle integration.
func (s Service) SyncWithCoverage(ctx context.Context, req SyncRequest, q *queue.Queue) (CoverageSyncResult, error) {
	return s.syncWithCoverage(ctx, req, q, nil)
}

func (s Service) syncWithCoverage(ctx context.Context, req SyncRequest, q *queue.Queue, runtime *QueueRuntime) (result CoverageSyncResult, err error) {
	if err := requireCanonicalRunner(ctx, req.AllowMergeTool); err != nil {
		return result, err
	}
	if q == nil || req.Config == nil {
		return result, fmt.Errorf("coverage sync requires configuration and queue")
	}
	if req.AllowMergeTool {
		return result, fmt.Errorf("coverage sync does not support external merge tools")
	}
	// Freeze caller-owned configuration, including machine path tokens.
	raw, err := json.Marshal(req.Config)
	if err != nil {
		return result, err
	}
	req.Config = nil
	if err := json.Unmarshal(raw, &req.Config); err != nil {
		return result, err
	}
	raw, err = json.Marshal(req.Machine)
	if err != nil {
		return result, err
	}
	req.Machine.Tokens = nil
	if err := json.Unmarshal(raw, &req.Machine); err != nil {
		return result, err
	}
	req.Flush.Paths = slices.Clone(req.Flush.Paths)
	if runtime != nil {
		if _, err := runtime.validateCoverageBinding(ctx, req, engine.LocalProfileNames()); err != nil {
			return result, err
		}
	}
	var remote *commitartifact.GitTransport
	if !req.DryRun && req.Config.Remote != "" {
		plan := adapter.PublicationPlan(req.Machine.Name, req.Config.Retention)
		remote, err = commitartifact.NewConfiguredGitTransport(commitartifact.GitTransportOptions{Remote: req.Config.Remote, Branch: plan.Branch})
		if err != nil {
			return result, err
		}
	}
	worker, err := q.Worker(ctx)
	if err != nil {
		return result, err
	}
	defer worker.Close()
	staging, release, err := storelock.Acquire(ctx, req.StagingDir, StoreWait)
	if err != nil {
		return result, err
	}
	defer release()
	staging = canonicalContext(staging)
	defer func() { err = canonicalResult(staging, err) }()
	ctx = process.WithSupervisorLease(ctx, staging)
	c := &manualCoverage{operation: ctx, worker: worker, queueDir: q.Directory(), runtime: runtime}
	// Validate exclusion even for local-only/dry runs: their capture still walks.
	if err := c.checkLayout(req, engine.LocalProfileNames()); err != nil {
		return result, err
	}
	if runtime != nil || (!req.DryRun && req.Config.Remote != "") {
		req.coverage = c
	}
	result.Sync, err = s.Sync(staging, req)
	if err != nil || req.coverage == nil || c.ticket == nil || len(c.proven) == 0 {
		return result, err
	}
	if !result.Sync.Publication.Pushed {
		return result, commitartifact.ErrUnconfirmed
	}
	err = commitartifact.ConfirmSnapshot(ctx, commitartifact.SnapshotConfirmation{
		Commit: result.Sync.Publication.SnapshotCommit, ScratchParent: c.queueDir, Remote: remote,
		Verify: func(ctx context.Context, root string) error {
			if err := backupgit.ValidateTree(ctx, root); err != nil {
				return err
			}
			if err := engine.CheckPublishContext(ctx, root); err != nil {
				return err
			}
			for path, expected := range c.digests {
				actual, err := snapshotDigest(ctx, filepath.Join(root, "cli", filepath.FromSlash(path)))
				if err != nil {
					return err
				}
				if actual != expected {
					return fmt.Errorf("%w: captured bytes differ from committed snapshot", commitartifact.ErrUnconfirmed)
				}
			}
			entries := ledger.LoadAll(root)
			for _, event := range c.proven {
				entry, ok := entries[event.Request.SessionID]
				if !ok || (c.identity.AccountUUID != "" && entry.Account != c.identity.AccountUUID) {
					return queue.ErrBinding
				}
			}
			return nil
		},
	})
	if err != nil {
		return result, err
	}
	var generations []uint64
	for _, event := range c.proven {
		generations = append(generations, event.Generation)
	}
	if c.runtime != nil {
		if _, err := c.runtime.validateCoverageBinding(ctx, req, engine.LocalProfileNames()); err != nil {
			return result, err
		}
	}
	result.Acknowledged, err = c.ticket.Acknowledge(ctx, generations)
	return result, err
}

type manualCoverage struct {
	runtime           *QueueRuntime
	operation         context.Context
	worker            *queue.Worker
	queueDir, cliRoot string
	identity          Identity
	ticket            *queue.Coverage
	root              adapter.Root
	required          map[uint64][]string
	evidencePaths     map[string]bool
	proven            []queue.Event
	digests           map[string]string
}

func (c *manualCoverage) checkLayout(req SyncRequest, profiles []string) error {
	roots, err := captureRoots(req, profiles)
	if err != nil {
		return err
	}
	dir, err := canonicalCapturePath(c.queueDir)
	if err != nil {
		return err
	}
	stage, err := canonicalCapturePath(req.StagingDir)
	if err != nil {
		return err
	}
	for _, path := range append(mapValues(roots), stage) {
		if overlapsCapture(dir, path) {
			return fmt.Errorf("queue must be outside source and staging roots")
		}
	}
	c.queueDir = dir
	return nil
}

// prepare runs after merge repair and the single identity/flush observation,
// before the engine reads sources. Identity errors never become unknown-account
// acknowledgements. The capture itself retains its ordinary best-effort identity.
func (c *manualCoverage) prepare(req SyncRequest, identity Identity, identityErr error, profiles []string) error {
	if err := c.checkLayout(req, profiles); err != nil {
		return err
	}
	var binding queue.Binding
	var err error
	if c.runtime != nil {
		binding, err = c.runtime.validateCoverageBinding(c.operation, req, profiles)
		if err != nil {
			return err
		}
	}
	// Invalid identity suppresses acknowledgement, never runtime validation.
	// Dry runs also revalidate but must not prepare a coverage transaction.
	if identityErr != nil || req.DryRun {
		return nil
	}
	provenance, err := CaptureProvenance(identity)
	if err != nil {
		return nil
	}
	// Flush decoding and dry-run/tool flags are not part of the queue binding.
	bindingReq := req
	bindingReq.ResolveFlush = nil
	bindingReq.AllowMergeTool = false
	bindingReq.DryRun = false
	if c.runtime == nil {
		binding, err = CaptureBinding(bindingReq, profiles)
		if err != nil {
			return err
		}
	}
	c.ticket, err = c.worker.PrepareCoverage(c.operation, binding, provenance)
	if errors.Is(err, queue.ErrEmpty) {
		return nil
	}
	if err != nil {
		return err
	}
	c.identity = identity
	c.identity.AccountUUID = account.CanonicalUUID(identity.AccountUUID)
	roots, err := captureRoots(req, profiles)
	if err != nil {
		return err
	}
	c.cliRoot = roots["cli"]
	for _, root := range adapter.Roots(req.Config, profiles) {
		if root.ID == "cli" && root.Enabled {
			c.root = root
		}
	}
	if c.cliRoot == "" {
		return nil
	}
	paths, _, err := allowlist.Walk(c.cliRoot, c.root.Allowlist)
	if err != nil {
		return nil
	} // Missing/churning sources cannot prove coverage.
	c.required = make(map[uint64][]string)
	c.evidencePaths = make(map[string]bool)
	for _, batch := range c.ticket.Batches() {
		for _, event := range batch.Events {
			required := c.sessionPaths(event, paths)
			c.required[event.Generation] = required
			for _, rel := range required {
				c.evidencePaths[filepath.Join(c.cliRoot, filepath.FromSlash(rel))] = true
			}
		}
	}
	return nil
}

// sessionPaths includes the native parent, its subagents, selected flush groups,
// and (for all-flush) all allowlisted CLI transcripts. Ambiguity is not coverage.
func (c *manualCoverage) sessionPaths(event queue.Event, paths []string) []string {
	var parent string
	var transcripts []string
	for _, path := range paths {
		if !c.root.Classify(path).ChunkEligible {
			continue
		}
		transcripts = append(transcripts, path)
		if strings.HasPrefix(path, "projects/") && strings.Count(path, "/") == 2 && filepath.Base(path) == event.Request.SessionID+".jsonl" {
			if parent != "" {
				return nil
			}
			parent = path
		}
	}
	if parent == "" {
		return nil
	}
	selected := []string{parent}
	for _, path := range event.Request.Flush.Paths {
		abs, err := canonicalCapturePath(path)
		if err != nil {
			return nil
		}
		matched := false
		for _, rel := range transcripts {
			target, err := canonicalCapturePath(filepath.Join(c.cliRoot, filepath.FromSlash(rel)))
			if err == nil && target == abs {
				// Keep the allowlisted entry, including aliases to external files,
				// while matching flush paths as the capture engine does.
				selected = append(selected, rel)
				matched = true
			}
		}
		if !matched {
			return nil
		}
	}
	if event.Request.Flush.Mode == queue.All {
		return transcripts
	}
	result := slices.Clone(selected)
	for _, path := range transcripts {
		for _, rel := range selected {
			if strings.HasPrefix(path, strings.TrimSuffix(rel, ".jsonl")+"/") {
				result = append(result, path)
				break
			}
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func (c *manualCoverage) captured(req SyncRequest, report *engine.Report) error {
	// Discovery may have changed during the walk. Refuse publication even if
	// identity/evidence cannot produce a coverage ticket for this capture.
	if c.runtime != nil {
		if _, err := c.runtime.validateCoverageBinding(c.operation, req, engine.LocalProfileNames()); err != nil {
			return err
		}
	}
	if c.ticket == nil || c.cliRoot == "" || report == nil || report.LedgerError != "" {
		return nil
	}
	paths, _, err := allowlist.Walk(c.cliRoot, c.root.Allowlist)
	if err != nil {
		return nil
	}
	entries := ledger.LoadAll(req.StagingDir)
	c.digests = make(map[string]string)
	cache := make(map[string]string)
	for _, batch := range c.ticket.Batches() {
		var proven []queue.Event
		batchDigests := make(map[string]string)
		for _, event := range batch.Events {
			required := c.required[event.Generation]
			current := c.sessionPaths(event, paths)
			if len(required) == 0 || len(current) == 0 {
				continue
			}
			required = append(slices.Clone(required), current...)
			entry, ok := entries[event.Request.SessionID]
			if !ok || (c.identity.AccountUUID != "" && entry.Account != c.identity.AccountUUID) {
				continue
			}
			digests := make(map[string]string)
			complete := true
			for _, rel := range required {
				if !report.FreshSnapshots[filepath.Join(c.cliRoot, filepath.FromSlash(rel))] {
					complete = false
					break
				}
				digest, ok := cache[rel]
				if !ok {
					var err error
					digest, err = snapshotDigest(c.operation, filepath.Join(req.StagingDir, "cli", filepath.FromSlash(rel)))
					if err != nil {
						complete = false
						break
					}
					cache[rel] = digest
				}
				digests[rel] = digest
			}
			if complete {
				proven = append(proven, event)
				for rel, digest := range digests {
					batchDigests[rel] = digest
				}
			}
		}
		// Partial batches cannot be acknowledged, so do not fetch their evidence.
		if len(proven) == len(batch.Events) {
			c.proven = append(c.proven, proven...)
			for rel, digest := range batchDigests {
				c.digests[rel] = digest
			}
		}
	}
	return c.operation.Err()
}

func snapshotDigest(ctx context.Context, path string) (string, error) {
	f, err := transcript.Open(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(h, &captureReader{ctx: ctx, r: f})
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
