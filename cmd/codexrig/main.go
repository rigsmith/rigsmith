// Command codexrig syncs your Codex CLI environment (config, instructions,
// prompts, and session rollouts) across machines via your own git remote,
// correcting paths across OSes on restore, and runs several Codex logins side by
// side on one machine. The fifth rig: a single statically-linked Go binary, zero
// runtime deps, installable by curl|sh / Homebrew / Scoop on any machine — the
// same north-star as rig / shiprig / changerig / clauderig.
//
// It is clauderig's sibling, not its generalisation: the two hard problems are
// the same (cross-OS path correction, not leaking secrets) but Codex's layered
// TOML config, isolated CODEX_HOME accounts and dated rollout files are its own.
// See docs/CODEXRIG-DESIGN.md.
package main

import (
	"context"
	"os"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/fang"
	"github.com/rigsmith/rigsmith/internal/codexrig/commands"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(context.Background()); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root := commands.NewRootCmd(version)

	// Bare, interactive `codexrig` lands on the dashboard — a discoverable hub
	// with the next step in view. Off a TTY (or with any verb/flag) the normal
	// help/dispatch stands, so hooks, scripts, and `codexrig -h` are unchanged.
	// Routing through the `ui` verb (not a root RunE) keeps cobra's
	// unknown-command errors intact.
	if len(os.Args) == 1 && commands.Interactive() {
		root.SetArgs([]string{"ui"})
	}
	return fang.Execute(ctx, root, fang.WithVersion(version), fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentCodex)), fang.WithBanner(brand.CodexBanner))
}
