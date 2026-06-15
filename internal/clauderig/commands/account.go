package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/spf13/cobra"
)

// NewAccountCmd builds the `account` command group (alias `acct`): manage
// multiple Claude Code logins from one machine.
//
// The concept — and the file-fallback trick that lets one terminal run a
// different account without disturbing the rest — is credited to claude-swap by
// realiti4 (https://github.com/realiti4/claude-swap, MIT). This is a clean-room
// Go reimplementation living inside clauderig.
func NewAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "account",
		Aliases: []string{"acct"},
		Short:   "Manage multiple Claude Code logins (session-isolate or swap)",
		Long: "Run several Claude Code accounts from one machine.\n\n" +
			"  add     capture the currently logged-in account into rig's store\n" +
			"  list    show stored accounts and which one is live\n" +
			"  run     launch Claude Code as an account in THIS terminal only\n" +
			"          (isolated via CLAUDE_CONFIG_DIR — others stay on the default)\n" +
			"  switch  swap the machine-wide live login to another account\n\n" +
			"Concept credit: claude-swap by realiti4 (github.com/realiti4/claude-swap, MIT).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAccountAddCmd(), newAccountListCmd(), newAccountRunCmd(), newAccountSwitchCmd())
	return cmd
}

func newAccountAddCmd() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Capture the currently logged-in account into rig's store",
		Long: "Reads the live Claude Code credential (macOS Keychain, or .credentials.json\n" +
			"elsewhere) and saves a copy under ~/.clauderig/accounts so rig can run or\n" +
			"swap to it later. Log into the account in Claude Code first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			raw, err := account.ReadLive()
			if err != nil {
				return err
			}
			a, err := account.AccountFromBlob(raw, label, time.Now())
			if err != nil {
				return err
			}
			existing, _ := st.Resolve(a.ID)
			if err := st.Save(a, raw); err != nil {
				return err
			}
			verb := "Added"
			if existing.ID == a.ID {
				verb = "Updated"
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render(verb), accountTitle(a))
			if a.Label == "" {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("tip: re-run with --label <name> for a friendly handle"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "friendly name for the account (e.g. work, personal)")
	return cmd
}

func newAccountListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "status"},
		Short:   "Show stored accounts and which one is live",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			all, err := st.List()
			if err != nil {
				return err
			}
			if len(all) == 0 {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("no accounts yet — run `clauderig account add` while logged in"))
				return nil
			}
			liveID := liveFingerprint()
			fmt.Fprintln(out, HeaderStyle.Render("Claude Code accounts"))
			for _, a := range all {
				marker := "  "
				if a.ID == liveID {
					marker = OkStyle.Render("→ ")
				}
				fmt.Fprintf(out, "%s%s\n", marker, accountTitle(a))
			}
			if liveID == "" {
				fmt.Fprintf(out, "\n%s\n", DimStyle.Render("(live credential is untracked — `account add` to capture it)"))
			}
			return nil
		},
	}
}

func newAccountRunCmd() *cobra.Command {
	var noShare bool
	cmd := &cobra.Command{
		Use:   "run <id|label> [-- claude args...]",
		Short: "Launch Claude Code as an account in THIS terminal only",
		Long: "Session mode: writes the account's credential into a private\n" +
			"CLAUDE_CONFIG_DIR and execs `claude` against it, so this terminal runs as\n" +
			"the chosen account while every other terminal and the VS Code extension\n" +
			"stay on your default. Your ~/.claude customizations (settings, CLAUDE.md,\n" +
			"skills, commands, agents) are shared in by default; --no-share for a bare\n" +
			"profile. Args after `--` are passed through to claude.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			a, err := st.Resolve(args[0])
			if err != nil {
				return err
			}
			raw, err := st.Credential(a.ID)
			if err != nil {
				return fmt.Errorf("read stored credential: %w", err)
			}
			home, err := account.ClaudeHome()
			if err != nil {
				return err
			}
			dir, err := st.PrepareSession(a, raw, !noShare, home)
			if err != nil {
				return err
			}
			claudeBin, err := exec.LookPath("claude")
			if err != nil {
				return errors.New("`claude` not found on PATH")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%s %s %s\n",
				DimStyle.Render("session:"), accountTitle(a),
				DimStyle.Render("(CLAUDE_CONFIG_DIR="+dir+")"))
			return runClaude(cmd, claudeBin, dir, args[1:])
		},
	}
	cmd.Flags().BoolVar(&noShare, "no-share", false, "don't share ~/.claude customizations into the session (bare profile)")
	return cmd
}

func newAccountSwitchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "switch [<id|label>]",
		Short: "Swap the machine-wide live login to another account",
		Long: "Global swap: overwrites the live credential the whole machine reads\n" +
			"(macOS Keychain / .credentials.json), so every Claude Code instance follows.\n" +
			"With no argument, rotates to the next stored account. The displaced\n" +
			"credential is backed up first under ~/.clauderig/cred-backups.\n\n" +
			"Note: this affects ALL terminals at once. For parallel accounts, prefer\n" +
			"`account run`.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			var target account.Account
			if len(args) == 1 {
				target, err = st.Resolve(args[0])
			} else {
				target, err = st.Next(liveFingerprint())
			}
			if err != nil {
				return err
			}
			raw, err := st.Credential(target.ID)
			if err != nil {
				return fmt.Errorf("read stored credential: %w", err)
			}
			// Back up the displaced live credential before overwriting.
			if cur, lerr := account.ReadLive(); lerr == nil {
				if account.FingerprintOf(cur) == target.ID {
					fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already live:"), accountTitle(target))
					return nil
				}
				path, berr := st.BackupLive(cur, time.Now().UTC().Format("20060102-150405"))
				if berr != nil {
					return fmt.Errorf("back up current credential: %w", berr)
				}
				if path != "" {
					fmt.Fprintf(out, "%s %s\n", DimStyle.Render("backed up live credential →"), path)
				}
			} else if !errors.Is(lerr, account.ErrNoLive) {
				return lerr
			}
			if err := account.WriteLive(raw); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("Switched to"), accountTitle(target))
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("all terminals now use this account; restart running Claude Code sessions"))
			return nil
		},
	}
	return cmd
}

// runClaude execs claude with an isolated CLAUDE_CONFIG_DIR, inheriting this
// terminal's stdio and propagating the exit code.
func runClaude(cmd *cobra.Command, bin, configDir string, extra []string) error {
	c := exec.CommandContext(cmd.Context(), bin, extra...)
	c.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	return err
}

// liveFingerprint returns the rig id of the currently-live credential, or "" if
// none / untracked / unreadable.
func liveFingerprint() string {
	raw, err := account.ReadLive()
	if err != nil {
		return ""
	}
	return account.FingerprintOf(raw)
}

func accountTitle(a account.Account) string {
	name := a.Label
	if name == "" {
		name = a.ID
	} else {
		name = fmt.Sprintf("%s (%s)", a.Label, a.ID)
	}
	if a.SubscriptionType != "" {
		name += DimStyle.Render(" · " + a.SubscriptionType)
	}
	return name
}
