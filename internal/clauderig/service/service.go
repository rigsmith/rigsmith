// Package service runs ClaudeRig application workflows without Cobra or terminal
// rendering. It initially retains Claude's existing policy and backup formats;
// vendor-neutral mechanics and adapters are separate extractions.
//
// Services coordinate staging operations through the shared store lock. Pass the
// operation context to sequential nested services to reuse that ownership.
package service

import (
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

// StoreWait bounds contention for requested operations; SessionStart pull tries
// once. The lock remains owned for the entire operation after acquisition.
const StoreWait = 15 * time.Second

// Service delivers synchronous progress to an optional observer. Observers must
// not mutate the store, event payloads or reenter a workflow. A nil observer
// discards progress. Now defaults to time.Now for device metadata and maintenance.
type Service struct {
	Observe func(Event)
	Now     func() time.Time
	// ReadIdentity defaults to account.LiveIdentity and is observed once per sync.
	ReadIdentity func() (Identity, error)
}

func (s Service) emit(e Event) {
	if s.Observe != nil {
		s.Observe(e)
	}
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Event is a typed progress notification, not formatted terminal output.
type Event interface{ serviceEvent() }
type event struct{}

func (event) serviceEvent() {}

type MergePending struct{ event }
type ConflictsResolved struct {
	event
	Resolutions []mergepolicy.Resolution
}
type MergeToolStarting struct {
	event
	Count int
}
type MergeRepairFailed struct {
	event
	Err error
}

// Published is emitted before history maintenance, preserving the CLI's existing
// progress order. A subsequent maintenance error can still fail the operation.
type Published struct {
	event
	Result    PublishResult
	LocalOnly bool
}
type Repacking struct {
	event
	GitBytes int64
	Factor   float64
}
type HistoryFolded struct {
	event
	Count, KeepDays int
	Cutoff          time.Time
}
type CloneSkipped struct {
	event
	Err error
}
type PullFailed struct {
	event
	Err error
}
type AutoRestored struct {
	event
	DesktopSessions int
}

type SyncStarted struct{ event }
type IdentityRejected struct {
	event
	Finding redact.Finding
}
type Captured struct {
	event
	Report *engine.Report
}
type CaptureFailed struct {
	event
	Findings []redact.Finding
	Err      error
}
type DryRunStaged struct{ event }
type DeviceUnregistered struct{ event }
