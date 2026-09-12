package allowlist

import "github.com/rigsmith/rigsmith/internal/agentrig/allowlist"

// The rule ENGINE moved to internal/agentrig/allowlist when codexrig arrived:
// "which files may travel" is a judgement about one CLI's layout, but
// longest-match-wins, default-deny and prune-on-any-depth-exclude are properties
// the rigs must not disagree about, and a second copy would drift.
//
// These aliases keep this package's surface exactly what it was, so every
// clauderig call site — allowlist.For, allowlist.Walk, allowlist.List — reads
// the same as before the move. Nothing here is new behaviour.
type (
	Action = allowlist.Action
	Rule   = allowlist.Rule
	List   = allowlist.List
	Link   = allowlist.Link
)

const (
	Exclude = allowlist.Exclude
	Include = allowlist.Include

	anyDepth = allowlist.AnyDepth
)

// Walk returns the sorted, '/'-separated relative paths of every file under root
// that the list includes, plus the directory symlinks worth recording.
var Walk = allowlist.Walk

// inc and exc build the rules in defaults.go. They stay lowercase here because
// this file is a rule SET, and a reader of a 130-line rule list should not have
// to think about which package a builder came from.
func inc(p string) Rule { return allowlist.Inc(p) }
func exc(p string) Rule { return allowlist.Exc(p) }
