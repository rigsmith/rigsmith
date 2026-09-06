// Package publication coordinates Git publication without selecting vendor formats.
// Callers own store locking and supply mandatory byte, audit and conflict policies.
package publication

import (
	"context"
	"fmt"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// Policy keeps audits and native merge handling outside the shared workflow.
// Resolve may emit vendor-specific resolution details before returning unresolved paths.
// All callbacks are required; missing audits must never silently permit publication.
type Policy struct {
	Init              func(context.Context, string) (*gitrepo.Repo, error)
	Prepare, Validate func(context.Context, string) error
	Audit             func(string) error
	Resolve           func(context.Context, *gitrepo.Repo) ([]string, error)
	HumanRequired     func([]string) error
}

func (p Policy) check() error {
	if p.Init == nil || p.Prepare == nil || p.Validate == nil || p.Audit == nil || p.Resolve == nil || p.HumanRequired == nil {
		return fmt.Errorf("publication: incomplete policy")
	}
	return nil
}

// Plan describes branch selection, commit labels and maintenance policies.
// PushRetries is the number of reconciliations after the initial push fails.
type Plan struct {
	RemoteName, Branch, SnapshotMessage string
	PushRetries                         int
	History                             *HistoryPlan
	Retention                           Retention
}

// HistoryPlan selects a best-effort independent history branch and Git pathspecs.
type HistoryPlan struct {
	Branch                       string
	Paths                        []string
	CommitMessage, SquashMessage string
	MaxCommits                   int
}

// Retention supplies resolved retention settings and the vendor's commit label.
type Retention struct {
	FloorBytes   int64
	SquashFactor float64
	KeepDays     int
	FoldMessage  func(time.Time) string
}

// Workflow does not acquire locks. Observers must not mutate or reenter the store.
type Workflow struct {
	Policy  Policy
	Observe func(Event)
	Now     func() time.Time
}

func (w Workflow) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}
func (w Workflow) emit(e Event) {
	if w.Observe != nil {
		w.Observe(e)
	}
}

type Event interface{ publicationEvent() }
type event struct{}

func (event) publicationEvent() {}

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
type MergePending struct{ event }
type MergeToolStarting struct {
	event
	Count int
}
