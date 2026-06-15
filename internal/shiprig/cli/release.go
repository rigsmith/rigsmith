package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
	"github.com/rigsmith/rigsmith/internal/shiprig/forge"
	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newReleaseCmd builds the `release` command: a configurable step pipeline
// (.changeset/release.jsonc) around the built-in version/commit/build/publish/
// push/release steps, with hooks, captured variables, confirm gates, and
// secret masking. The `build` step produces each package's distributable
// artifacts (the ecosystem Artifacts method) into dist/ before publish; the
// release step attaches the Attach:true ones. Ported from net-changesets.
func newReleaseCmd() *cobra.Command {
	var (
		dryRun     bool
		dryBuild   bool
		only, skip []string
		from, to   string
		configPath string
		yes        bool
		gitOnly    bool
		ui, noUI   bool
	)
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Run the release pipeline (version → commit → build → publish → tag → push → release)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := commands.Open()
			if err != nil {
				return err
			}

			path := configPath
			if path == "" {
				path = filepath.Join(ws.ChangesetDir, "release.jsonc")
			}
			cfg, err := pipeline.LoadConfig(path)
			if err != nil {
				return err
			}
			// The Go binaries are their own changeset engine — default the
			// built-in version/publish steps to shiprig itself, not the Node CLI.
			if cfg.Tool == "" {
				cfg.Tool = "shiprig"
			}

			steps, err := pipeline.Resolve(cfg, pipeline.ResolveOptions{
				Only: only, Skip: skip, From: from, To: to, DryBuild: dryBuild,
			})
			if err != nil {
				return err
			}

			if dryBuild {
				// A dry-build only builds — no registry/forge side effects. Drop the
				// global hooks and captured vars so it can't trigger them (e.g. an OTP
				// prompt), and require an enabled build step so it actually does work.
				cfg.Hooks = nil
				cfg.Vars = nil
				if !hasEnabledStep(steps, "build") {
					return fmt.Errorf("nothing to dry-build: no enabled 'build' step in the release order")
				}
			}

			outRedirected := !term.IsTerminal(int(os.Stdout.Fd()))
			inRedirected := !term.IsTerminal(int(os.Stdin.Fd()))
			mode := pipeline.ResolveUIMode(ui, noUI, yes, outRedirected, inRedirected)

			masker := pipeline.NewSecretMasker()

			// release native handler: per-package forge releases. Output is
			// routed through the active reporter (so the live dashboard captures it
			// instead of writing raw to the terminal).
			fmode := forge.ParseMode(stepForge(cfg))
			if gitOnly {
				fmode = forge.None
			}
			// built is shared between the `build` and `release` handlers in a
			// single run: build produces dist/ and records each package's artifacts;
			// the forge step attaches the Attach:true ones to the release.
			built := map[string][]plugin.Artifact{}
			distDir := filepath.Join(ws.Root, "dist")
			newPipeline := func(reporter pipeline.Reporter, prompter pipeline.Prompter) *pipeline.Pipeline {
				out := func(lines ...string) { reporter.CommandOutput(lines) }

				buildHandler := func() bool {
					pkgs, ecoOf, err := ws.Discover(cmd.Context())
					if err != nil {
						out("discover: " + err.Error())
						return false
					}
					for k := range built {
						delete(built, k) // fresh each run
					}
					for _, pkg := range pkgs {
						eco, ok := ws.Registry.Get(ecoOf[pkg.Name])
						if !ok {
							continue
						}
						resp, err := eco.Artifacts(cmd.Context(), plugin.ArtifactsRequest{
							RepoRoot: ws.Root, Package: pkg, OutputDir: distDir, Snapshot: dryBuild,
						})
						if err != nil {
							out("build " + pkg.Name + ": " + err.Error())
							return false
						}
						if resp.Built {
							built[pkg.Name] = resp.Artifacts
						}
						if resp.Message != "" {
							out("build " + pkg.Name + ": " + resp.Message)
						}
					}
					return true
				}

				releaseHandler := func() bool {
					pkgs, ecoOf, err := ws.Discover(cmd.Context())
					if err != nil {
						out("discover: " + err.Error())
						return false
					}
					ok, msg := forge.Run(pkgs, ecoOf, attachPaths(built), ws.Config, fmode, ws.Root, execForgeRunner(cmd), out)
					if msg != "" {
						out(msg)
					}
					return ok
				}

				return pipeline.New(pipeline.ExecRunner, reporter, masker, prompter, ws.Root,
					map[string]pipeline.NativeHandler{"build": buildHandler, "release": releaseHandler})
			}

			fail := func() error {
				cmd.SilenceUsage = true
				cmd.SilenceErrors = true // the reporter already told the story
				return errors.New("release failed")
			}

			// Full TUI flow (interactive, rich, real run): the plan editor lets the
			// user toggle steps, then the live dashboard drives the run with inline
			// confirm gates. A dry-build only builds (nothing to gate), so it takes
			// the straight sequential path like --dry-run.
			if mode.Interactive && mode.Rich && !dryRun && !dryBuild {
				chosen, proceed := interactiveChooser{
					in: cmd.InOrStdin(), out: cmd.OutOrStdout(), masker: masker,
				}.Choose(steps)
				if !proceed {
					fmt.Fprintln(cmd.OutOrStdout(), "Release cancelled.")
					return nil
				}
				ok, err := runDashboard(chosen, cfg, cfg.Tool,
					cmd.InOrStdin(), cmd.OutOrStdout(), masker, newPipeline)
				if err != nil {
					return err
				}
				if !ok {
					return fail()
				}
				return nil
			}

			// Sequential path (CI, --yes, piped, --no-ui, or --dry-run).
			var reporter pipeline.Reporter
			if mode.Rich {
				reporter = newRichReporter(cmd.OutOrStdout(), masker, cfg.Tool)
			} else {
				reporter = pipeline.NewPlainReporter(cmd.OutOrStdout(), masker, cfg.Tool)
			}
			var prompter pipeline.Prompter
			if mode.Interactive {
				prompter = ttyPrompter{}
			} else {
				// Non-interactive: --yes approves gates; otherwise a gate safely
				// stops the release rather than guessing.
				prompter = pipeline.FixedPrompter{Answer: yes}
			}
			if !newPipeline(reporter, prompter).Run(steps, cfg, dryRun) {
				return fail()
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&dryRun, "dry-run", "n", false, "print the plan without executing anything")
	f.BoolVar(&dryBuild, "dry-build", false, "build the release's artifacts locally (snapshot) and publish nothing — runs only the build step")
	f.StringSliceVar(&only, "only", nil, "run only these steps (comma-separated)")
	f.StringSliceVar(&skip, "skip", nil, "skip these steps (comma-separated)")
	f.StringVar(&from, "from", "", "start at this step (resume point)")
	f.StringVar(&to, "to", "", "stop after this step")
	f.StringVar(&configPath, "config", "", "release config file (default .changeset/release.jsonc)")
	f.BoolVarP(&yes, "yes", "y", false, "approve all confirm gates (non-interactive)")
	f.BoolVar(&gitOnly, "git-only", false, "skip forge (GitHub) releases; tags only")
	f.BoolVar(&ui, "ui", false, "force the rich reporter even when piped")
	f.BoolVar(&noUI, "no-ui", false, "force the plain reporter")
	// --dry-build, --dry-run, and the step-selection flags are three distinct
	// modes that don't compose: dry-run is plan-only, dry-build forces a build-only
	// plan, and --only/--skip/--from/--to hand-pick steps. Keep them exclusive so a
	// combination can't produce a confusing or no-op run.
	for _, mutex := range []string{"dry-run", "only", "skip", "from", "to"} {
		cmd.MarkFlagsMutuallyExclusive("dry-build", mutex)
	}
	return cmd
}

// hasEnabledStep reports whether the resolved plan contains an enabled step with
// the given name.
func hasEnabledStep(steps []pipeline.ResolvedStep, name string) bool {
	for _, s := range steps {
		if s.Name == name && s.Enabled() {
			return true
		}
	}
	return false
}

// stepForge reads the forge mode from the `release` step config.
func stepForge(cfg *pipeline.Config) string {
	if s, ok := cfg.Steps["release"]; ok && s != nil {
		return s.Forge
	}
	return ""
}

// attachPaths flattens the build's artifacts into per-package file paths for the
// ones marked Attach (binaries/archives), dropping registry packages (.tgz/
// .nupkg/.crate) that ship to their registry rather than the forge release.
func attachPaths(built map[string][]plugin.Artifact) map[string][]string {
	out := map[string][]string{}
	for name, arts := range built {
		for _, a := range arts {
			if a.Attach {
				out[name] = append(out[name], a.Path)
			}
		}
	}
	return out
}

// ttyPrompter asks a confirm gate on the terminal.
type ttyPrompter struct{}

func (ttyPrompter) Confirm(message string) bool {
	ok := false
	err := huh.NewConfirm().Title(message).Value(&ok).WithTheme(brand.Theme(brand.AccentShip)).Run()
	if err != nil {
		return false // treat an aborted prompt as a decline
	}
	return ok
}

// execForgeRunner adapts os/exec to the forge.Runner seam.
func execForgeRunner(cmd *cobra.Command) forge.Runner {
	return func(dir, name string, args ...string) (string, error) {
		c := exec.CommandContext(cmd.Context(), name, args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		return string(out), err
	}
}
