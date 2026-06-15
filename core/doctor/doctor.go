// Package doctor is the shared health-check model behind every rig's `doctor`
// command. A doctor produces a set of checks, each reporting OK/Warn/Fail/Info
// with a detail and — when the issue is something the tool can repair — a Fix
// closure. The model is pure: it does no terminal I/O and pulls in nothing beyond
// the standard library, so it can live in core/ and be shared by rig, clauderig,
// changerig, and shiprig. Presentation and the fix-apply loop live in
// internal/doctorui; the per-tool checks live in each tool's own package.
package doctor

import "context"

// Status is a single check's verdict.
type Status int

const (
	OK   Status = iota // healthy
	Warn               // degraded but usable
	Fail               // broken
	Info               // neutral context, never a problem
)

// Result is one check's outcome. Fix is non-nil only when the tool can repair the
// issue itself (e.g. install a missing tool it owns the install command for);
// FixLabel is the one-line description shown in the fix selector. Hint is manual
// remediation shown when Fix is nil — the report-only path.
type Result struct {
	Name     string
	Status   Status
	Detail   string
	Hint     string                      // manual remediation when Fix is nil
	Fix      func(context.Context) error // nil ⇒ not auto-fixable (report-only)
	FixLabel string
}

// Section groups related checks under a heading.
type Section struct {
	Title   string
	Results []Result
}

// Counts tallies the report. fixable counts Warn/Fail results that carry a Fix.
func Counts(sections []Section) (fails, warns, fixable int) {
	for _, s := range sections {
		for _, r := range s.Results {
			switch r.Status {
			case Fail:
				fails++
			case Warn:
				warns++
			}
			if r.Fix != nil && (r.Status == Fail || r.Status == Warn) {
				fixable++
			}
		}
	}
	return fails, warns, fixable
}

// Fixable returns the repairable results, in report order.
func Fixable(sections []Section) []Result {
	var out []Result
	for _, s := range sections {
		for _, r := range s.Results {
			if r.Fix != nil && (r.Status == Fail || r.Status == Warn) {
				out = append(out, r)
			}
		}
	}
	return out
}
