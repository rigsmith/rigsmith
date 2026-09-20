// Command brewrig keeps several machines on the same Homebrew software — and
// the same versions of it — by publishing each machine's deliberately-installed
// packages to your own private git repo and installing whatever a machine is
// missing. The sixth rig: a single statically-linked Go binary, zero runtime
// deps, installable by curl|sh / Homebrew on any machine Homebrew runs on — the
// same north-star as rig / shiprig / changerig / clauderig / codexrig.
//
// It is not a shared Brewfile with a nicer face. A single merged file is
// last-writer-wins and cannot tell "not installed there yet" apart from
// "deliberately removed there", which is the distinction that decides whether
// software gets uninstalled. See docs/BREWRIG-DESIGN.md.
//
// Unlike its siblings it ships for darwin and linux only, because Homebrew does
// not run on Windows.
package main

import (
	"context"
	"os"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/fang"
	"github.com/rigsmith/rigsmith/internal/brewrig/commands"
)

// version is stamped at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(context.Background()); err != nil {
		os.Exit(1)
	}
	// `doctor` reports its own findings and returns no error, so that fang does
	// not print an empty ERROR block under a report that already said
	// everything. Its exit code comes from here instead.
	if commands.CheckFailed() {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root := commands.NewRootCmd(version)

	// Bare, interactive `brewrig` lands on the dashboard — a discoverable hub
	// with the next step in view. Off a TTY (or with any verb/flag) the normal
	// help/dispatch stands, so scripts and `brewrig -h` are unchanged. Routing
	// through the `ui` verb (not a root RunE) keeps cobra's unknown-command
	// errors intact.
	if len(os.Args) == 1 && commands.Interactive() {
		root.SetArgs([]string{"ui"})
	}
	return fang.Execute(ctx, root, fang.WithVersion(version), fang.WithColorSchemeFunc(brand.ColorSchemeFunc(brand.AccentBrew)), fang.WithBanner(brand.BrewBanner))
}
