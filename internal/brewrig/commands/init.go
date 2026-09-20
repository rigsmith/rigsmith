package commands

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/agentrig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/brewrig/brew"
	"github.com/rigsmith/rigsmith/internal/brewrig/config"
	"github.com/spf13/cobra"
)

// NewInitCmd sets brewrig up on this machine: name it, point it at a private
// repo, and take the first snapshot.
func NewInitCmd() *cobra.Command {
	var remote, machine string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up brewrig on this machine",
		Long: "Names this machine, points it at a private git repo shared with your other\n" +
			"machines, and publishes its first inventory. The repo must be private —\n" +
			"a package list is a decent map of what you work on.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd.Context(), cmd.OutOrStdout(), remote, machine)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "private GitHub repo URL (verified via gh)")
	cmd.Flags().StringVar(&machine, "machine", "", "name for this machine (default: its hostname)")
	return cmd
}

func runInit(ctx context.Context, out io.Writer, remote, machine string) error {
	c := brew.New()
	if err := c.Available(ctx); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		// Only a missing config starts from defaults. A present-but-unreadable
		// one must not: Save would then overwrite the stored remote and machine
		// name, and the machine would publish under a new name and orphan its
		// inventory — the exact outcome config.Load refuses to cause.
		if !errors.Is(err, config.ErrNotConfigured) {
			return fmt.Errorf("%w\n\nFix or delete it, then run `brewrig init` again", err)
		}
		cfg = config.Default()
	}
	if machine != "" {
		cfg.Machine = config.SanitizeMachineName(machine)
	}
	if remote != "" {
		if err := ghrepo.EnsurePrivate(ctx, remote); err != nil {
			return err
		}
		cfg.Remote = remote
	}

	// Flags alone are enough to run this unattended; only ask for what is
	// still missing.
	if interactive() && (machine == "" || cfg.Remote == "") {
		if machine == "" {
			name := cfg.Machine
			if err := huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("Name for this machine").
					Description("How it appears to your other Macs, and the file it publishes.").
					Value(&name),
			)).WithTheme(brand.Theme(brand.AccentBrew)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
				return err
			}
			cfg.Machine = config.SanitizeMachineName(name)
		}
		if cfg.Remote == "" {
			url, err := chooseRemote(ctx, out, cfg.Remote)
			if err != nil {
				return err
			}
			cfg.Remote = url
		}
	}

	if cfg.Machine == "" {
		return fmt.Errorf("no machine name — pass --machine")
	}
	if cfg.Remote == "" {
		return fmt.Errorf("no remote — pass --remote, or run `brewrig init` on a terminal to pick one")
	}

	if err := config.Save(cfg); err != nil {
		return err
	}
	p, _ := config.Path()
	fmt.Fprintf(out, "%s %s is %s\n", OkStyle.Render("✓"), p, AccentStyle.Render(cfg.Machine))

	// The first sync is part of setup: an init that leaves nothing published
	// looks identical to one that failed.
	fmt.Fprintln(out, DimStyle.Render("taking first snapshot…"))
	return runSync(ctx, out, syncOpts{})
}

// chooseRemote picks the private repo, creating it if asked. gh is required:
// it is what lets brewrig *verify* the repo is private rather than trust a URL.
func chooseRemote(ctx context.Context, out io.Writer, defaultURL string) (string, error) {
	if !ghrepo.Available() {
		return "", fmt.Errorf("GitHub CLI (gh) not found — it's how brewrig verifies the repo is private.\n" +
			"Install it (brew install gh) and re-run, or pass --remote for a repo you've checked yourself")
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
	)).WithTheme(brand.Theme(brand.AccentBrew)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
		return "", err
	}

	if mode == "create" {
		repoName := "brew-sync"
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("New private repo name").Value(&repoName),
		)).WithTheme(brand.Theme(brand.AccentBrew)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
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
			Placeholder("git@github.com:you/brew-sync.git").Value(&url),
	)).WithTheme(brand.Theme(brand.AccentBrew)).WithKeyMap(huhEscKeyMap()).Run(); err != nil {
		return "", err
	}
	if err := ghrepo.EnsurePrivate(ctx, url); err != nil {
		return "", err
	}
	return url, nil
}
