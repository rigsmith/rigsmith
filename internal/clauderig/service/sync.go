package service

import (
	"context"
	"fmt"

	"github.com/rigsmith/rigsmith/internal/agentrig/storelock"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/journal"
)

// FlushMode selects normal throttling, specific session groups, or all sessions.
// Paths are native transcript paths interpreted by the existing Claude engine.
type FlushMode uint8

const (
	FlushNormal FlushMode = iota
	FlushSelected
	FlushAll
)

type FlushIntent struct {
	Mode  FlushMode
	Paths []string
}

// SyncRequest supplies resolved configuration and paths. Config must be non-nil.
// Sync acquires store ownership, or borrows it from the operation context.
// ResolveFlush, when supplied, is called once instead of using Flush, after
// merge repair and identity capture. This keeps CLI stdin decoding at its old
// point in the workflow; a worker can provide a decoded Flush value directly.
type SyncRequest struct {
	Config                 *config.Config
	Machine                config.Machine
	StagingDir             string
	DryRun, AllowMergeTool bool
	Flush                  FlushIntent
	ResolveFlush           func() FlushIntent
	coverage               *manualCoverage
}

// SyncResult preserves the capture report and completed publication phases even
// on error. Dry runs capture and scan but leave Publication empty.
type SyncResult struct {
	Capture     *engine.Report
	Publication PublishResult
}

// Sync repairs, captures, scans, records metadata/journal entries and publishes.
// Debounce and terminal input remain caller responsibilities.
func (s Service) Sync(ctx context.Context, req SyncRequest) (result SyncResult, rerr error) {
	if req.Config == nil {
		return result, fmt.Errorf("sync requires a configuration")
	}
	ctx, release, err := storelock.Acquire(ctx, req.StagingDir, StoreWait)
	if err != nil {
		return result, err
	}
	defer release()
	result.Capture, rerr = s.Capture(ctx, req)
	if rerr != nil || req.DryRun {
		return result, rerr
	}
	if req.coverage != nil {
		if rerr = req.coverage.captured(req, result.Capture); rerr != nil {
			return result, rerr
		}
	}
	// Capture records its own failures. Publication failures remain a separate
	// journal entry, written after the attempted commit as in synchronous sync.
	defer func() {
		if rerr != nil {
			_ = journal.Append(req.StagingDir, journal.Failed(req.Machine.Name, journal.OpSync, rerr))
		}
	}()
	result.Publication, rerr = s.Publish(ctx, PublishRequest{
		StagingDir: req.StagingDir, Remote: req.Config.Remote, MachineName: req.Machine.Name,
		Retention: req.Config.Retention, AllowMergeTool: req.AllowMergeTool,
		RecordCommit: req.coverage != nil,
	})
	return result, rerr
}
