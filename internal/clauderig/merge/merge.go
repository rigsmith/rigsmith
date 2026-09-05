// Package merge encodes clauderig's conflict-resolution policies — the
// mechanical answers to the conflicts a diverged sync repo actually produces.
//
// It exists because the 2026-08-07 Air/Pro divergence was resolved by hand over
// an afternoon, and every single conflict turned out to have a correct answer
// that needed no judgment: newest timestamp wins, union the memory index, take
// the superset of an append-only transcript, merge the manifest per key. None
// of them needed a human — they needed a button. Full analysis in
// docs/CLAUDERIG-MERGE-POLICIES.md.
//
// The package is pure: it takes the three sides of a conflict as bytes and
// returns merged bytes. All git lives in the command layer, so every policy is
// testable from a literal.
//
// # The hazard this is built around
//
// The tempting resolution is whole-file `git checkout --ours`. It is wrong for
// every structured file here. Git auto-merges most hunks and conflicts on one;
// taking the whole file discards the other machine's auto-merged content
// silently — which is how a hand-merge nearly dropped a machine's entire
// projects map. Every policy below merges *per key* or *per line*; none of them
// ever picks a side wholesale unless the file is genuinely a single value.
package merge

import (
	"bytes"
	"path"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// Sides are the three versions git records for a conflicted file: the common
// ancestor and each branch's version. Base is nil when the file was added
// independently on both sides.
type Sides struct {
	Path   string // slash-relative path within the repo
	Base   []byte // stage 1
	Ours   []byte // stage 2 — this machine
	Theirs []byte // stage 3 — the remote
}

// Result is a resolved file plus the ledger entry describing how.
type Result struct {
	Content []byte
	// Policy names the rule that ran, for the ledger the UI renders.
	Policy string
	// Detail is one line saying what it actually did — "kept 12 local entries,
	// added 3 from the remote". A merge that can't explain itself is magic, and
	// magic is what makes people distrust the button.
	Detail string
}

// policy resolves one class of file.
type policy struct {
	name  string
	match func(rel string) bool
	apply func(s Sides) (content []byte, detail string, err error)
}

// policies are tried in order; the first match wins. Ordered most specific
// first, so the named registry files beat the generic JSON rules.
var policies = []policy{
	// The two registries are matched at the repo root exactly, not by basename.
	// A synced project of someone's own containing a file with one of these
	// names would otherwise be run through a merger that understands a
	// different format entirely, and rewritten as one. MEMORY.md below is
	// basename-matched on purpose — those genuinely live all over the tree.
	{
		name:  "devices-union",
		match: func(rel string) bool { return rel == devicesFile },
		apply: mergeDevices,
	},
	{
		name:  "manifest-union",
		match: func(rel string) bool { return rel == manifestFile },
		apply: mergeManifest,
	},
	{
		name:  "memory-union",
		match: func(rel string) bool { return path.Base(rel) == "MEMORY.md" },
		apply: mergeMemory,
	},
	{
		name:  "transcript-superset",
		match: func(rel string) bool { return strings.HasSuffix(rel, ".jsonl") },
		apply: mergeJSONL,
	},
	{
		name:  "newest-timestamp",
		match: func(rel string) bool { return strings.HasSuffix(rel, ".json") },
		apply: mergeNewest,
	},
}

// Resolve applies the policy for s.Path. ok is false when no policy matches, or
// when the matched policy can't handle these particular contents — both mean
// "leave it conflicted for a human", which is the only honest answer for a file
// this package doesn't understand.
func Resolve(s Sides) (Result, bool) {
	// A file added or deleted on only one side is a delete/modify conflict.
	// Every policy below merges two present versions; resurrecting a file the
	// other machine deleted (or dropping one it kept) is a judgment call, not a
	// mechanical one.
	if s.Ours == nil || s.Theirs == nil {
		return Result{}, false
	}
	if transcript.IsIndex(s.Ours) || transcript.IsIndex(s.Theirs) {
		if bytes.Equal(s.Ours, s.Theirs) {
			return Result{Content: s.Ours, Policy: "chunk-index-identical", Detail: "identical chunk snapshots"}, true
		}
		return Result{}, false
	}
	for _, p := range policies {
		if !p.match(s.Path) {
			continue
		}
		content, detail, err := p.apply(s)
		if err != nil {
			return Result{}, false
		}
		return Result{Content: content, Policy: p.name, Detail: detail}, true
	}
	return Result{}, false
}

// PolicyFor names the policy that would handle rel, or "" for none. The UI uses
// it to say in advance what Resolve will do.
func PolicyFor(rel string) string {
	for _, p := range policies {
		if p.match(rel) {
			return p.name
		}
	}
	return ""
}
