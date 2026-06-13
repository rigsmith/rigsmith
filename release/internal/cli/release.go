package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/changerig/commands"
	"github.com/rigsmith/release/internal/forge"
	"github.com/rigsmith/release/internal/pipeline"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newReleaseCmd builds the `release` command: a configurable step pipeline
// (.changeset/release.jsonc) around the built-in version/commit/publish/push/
// githubRelease steps, with hooks, captured variables, confirm gates, and
// secret masking. Ported from net-changesets' release orchestrator.
func newReleaseCmd() *cobra.Command {
	var (
		dryRun     bool
		only, skip []string
		from, to   string
		configPath string
		yes        bool
		gitOnly    bool
		ui, noUI   bool
	)
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Run the release pipeline (version → commit → publish → push → githubRelease)",
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
			// built-in version/publish steps to relrig itself, not the Node CLI.
			if cfg.Tool == "" {
				cfg.Tool = "relrig"
			}

			steps, err := pipeline.Resolve(cfg, pipeline.ResolveOptions{
				Only: only, Skip: skip, From: from, To: to,
			})
			if err != nil {
				return err
			}

			outRedirected := !term.IsTerminal(int(os.Stdout.Fd()))
			inRedirected := !term.IsTerminal(int(os.Stdin.Fd()))
			mode := pipeline.ResolveUIMode(ui, noUI, yes, outRedirected, inRedirected)

			masker := pipeline.NewSecretMasker()

			// githubRelease native handler: per-package forge releases. Output is
			// routed through the active reporter (so the live dashboard captures it
			// instead of writing raw to the terminal).
			fmode := forge.ParseMode(stepForge(cfg))
			if gitOnly {
				fmode = forge.None
			}
			newPipeline := func(reporter pipeline.Reporter, prompter pipeline.Prompter) *pipeline.Pipeline {
				handler := func() bool {
					pkgs, _, err := ws.Discover(cmd.Context())
					if err != nil {
						reporter.CommandOutput([]string{"discover: " + err.Error()})
						return false
					}
					ok, msg := forge.Run(pkgs, ws.Config, fmode, ws.Root, execForgeRunner(cmd), func(lines ...string) {
						reporter.CommandOutput(lines)
					})
					if msg != "" {
						reporter.CommandOutput([]string{msg})
					}
					return ok
				}
				return pipeline.New(pipeline.ExecRunner, reporter, masker, prompter, ws.Root,
					map[string]pipeline.NativeHandler{"githubRelease": handler})
			}

			fail := func() error {
				cmd.SilenceUsage = true
				cmd.SilenceErrors = true // the reporter already told the story
				return errors.New("release failed")
			}

			// Full TUI flow (interactive, rich, real run): the plan editor lets the
			// user toggle steps, then the live dashboard drives the run with inline
			// confirm gates. Everything else uses the sequential reporters.
			if mode.Interactive && mode.Rich && !dryRun {
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
	f.StringSliceVar(&only, "only", nil, "run only these steps (comma-separated)")
	f.StringSliceVar(&skip, "skip", nil, "skip these steps (comma-separated)")
	f.StringVar(&from, "from", "", "start at this step (resume point)")
	f.StringVar(&to, "to", "", "stop after this step")
	f.StringVar(&configPath, "config", "", "release config file (default .changeset/release.jsonc)")
	f.BoolVarP(&yes, "yes", "y", false, "approve all confirm gates (non-interactive)")
	f.BoolVar(&gitOnly, "git-only", false, "skip forge (GitHub) releases; tags only")
	f.BoolVar(&ui, "ui", false, "force the rich reporter even when piped")
	f.BoolVar(&noUI, "no-ui", false, "force the plain reporter")
	return cmd
}

// stepForge reads the githubRelease step's forge mode from the config.
func stepForge(cfg *pipeline.Config) string {
	if s, ok := cfg.Steps["githubRelease"]; ok && s != nil {
		return s.Forge
	}
	return ""
}

// ttyPrompter asks a confirm gate on the terminal.
type ttyPrompter struct{}

func (ttyPrompter) Confirm(message string) bool {
	ok := false
	err := huh.NewConfirm().Title(message).Value(&ok).Run()
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
