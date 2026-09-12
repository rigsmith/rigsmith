package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/guard"
	"github.com/rigsmith/rigsmith/internal/codexrig/hooks"
	"github.com/spf13/cobra"
)

// The scope IS the command. Codex has two places hooks can live — the user's
// Codex home and a repository's .codex/ — and they do different jobs, so
// `codexrig global install` and `codexrig project install` read better than one
// command with a --scope flag whose default somebody has to remember.
//
// There is deliberately no third scope. clauderig has `local` for Claude Code's
// settings.local.json; Codex has no gitignored per-checkout counterpart, and
// inventing one would mean writing a file Codex does not read.

// ScopeCommands builds the scoped hook groups.
func ScopeCommands() []*cobra.Command {
	return []*cobra.Command{
		scopeCmd("global", []string{"hooks"},
			"Install the backup hooks into your own Codex home",
			"Your Codex home's hooks.json, so every session on this machine backs itself up.",
			userHooksPath, func(context.Context) ([]hooks.Plan, error) { return hooks.SyncPlans(), nil }),
		scopeCmd("project", nil,
			"Install the worktree guard into this repository",
			"This repository's .codex/hooks.json, committed, so everyone working here gets the same guard.",
			projectHooksPath, guardPlans),
	}
}

func guardPlans(context.Context) ([]hooks.Plan, error) {
	return hooks.GuardPlans(guard.Matcher()), nil
}

func userHooksPath(context.Context) (string, error) {
	home, err := codexhome.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, hooks.FileName), nil
}

func projectHooksPath(ctx context.Context) (string, error) {
	root, err := repoRoot(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".codex", hooks.FileName), nil
}

func repoRoot(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	repo, err := gitrepo.Open(ctx, cwd)
	if err != nil {
		return "", fmt.Errorf("not inside a git repository")
	}
	return repo.Toplevel(ctx)
}

func scopeCmd(name string, aliases []string, short, long string,
	pathOf func(context.Context) (string, error),
	plansOf func(context.Context) ([]hooks.Plan, error)) *cobra.Command {

	cmd := &cobra.Command{
		Use:     name,
		Aliases: aliases,
		Short:   short,
		Long:    long,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		&cobra.Command{
			Use: "install", Short: "Add the hooks", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return installScope(cmd, pathOf, plansOf)
			},
		},
		&cobra.Command{
			Use: "uninstall", Short: "Remove the hooks", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := pathOf(cmd.Context())
				if err != nil {
					return err
				}
				removed, err := hooks.Uninstall(path)
				if err != nil {
					return err
				}
				if len(removed) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("nothing of codexrig's was installed there"))
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s from %s\n", OkStyle.Render("removed"), strings.Join(removed, ", "), path)
				return nil
			},
		},
		&cobra.Command{
			Use: "status", Short: "Show which hooks are installed, and whether Codex will run them", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return statusScope(cmd, pathOf, plansOf)
			},
		},
		&cobra.Command{
			Use: "trust", Short: "Record these hooks as trusted, so Codex actually runs them", Args: cobra.NoArgs,
			Long: "Codex will not run a hook until its hash is recorded in config.toml, and it\n" +
				"says nothing when it declines — an installed-but-untrusted hook is simply\n" +
				"silent.\n\n" +
				"This asks Codex for that hash and hands it back, rather than computing one,\n" +
				"and makes the edit through Codex so config.toml keeps its own formatting.\n" +
				"It is you trusting a file, which is why it is a separate command.",
			RunE: func(cmd *cobra.Command, args []string) error {
				return trustScope(cmd, pathOf)
			},
		},
	)
	return cmd
}

func installScope(cmd *cobra.Command, pathOf func(context.Context) (string, error), plansOf func(context.Context) ([]hooks.Plan, error)) error {
	out := cmd.OutOrStdout()
	path, err := pathOf(cmd.Context())
	if err != nil {
		return err
	}
	plans, err := plansOf(cmd.Context())
	if err != nil {
		return err
	}
	added, updated, err := hooks.Install(path, plans)
	if err != nil {
		return err
	}
	switch {
	case len(added) == 0 && len(updated) == 0:
		fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already installed:"), path)
	default:
		if len(added) > 0 {
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("installed"), strings.Join(added, ", "))
		}
		if len(updated) > 0 {
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("updated"), strings.Join(updated, ", "))
		}
		fmt.Fprintf(out, "  %s\n", DimStyle.Render(path))
	}
	// Installed is not running. Say so every time, because the failure is
	// silent: Codex declines an untrusted hook without a word.
	fmt.Fprintf(out, "\n  %s\n", WarnStyle.Render("Codex will not run these until they are trusted."))
	fmt.Fprintf(out, "  %s\n", DimStyle.Render("run `codexrig "+cmd.Parent().Name()+" trust`"))
	return nil
}

func statusScope(cmd *cobra.Command, pathOf func(context.Context) (string, error), plansOf func(context.Context) ([]hooks.Plan, error)) error {
	out := cmd.OutOrStdout()
	path, err := pathOf(cmd.Context())
	if err != nil {
		return err
	}
	present, err := hooks.Status(path)
	if err != nil {
		return err
	}
	if len(present) == 0 {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("not installed — `codexrig "+cmd.Parent().Name()+" install`"))
		return nil
	}
	fmt.Fprintf(out, "%s %s\n", OkStyle.Render("installed"), strings.Join(present, ", "))
	fmt.Fprintf(out, "  %s\n", DimStyle.Render(path))

	plans, err := plansOf(cmd.Context())
	if err == nil {
		if drifted, derr := hooks.Drift(path, plans); derr == nil && len(drifted) > 0 {
			fmt.Fprintf(out, "  %s %s\n", WarnStyle.Render("out of date:"), strings.Join(drifted, ", "))
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("`codexrig "+cmd.Parent().Name()+" install` brings them up to date"))
		}
	}
	// Ask Codex whether it will actually run them, since that is the question
	// somebody checking status is really asking.
	printTrustState(cmd, filepath.Dir(path))
	return nil
}

func printTrustState(cmd *cobra.Command, cwd string) {
	out := cmd.OutOrStdout()
	if !appserverAvailable() {
		return
	}
	ours, err := hooks.Check(cmd.Context(), "", cwd)
	if err != nil {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("could not ask Codex whether it will run them: "+err.Error()))
		return
	}
	var untrusted []string
	for _, h := range ours {
		if !h.Trusted() {
			untrusted = append(untrusted, h.EventName)
		}
	}
	if len(untrusted) == 0 && len(ours) > 0 {
		fmt.Fprintf(out, "  %s\n", OkStyle.Render("trusted — Codex will run them"))
		return
	}
	if len(untrusted) > 0 {
		fmt.Fprintf(out, "  %s %s\n", ErrStyle.Render("not trusted:"), strings.Join(untrusted, ", "))
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("Codex is silently skipping them — `codexrig "+cmd.Parent().Name()+" trust`"))
	}
}

func trustScope(cmd *cobra.Command, pathOf func(context.Context) (string, error)) error {
	out := cmd.OutOrStdout()
	path, err := pathOf(cmd.Context())
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("nothing installed at %s yet", path)
	}
	res, err := hooks.Trust(cmd.Context(), "", filepath.Dir(path))
	if err != nil {
		return err
	}
	if len(res.Trusted) > 0 {
		fmt.Fprintf(out, "%s %s\n", OkStyle.Render("trusted"), strings.Join(res.Trusted, ", "))
	}
	if len(res.Already) > 0 {
		fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already trusted:"), strings.Join(res.Already, ", "))
	}
	if len(res.Trusted) == 0 && len(res.Already) == 0 {
		fmt.Fprintln(out, DimStyle.Render("Codex reported no codexrig hooks here"))
	}
	if len(res.Foreign) > 0 {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("left alone (not codexrig's): "+strings.Join(res.Foreign, ", ")))
	}
	return nil
}
