package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/hooks"
	"github.com/spf13/cobra"
)

// NewInitCmd builds the `init` command — the first-run wizard. Interactively
// (huh) or via flags it captures this machine's name, the git remote, whether to
// sync the Desktop root, and whether to install hooks, then writes config.json.
// --yes runs non-interactively from flags/defaults (for scripting and fresh
// machines without a TTY).
func NewInitCmd() *cobra.Command {
	var remote, name string
	installHooks, syncDesktop, yes := true, true, false

	cmd := &cobra.Command{
		Use:   "init",
		Short: "First-run wizard: configure remote, machine identity, roots, and hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			me := config.Detect("")
			if name == "" {
				name, _ = os.Hostname()
			}
			me.Name = name

			if existing, err := config.LoadOrDefault(); err == nil && existing.Remote != "" && remote == "" {
				remote = existing.Remote // reconfigure keeps the known remote as default
			}

			if !yes {
				form := huh.NewForm(huh.NewGroup(
					huh.NewInput().Title("This machine's name").Value(&me.Name),
					huh.NewInput().Title("Git remote URL (a private repo)").
						Placeholder("git@github.com:you/claude-sync.git").Value(&remote),
					huh.NewConfirm().Title("Sync the Desktop/Cowork root too?").Value(&syncDesktop),
					huh.NewConfirm().Title("Install Claude Code hooks (auto pull on start, sync on stop)?").Value(&installHooks),
				))
				if err := form.Run(); err != nil {
					return err
				}
			}

			cfg := config.Default()
			cfg.Remote = remote
			if !syncDesktop {
				for i := range cfg.Roots {
					if cfg.Roots[i].ID == "desktop" {
						cfg.Roots[i].Enabled = false
					}
				}
			}
			cfg.Machines[me.Name] = me

			dir, err := config.Dir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			if err := config.Save(cfg, dir); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s wrote %s\n", OkStyle.Render("✓"), filepath.Join(dir, "config.json"))

			if installHooks {
				if path, err := settingsPath(); err == nil {
					if _, err := hooks.Install(path); err == nil {
						fmt.Fprintln(out, OkStyle.Render("✓ Claude Code hooks installed"))
					}
				}
			}
			if remote == "" {
				fmt.Fprintln(out, WarnStyle.Render("\n  no remote set — sync will commit locally only"))
			}
			fmt.Fprintln(out, DimStyle.Render("\n  next: clauderig sync"))
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "git remote URL")
	cmd.Flags().StringVar(&name, "name", "", "this machine's name (default: hostname)")
	cmd.Flags().BoolVar(&installHooks, "hooks", true, "install Claude Code hooks")
	cmd.Flags().BoolVar(&syncDesktop, "desktop", true, "sync the Desktop/Cowork root")
	cmd.Flags().BoolVar(&yes, "yes", false, "non-interactive: use flags/defaults, no prompts")
	return cmd
}
