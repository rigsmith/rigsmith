// Package service runs ClaudeRig application workflows without Cobra or terminal
// rendering. It initially retains Claude's existing policy and backup formats;
// vendor-neutral mechanics and adapters are separate extractions.
//
// Services coordinate staging operations through the shared store lock. Pass the
// operation context to sequential nested services to reuse that ownership.
package service

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/rigsmith/rigsmith/core/commandrun"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/mergepolicy"
	"github.com/rigsmith/rigsmith/internal/clauderig/redact"
)

// Interactive merge tools cannot obey the finite-input supervised runner contract.
var ErrSupervisedMergeToolUnavailable = errors.New("supervised canonical workflows do not support interactive merge tools")

type canonicalRunnerKey struct{}

func requireCanonicalRunner(ctx context.Context, allowMergeTool bool) error {
	if commandrun.Configured(ctx) && ctx.Value(canonicalRunnerKey{}) != true {
		return errors.New("canonical service requires its own command runner")
	}
	if process.SupervisionEnabled(ctx) && allowMergeTool {
		return ErrSupervisedMergeToolUnavailable
	}
	if process.SupervisionEnabled(ctx) && runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return errors.New("canonical command supervision is unsupported on this platform")
	}
	if err := process.CheckSupervisorLease(ctx); err != nil {
		return err
	}
	return commandrun.Check(ctx)
}

// Called only after acquiring staging. Nested canonical services reuse the
// selection and its failure state, binding command supervision to the active
// staging lease rather than an unrelated caller-provided private-store lease.
func canonicalContext(ctx context.Context) context.Context {
	if !process.SupervisionEnabled(ctx) {
		return ctx
	}
	ctx = process.WithSupervisorLease(ctx, ctx)
	if commandrun.Configured(ctx) {
		return ctx
	}
	return context.WithValue(commandrun.WithRunner(ctx, process.Run), canonicalRunnerKey{}, true)
}

func canonicalResult(ctx context.Context, err error) error {
	if failure := commandrun.Check(ctx); failure != nil && !errors.Is(err, failure) {
		return errors.Join(err, failure)
	}
	return err
}

// StoreWait bounds contention for requested operations; SessionStart pull tries
// once. The lock remains owned for the entire operation after acquisition.
const StoreWait = 15 * time.Second

// Service delivers synchronous progress to an optional observer. Observers must
// not mutate the store, event payloads or reenter a workflow. A nil observer
// discards progress. Now defaults to time.Now for device metadata, maintenance
// and queue retry deadlines. A supplied clock must return a nonzero time.
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
