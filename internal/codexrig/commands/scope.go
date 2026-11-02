package commands

import (
	"context"
	"encoding/json"
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
					return hooks.DisplayError(err)
				}
				removed, err := hooks.Uninstall(path)
				if err != nil {
					return hooks.DisplayError(err)
				}
				if len(removed) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("nothing of codexrig's was installed there"))
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s from %s\n", OkStyle.Render("removed"), hooks.Display(strings.Join(removed, ", ")), hooks.Display(path))
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
				"Only exact current codexrig commands in this scope are approved, without a prompt.\n" +
				"Every command approved, refused, or left alone is printed (also with --json).\n" +
				"Lookalike or outdated commands are refused: reinstall outdated hooks, and review\n" +
				"lookalikes through Codex itself. Other hooks are left alone. Nothing eligible\n" +
				"means a non-zero exit.",
			RunE: func(cmd *cobra.Command, args []string) error {
				return trustScope(cmd, pathOf)
			},
		},
	)
	for _, child := range cmd.Commands() {
		if child.Name() == "trust" {
			child.Flags().Bool("json", false, "print commands approved and refused as JSON")
		}
	}
	return cmd
}

func installScope(cmd *cobra.Command, pathOf func(context.Context) (string, error), plansOf func(context.Context) ([]hooks.Plan, error)) error {
	out := cmd.OutOrStdout()
	path, err := pathOf(cmd.Context())
	if err != nil {
		return hooks.DisplayError(err)
	}
	added, updated, err := hooks.InstallScope(commandHookScope(cmd, path))
	if err != nil {
		return hooks.DisplayError(err)
	}
	switch {
	case len(added) == 0 && len(updated) == 0:
		fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already installed:"), hooks.Display(path))
	default:
		if len(added) > 0 {
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("installed"), strings.Join(added, ", "))
		}
		if len(updated) > 0 {
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("updated"), strings.Join(updated, ", "))
		}
		fmt.Fprintf(out, "  %s\n", DimStyle.Render(hooks.Display(path)))
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
		return hooks.DisplayError(err)
	}
	status, err := hooks.Status(path, commandHookScope(cmd, path))
	if err != nil {
		return hooks.DisplayError(err)
	}
	present := status.Events()
	switch {
	case len(status.Lookalikes) > 0:
		fmt.Fprintf(out, "%s — run `codexrig %s install`\n", WarnStyle.Render("lookalike or out of date, reinstall"), cmd.Parent().Name())
	case len(present) == 0:
		fmt.Fprintf(out, "%s\n", DimStyle.Render("not installed — `codexrig "+cmd.Parent().Name()+" install`"))
	default:
		fmt.Fprintf(out, "%s %s\n", OkStyle.Render("installed"), hooks.Display(strings.Join(present, ", ")))
	}
	fmt.Fprintf(out, "  %s\n", DimStyle.Render(hooks.Display(path)))
	plans, err := plansOf(cmd.Context())
	if err == nil && len(status.Lookalikes) == 0 {
		if drifted, derr := hooks.Drift(path, plans); derr == nil && len(drifted) > 0 {
			fmt.Fprintf(out, "  %s %s\n", WarnStyle.Render("out of date, reinstall:"), hooks.Display(strings.Join(drifted, ", ")))
			fmt.Fprintf(out, "  `codexrig %s install` brings them up to date\n", cmd.Parent().Name())
		}
	}
	// Ask Codex whether it will actually run them, since that is the question
	// somebody checking status is really asking.
	printTrustState(cmd, path, status)
	return nil
}

func printTrustState(cmd *cobra.Command, path string, installed hooks.CheckResult) {
	out := cmd.OutOrStdout()
	if !appserverAvailable() {
		printOtherHooks(cmd, path, installed)
		return
	}
	check, err := hooks.Check(cmd.Context(), commandCodexHome(cmd, path), filepath.Dir(path), commandHookScope(cmd, path))
	if err != nil {
		printOtherHooks(cmd, path, installed)
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("could not ask Codex whether it will run them: "+hooks.Display(err.Error())))
		return
	}
	printCheckedTrustState(cmd, path, check)
}

func printOtherHooks(cmd *cobra.Command, path string, check hooks.CheckResult) {
	out := cmd.OutOrStdout()
	scope := commandHookScope(cmd, path)
	for _, h := range check.Lookalikes {
		if scope.References(h.SourcePath) {
			fmt.Fprintf(out, "  lookalike or out of date %s: %s\n    Reinstall outdated hooks; review lookalikes in Codex itself.\n", hooks.Display(h.EventName), hooks.Display(h.Command))
			if h.ValidationIssue != "" {
				fmt.Fprintf(out, "    %s\n", hooks.Display(h.ValidationIssue))
			}
		}
	}
	for _, h := range check.Foreign {
		if scope.References(h.SourcePath) {
			fmt.Fprintf(out, "  left alone %s: %s\n", hooks.Display(h.EventName), hooks.Display(h.Command))
		}
	}
}

func printCheckedTrustState(cmd *cobra.Command, path string, check hooks.CheckResult) {
	out := cmd.OutOrStdout()
	scope := commandHookScope(cmd, path)
	printOtherHooks(cmd, path, check)
	var untrusted []string
	seen := 0
	disabled := 0
	for _, h := range check.Ours {
		if !scope.References(h.SourcePath) {
			continue
		}
		seen++
		if h.Trusted() && !h.Enabled {
			disabled++
			fmt.Fprintf(out, "  trusted but disabled: %s\n", hooks.Display(h.EventName))
			continue
		}
		if !h.Trusted() {
			untrusted = append(untrusted, hooks.Display(h.EventName))
		}
	}
	for _, h := range check.StillTrusted {
		if h.SourcePath == "" || scope.References(h.SourcePath) {
			fmt.Fprintf(out, "  STILL TRUSTED: %s from %s: %s\n    %s\n", hooks.Display(h.EventName), hooks.Display(h.SourcePath), hooks.Display(h.Command), hooks.Display(hooks.RevokeHint("", h)))
		}
	}
	for _, h := range check.Unreported {
		if scope.References(h.SourcePath) {
			fmt.Fprintf(out, "  trust unknown: Codex did not list %s: %s\n", hooks.Display(h.EventName), hooks.Display(h.Command))
		}
	}
	if seen == 0 && len(check.Unreported) == 0 {
		fmt.Fprintln(out, "  trust unknown: Codex did not list current hooks in this scope")
	}
	enabled := 0
	for _, h := range check.Ours {
		if scope.References(h.SourcePath) && h.Runnable() {
			enabled++
		}
	}
	if len(untrusted) == 0 && enabled > 0 {
		message := "trusted — Codex will run them"
		if disabled > 0 {
			message = "trusted — Codex will run enabled hooks only"
		}
		fmt.Fprintf(out, "  %s\n", OkStyle.Render(message))
		return
	}
	if len(untrusted) > 0 {
		fmt.Fprintf(out, "  %s %s\n", ErrStyle.Render("not trusted:"), strings.Join(untrusted, ", "))
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("Codex is silently skipping them — `codexrig "+cmd.Parent().Name()+" trust`"))
	}
}

func trustScope(cmd *cobra.Command, pathOf func(context.Context) (string, error), clients ...hooks.Client) error {
	path, err := pathOf(cmd.Context())
	if err != nil {
		return hooks.DisplayError(err)
	}
	var res hooks.TrustResult
	var trustErr error
	if len(clients) > 0 {
		res, trustErr = hooks.TrustWithClient(cmd.Context(), clients[0], commandCodexHome(cmd, path), commandHookScope(cmd, path))
	} else {
		res, trustErr = hooks.Trust(cmd.Context(), commandCodexHome(cmd, path), filepath.Dir(path), commandHookScope(cmd, path))
	}
	return printScopeTrustResult(cmd, res, trustErr)
}

func commandCodexHome(cmd *cobra.Command, path string) string {
	if cmd.Parent().Name() == "global" {
		return filepath.Dir(path)
	}
	return ""
}

func commandHookScope(cmd *cobra.Command, path string) hooks.Scope {
	if cmd.Parent().Name() == "project" {
		return hooks.ProjectScope(filepath.Dir(filepath.Dir(path)))
	}
	return hooks.GlobalScope(filepath.Dir(path))
}

func printScopeTrustResult(cmd *cobra.Command, res hooks.TrustResult, trustErr error) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
			return hooks.DisplayError(err)
		}
	} else {
		hooks.WriteTrustResult(cmd.OutOrStdout(), res)
	}
	return hooks.DisplayError(trustErr)
}
