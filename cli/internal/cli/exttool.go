package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/cli/internal/config"
	"github.com/spf13/cobra"
)

// extTool describes an optional external tool rig uses beyond the core ecosystem
// toolchain (dotnet/go/the node package manager/cargo). It centralizes the
// detect → (config) → prompt → install → resolve lifecycle so every such tool
// behaves the same way — the ReportGenerator pattern, generalized.
//
// The zero hooks cover a simple "binary on PATH, installed by a command" tool
// (the cargo subcommands); ReportGenerator overrides resolve/canInstall/readMode/
// configKey for its multi-strategy resolution and fetch-on-use install.
type extTool struct {
	// name is the canonical tool name: the PATH binary the default resolver
	// looks for, the `tools.<name>` config key, and the label in messages.
	name string
	// why is a short description of what rig needs it for, shown in the prompt
	// and the require() error (e.g. "renders Rust coverage for `rig coverage`").
	why string
	// install is the command that installs the tool (e.g.
	// {"cargo","install","cargo-llvm-cov"}); nil when rig can't install it
	// (ReportGenerator: fetched on use via dnx; dnx: ships with the SDK).
	install []string
	// hint is extra guidance shown in the require() error when the tool is
	// unavailable and rig can't install it (e.g. a download URL). Optional.
	hint string
	// openURL, when set, is offered for opening in a browser when the tool is
	// missing and rig can't install it (e.g. an SDK download page).
	openURL string

	// resolve reports how to invoke the tool when present, returning the argv
	// prefix. nil → the default: exec.LookPath(name) → {name}. mode is the
	// resolved config mode, for tools whose invocation depends on it
	// (ReportGenerator's install-mode dnx invoker).
	resolve func(root, mode string) ([]string, bool)
	// canInstall reports whether an install is possible now. nil → the default:
	// len(install) > 0.
	canInstall func(root string) bool
	// readMode returns the configured mode from merged config. nil → the
	// default: cfg.Tools[name]. (ReportGenerator reads coverage.reportGenerator.)
	readMode func(cfg config.Config) string
	// configKey is the .rig.json path the chosen mode persists to. nil → the
	// default: {"tools", name}.
	configKey []string
}

// extTool config modes.
const (
	toolAuto    = "auto"    // default: use if present, offer to install on a TTY
	toolOff     = "off"     // never use / never ask
	toolInstall = "install" // acquire without asking
)

// ensure resolves the tool — installing or prompting as policy allows — and
// returns the argv prefix to invoke it. ok=false means it's unavailable and the
// caller should fall back. Under --dry-run it only resolves (never prompts or
// installs).
func (t extTool) ensure(cmd *cobra.Command, root string) ([]string, bool) {
	resolve := t.resolver()
	mode := t.mode(root)
	if inv, ok := resolve(root, mode); ok {
		return inv, true
	}
	if dryRun {
		return nil, false
	}
	switch mode {
	case toolOff:
		return nil, false
	case toolInstall:
		if t.installable(root) {
			t.runInstall(cmd, root)
			return resolve(root, mode)
		}
		return nil, false
	}
	// auto: offer to acquire it when we can and the terminal allows a prompt.
	if quiet || !interactive() {
		return nil, false
	}
	if !t.installable(root) {
		t.offerOpen(cmd) // nothing to install; at most open a download page
		return nil, false
	}
	switch t.prompt() {
	case extInstallNow:
		t.persist(cmd, root, toolInstall)
		t.runInstall(cmd, root)
		return resolve(root, toolInstall)
	case extNever:
		t.persist(cmd, root, toolOff)
		return nil, false
	default: // not now
		return nil, false
	}
}

// require is ensure for a tool with no fallback: it returns the invoker, or a
// guidance error when the tool is unavailable so the command fails cleanly (and
// CI catches it). Under --dry-run a miss is tolerated (nil, nil) so the caller
// can still print what it would run.
func (t extTool) require(cmd *cobra.Command, root string) ([]string, error) {
	if inv, ok := t.ensure(cmd, root); ok {
		return inv, nil
	}
	if dryRun {
		return nil, nil
	}
	return nil, t.unavailableErr()
}

// resolver is the configured resolve hook or the default PATH lookup.
func (t extTool) resolver() func(root, mode string) ([]string, bool) {
	if t.resolve != nil {
		return t.resolve
	}
	return func(_, _ string) ([]string, bool) {
		if p, err := exec.LookPath(t.name); err == nil {
			return []string{p}, true
		}
		return nil, false
	}
}

// available reports whether the tool is actually installed now — the auto-mode
// resolve (on PATH or a tool-manifest), not counting a fetch-on-use path. Used
// by `rig doctor` to report tool presence regardless of the configured mode.
func (t extTool) available(root string) bool {
	_, ok := t.resolver()(root, toolAuto)
	return ok
}

// installable reports whether rig can install the tool right now.
func (t extTool) installable(root string) bool {
	if t.canInstall != nil {
		return t.canInstall(root)
	}
	return len(t.install) > 0
}

// mode reads the configured mode (auto|off|install), defaulting to auto.
func (t extTool) mode(root string) string {
	cfg, _ := config.LoadMerged(root)
	raw := cfg.Tools[t.name]
	if t.readMode != nil {
		raw = t.readMode(cfg)
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case toolOff:
		return toolOff
	case toolInstall:
		return toolInstall
	default:
		return toolAuto
	}
}

// cfgKey is the persistence path for the chosen mode.
func (t extTool) cfgKey() []string {
	if t.configKey != nil {
		return t.configKey
	}
	return []string{"tools", t.name}
}

// persist records the chosen mode in the repo .rig.json (comment-preserving).
func (t extTool) persist(cmd *cobra.Command, root, mode string) {
	path := filepath.Join(root, config.FileName)
	if config.SetString(path, t.cfgKey(), mode) {
		fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render(
			fmt.Sprintf("set %s = %s in %s", strings.Join(t.cfgKey(), "."), mode, filepath.Base(path))))
	}
}

// runInstall runs the install command when there is one. ReportGenerator has
// none — it's fetched on use, so resolve in install mode yields the dnx invoker.
func (t extTool) runInstall(cmd *cobra.Command, root string) {
	if len(t.install) == 0 {
		return
	}
	if err := runCommand(cmd, root, t.install); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(fmt.Sprintf("installing %s failed: %v", t.name, err)))
	}
}

// unavailableErr is the guidance error require() returns when the tool can't be
// resolved: what it's for, plus how to install it or a hint.
func (t extTool) unavailableErr() error {
	switch {
	case len(t.install) > 0:
		return fmt.Errorf("%s is required (%s) — install it with: %s",
			t.name, t.why, strings.Join(t.install, " "))
	case t.hint != "":
		return fmt.Errorf("%s is required (%s) — %s", t.name, t.why, t.hint)
	default:
		return fmt.Errorf("%s is required (%s)", t.name, t.why)
	}
}

// offerOpen offers to open a download page for a tool rig can't install itself
// (e.g. an SDK). No-op when there's no URL.
func (t extTool) offerOpen(cmd *cobra.Command) {
	if t.openURL == "" {
		return
	}
	offerOpenURL(cmd, t.name+" isn't installed — "+t.why, t.openURL)
}

// offerOpenURL prompts (on a TTY) to open url in a browser, opening it on yes.
// Shared by extTool.offerOpen and the doctor .NET-SDK install offer.
func offerOpenURL(cmd *cobra.Command, title, url string) {
	var open bool
	c := huh.NewConfirm().
		Title(title).
		Description("Open " + url + " to install it?").
		Affirmative("Open").
		Negative("Not now").
		Value(&open)
	if err := runHuhConfirm(c); err == nil && open {
		openPath(cmd, url)
	}
}

// extPromptChoice is the answer to the "install <tool>?" prompt.
type extPromptChoice int

const (
	extInstallNow extPromptChoice = iota
	extNotNow
	extNever
)

// prompt shows the yes / not-now / never picker. Any error (Ctrl-C/esc) is
// treated as not-now.
func (t extTool) prompt() extPromptChoice {
	desc := "Download it on demand?"
	if len(t.install) > 0 {
		desc = "Run `" + strings.Join(t.install, " ") + "`?"
	}
	var choice extPromptChoice
	sel := huh.NewSelect[extPromptChoice]().
		Title(t.name+" isn't installed — "+t.why).
		Description(desc).
		Options(
			huh.NewOption("Yes — install it and use it from now on", extInstallNow),
			huh.NewOption("Not now", extNotNow),
			huh.NewOption("Never ask again", extNever),
		).
		Value(&choice)
	if err := runHuhSelect(sel); err != nil {
		return extNotNow
	}
	return choice
}

// The optional external tools rig knows how to acquire.
var (
	toolCargoLlvmCov = extTool{
		name:    "cargo-llvm-cov",
		why:     "renders Rust coverage for `rig coverage`",
		install: []string{"cargo", "install", "cargo-llvm-cov"},
	}
	toolCargoOutdated = extTool{
		name:    "cargo-outdated",
		why:     "lists outdated crates for `rig outdated`",
		install: []string{"cargo", "install", "cargo-outdated"},
	}
	toolCargoWatch = extTool{
		name:    "cargo-watch",
		why:     "powers `rig watch` for Rust",
		install: []string{"cargo", "install", "cargo-watch"},
	}
	// dnx ships with the .NET 10 SDK — rig can't install it, so it only guides /
	// offers the download page.
	toolDnx = extTool{
		name:    "dnx",
		why:     "runs .NET tools once for `rig dlx`",
		hint:    "dnx ships with the .NET 10 SDK",
		openURL: "https://dot.net/download",
	}
)
