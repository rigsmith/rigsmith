// Package confkit holds the configuration mechanics shared across the rigsmith
// family — the comment-preserving JSONC config Writer and the truthy-env helper.
//
// It deliberately stays type-agnostic: each tool keeps its own typed config
// schema (rig's .rig.json, the changeset config.json, clauderig's config.json)
// and merge rules, and reaches here only for the file-level write splice and the
// "is this env var on" check, so those behave identically everywhere.
//
// Root discovery is intentionally NOT here: the tools' walk-up finders have
// genuinely different precedence (rig: .rig.json > workspace manifest > .git;
// changeset: .changeset > .git), so a shared abstraction would obscure rather
// than dedupe. confkit covers only the mechanics that are identical by design.
//
// Zero external dependencies (core/jsonc + stdlib), preserving core's portable,
// stdlib-only invariant.
package confkit

import (
	"os"
	"strings"
)

// Truthy reports whether the named environment variable is set to a truthy
// value: 1, true, yes, or on (case-insensitive). This is the one accepted
// spelling of an on/off env toggle across the family (e.g. CLAUDERIG_ALLOW_MAIN).
func Truthy(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
