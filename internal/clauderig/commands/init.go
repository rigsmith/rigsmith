package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
	"github.com/spf13/cobra"
)

// NewInitCmd builds the `init` command — the first-run wizard. It captures this
// machine's name, the (always-private) sync remote, whether to sync the Desktop
// root, and whether to install hooks, then writes config.json. --yes runs
// non-interactively from flags. The remote is ALWAYS verified private via gh.
func NewInitCmd() *cobra.Command {
	var remote, name string
	installHooks, syncDesktop, yes := true, true, false
	alwaysPrune := false

	cmd := &cobra.Command{
		Use:   "init",
		Short: "First-run wizard: configure remote, machine identity, roots, and hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			me := config.Detect("")
			if name == "" {
				name, _ = os.Hostname()
			}
			me.Name = name

			existingRemote := ""
			if existing, err := config.LoadOrDefault(); err == nil {
				existingRemote = existing.Remote
			}

			if yes {
				if remote == "" {
					remote = existingRemote
				}
				if remote != "" {
					if err := ghrepo.EnsurePrivate(ctx, remote); err != nil {
						return err
					}
				}
			} else {
				// Two pages so the prompts aren't all crammed onto one screen: the
				// machine name on its own, then the three yes/no choices together.
				form := huh.NewForm(
					huh.NewGroup(
						huh.NewInput().Title("This machine's name").Value(&me.Name),
					),
					huh.NewGroup(
						huh.NewConfirm().Title("Sync the Desktop/Cowork root too?").Value(&syncDesktop),
						huh.NewConfirm().Title("Install Claude Code hooks (auto pull on start, sync on stop)?").Value(&installHooks),
						huh.NewConfirm().Title("On restore, prune config files (skills/commands/agents/plans) deleted elsewhere?").Value(&alwaysPrune),
					),
				).WithTheme(brand.Theme(brand.AccentClaude)).WithKeyMap(huhEscKeyMap())
				if err := form.Run(); err != nil {
					return cancelOrErr(out, err)
				}
				r, err := chooseRemote(ctx, out, existingRemote)
				if err != nil {
					return cancelOrErr(out, err)
				}
				remote = r
			}

			cfg := config.Default()
			cfg.Remote = remote
			cfg.AlwaysPrune = alwaysPrune
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
				// init wires the global sync hooks (pull/sync); the per-repo guard is
				// opt-in via `clauderig project install` inside a repo.
				if path, err := settingsPath(); err == nil {
					if _, err := hooks.Install(path, hooks.SyncPlans()); err == nil {
						fmt.Fprintln(out, OkStyle.Render("✓ Claude Code sync hooks installed"))
					}
				}
			}
			if remote == "" {
				fmt.Fprintln(out, WarnStyle.Render("\n  no remote set — sync will commit locally only; run `clauderig config set remote <url>` later"))
			}
			fmt.Fprintln(out, DimStyle.Render("\n  next: clauderig sync"))
			return nil
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "private GitHub/GitLab repo URL (verified via gh/glab or GITHUB_TOKEN/GITLAB_TOKEN)")
	cmd.Flags().StringVar(&name, "name", "", "this machine's name (default: hostname)")
	cmd.Flags().BoolVar(&installHooks, "hooks", true, "install Claude Code hooks")
	cmd.Flags().BoolVar(&syncDesktop, "desktop", true, "sync the Desktop/Cowork root")
	cmd.Flags().BoolVar(&alwaysPrune, "prune", false, "prune stale config files on every restore by default")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "non-interactive: use flags/defaults, no prompts")
	return cmd
}

// cancelOrErr maps a huh abort (esc/ctrl+c) to a clean cancel — a dim note and
// a nil error so the wizard exits 0 instead of printing a red error box. Any
// other error passes through unchanged.
func cancelOrErr(out io.Writer, err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Fprintln(out, DimStyle.Render("init cancelled"))
		return nil
	}
	return err
}

// chooseRemote runs the interactive repo picker: create a new private repo via gh,
// or supply an existing one (verified private). Returns "" (local-only) when gh
// isn't available — the only way to skip the private-repo requirement is to have
// no remote at all.
func chooseRemote(ctx context.Context, out io.Writer, defaultURL string) (string, error) {
	if !ghrepo.Available() {
		fmt.Fprintln(out, WarnStyle.Render(
			"GitHub CLI (gh) not found — it's required to guarantee a private repo. Skipping remote for now."))
		return "", nil
	}

	mode := "create"
	if defaultURL != "" {
		mode = "existing"
	}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Sync repository (must be private)").
			Options(
				huh.NewOption("Create a new private GitHub repo with gh", "create"),
				huh.NewOption("Use an existing private GitHub repo", "existing"),
			).Value(&mode),
	)).WithTheme(brand.Theme(brand.AccentClaude)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
		return "", err
	}

	if mode == "create" {
		repoName := "claude-sync"
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("New private repo name").Value(&repoName),
		)).WithTheme(brand.Theme(brand.AccentClaude)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
			return "", err
		}
		url, err := ghrepo.CreatePrivate(ctx, repoName)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(out, "%s created private repo %s\n", OkStyle.Render("✓"), url)
		return url, nil
	}

	url := defaultURL
	if err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Private GitHub repo URL").
			Placeholder("git@github.com:you/claude-sync.git").Value(&url),
	)).WithTheme(brand.Theme(brand.AccentClaude)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
		return "", err
	}
	if err := ghrepo.EnsurePrivate(ctx, url); err != nil {
		return "", err
	}
	return url, nil
}
