package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/tui"
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
			"  add     capture the currently logged-in account into claudeRig's store\n" +
			"  list    show stored accounts and which one is live\n" +
			"  run     launch Claude Code as an account in THIS terminal only\n" +
			"          (isolated via CLAUDE_CONFIG_DIR — others stay on the default)\n" +
			"  switch  swap the machine-wide live login to another account\n" +
			"  remove  stop tracking an account (does not log it out)\n" +
			"  purge   remove all of claudeRig's account data",
		RunE: func(cmd *cobra.Command, args []string) error {
			if Interactive() {
				return runAccountUI(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAccountAddCmd(), newAccountListCmd(), newAccountRunCmd(),
		newAccountSwitchCmd(), newAccountRemoveCmd(), newAccountPurgeCmd())
	return cmd
}

func newAccountAddCmd() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Capture the currently logged-in account into claudeRig's store",
		Long: "Reads the live Claude Code credential (macOS Keychain, or .credentials.json\n" +
			"elsewhere) and saves a copy under ~/.clauderig/accounts so claudeRig can run or\n" +
			"swap to it later. Log into the account in Claude Code first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			a, updated, err := captureLive(st, label)
			if err != nil {
				return err
			}
			verb := "Added"
			if updated {
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
			return launchSession(cmd, st, a, noShare, args[1:])
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
			backup, already, err := performSwitch(st, target)
			if err != nil {
				return err
			}
			if already {
				fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already live:"), accountTitle(target))
				return nil
			}
			if backup != "" {
				fmt.Fprintf(out, "%s %s\n", DimStyle.Render("backed up live credential →"), backup)
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("Switched to"), accountTitle(target))
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("all terminals now use this account; restart running Claude Code sessions"))
			return nil
		},
	}
	return cmd
}

func newAccountRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <id|label>",
		Aliases: []string{"rm"},
		Short:   "Stop tracking an account (does not log it out of Claude Code)",
		Long: "Delete claudeRig's copy of an account and any session profile for it. This does\n" +
			"NOT touch the live Claude Code login — it just stops claudeRig tracking the\n" +
			"account. Requires an interactive terminal to confirm.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			a, err := st.Resolve(args[0])
			if err != nil {
				return err
			}
			if !Interactive() {
				return errors.New("refusing to remove without a terminal to confirm")
			}
			ok, err := confirmDestructive(fmt.Sprintf("Remove account %s from claudeRig's store? (does not log it out)", accountTitle(a)))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("aborted"))
				return nil
			}
			if err := st.Remove(a.ID); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render("Removed"), accountTitle(a))
			return nil
		},
	}
}

func newAccountPurgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "purge",
		Short: "Remove all of claudeRig's account data (does not log out of Claude Code)",
		Long: "Delete every tracked account, session profile, and credential backup from\n" +
			"claudeRig's store (~/.clauderig/accounts, sessions, cred-backups). This does NOT\n" +
			"touch the live Claude Code login. Requires an interactive terminal to confirm.",
		Args: cobra.NoArgs,
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
				fmt.Fprintln(out, DimStyle.Render("nothing to purge — no accounts tracked"))
				return nil
			}
			if !Interactive() {
				return errors.New("refusing to purge without a terminal to confirm")
			}
			ok, err := confirmDestructive(fmt.Sprintf("Delete ALL %d tracked accounts and their session profiles? (does not log out)", len(all)))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(out, DimStyle.Render("aborted"))
				return nil
			}
			if err := st.Purge(); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %d accounts\n", OkStyle.Render("Purged"), len(all))
			return nil
		},
	}
}

// confirmDestructive asks a yes/no question before an irreversible store change.
// Backing out (esc) is treated as "no", never an error.
func confirmDestructive(title string) (bool, error) {
	var ok bool
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Affirmative("Yes").Negative("No").Value(&ok),
	)).WithKeyMap(huhEscKeyMap()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return false, nil
	}
	return ok, err
}

// runAccountUI drives the interactive accounts screen. Like the MCP screen, the
// model only records an intent on exit; the work (capture / swap / launch) runs
// here, outside the event loop, then the screen re-opens with a fresh snapshot.
// "run" is terminal — it execs claude and takes over the terminal.
func runAccountUI(cmd *cobra.Command) error {
	st, err := account.DefaultStore()
	if err != nil {
		return err
	}
	note := ""
	for {
		all, err := st.List()
		if err != nil {
			return err
		}
		res, err := tea.NewProgram(tui.NewAccount(all, liveFingerprint(), note)).Run()
		if err != nil {
			return err
		}
		final, ok := res.(tui.AccountModel)
		if !ok {
			return nil
		}
		note = ""
		switch final.Action.Kind {
		case "":
			return nil
		case "add":
			label, err := promptLabel(cmd)
			if errors.Is(err, huh.ErrUserAborted) {
				continue
			}
			if err != nil {
				return err
			}
			a, updated, err := captureLive(st, label)
			if err != nil {
				note = errStyleNote(err)
				continue
			}
			note = "added " + a.ID
			if updated {
				note = "updated " + a.ID
			}
		case "switch":
			target, err := st.Resolve(final.Action.ID)
			if err != nil {
				note = errStyleNote(err)
				continue
			}
			_, already, err := performSwitch(st, target)
			if err != nil {
				note = errStyleNote(err)
				continue
			}
			note = "switched to " + accountTitle(target)
			if already {
				note = accountTitle(target) + " already live"
			}
		case "remove":
			target, err := st.Resolve(final.Action.ID)
			if err != nil {
				note = errStyleNote(err)
				continue
			}
			ok, err := confirmDestructive(fmt.Sprintf("Remove account %s from claudeRig's store? (does not log it out)", accountTitle(target)))
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := st.Remove(target.ID); err != nil {
				note = errStyleNote(err)
				continue
			}
			note = "removed " + accountTitle(target)
		case "run":
			target, err := st.Resolve(final.Action.ID)
			if err != nil {
				return err
			}
			return launchSession(cmd, st, target, false, nil)
		}
	}
}

// promptLabel asks for an optional friendly name when capturing an account from
// the UI. An empty value is fine; ErrUserAborted means the user backed out.
func promptLabel(cmd *cobra.Command) (string, error) {
	var label string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Label for this account (optional)").
			Placeholder("work, personal, …").
			Value(&label),
	)).WithKeyMap(huhEscKeyMap()).Run()
	return label, err
}

// errStyleNote renders an error as a transient screen note.
func errStyleNote(err error) string { return ErrStyle.Render(err.Error()) }

// captureLive reads the live credential and saves it as a tracked account,
// reporting whether an existing account was updated rather than created.
func captureLive(st *account.Store, label string) (account.Account, bool, error) {
	raw, err := account.ReadLive()
	if err != nil {
		return account.Account{}, false, err
	}
	a, err := account.AccountFromBlob(raw, label, time.Now())
	if err != nil {
		return account.Account{}, false, err
	}
	_, resolveErr := st.Resolve(a.ID)
	updated := resolveErr == nil
	if err := st.Save(a, raw); err != nil {
		return account.Account{}, false, err
	}
	return a, updated, nil
}

// performSwitch overwrites the live credential with target's, backing up the
// displaced one first. Returns the backup path ("" if nothing was live) and
// whether target was already live (a no-op). Shared by the CLI and the UI.
func performSwitch(st *account.Store, target account.Account) (backup string, already bool, err error) {
	raw, err := st.Credential(target.ID)
	if err != nil {
		return "", false, fmt.Errorf("read stored credential: %w", err)
	}
	if cur, lerr := account.ReadLive(); lerr == nil {
		if account.FingerprintOf(cur) == target.ID {
			return "", true, nil
		}
		backup, err = st.BackupLive(cur, time.Now().UTC().Format("20060102-150405"))
		if err != nil {
			return "", false, fmt.Errorf("back up current credential: %w", err)
		}
	} else if !errors.Is(lerr, account.ErrNoLive) {
		return "", false, lerr
	}
	if err := account.WriteLive(raw); err != nil {
		return "", false, err
	}
	return backup, false, nil
}

// launchSession prepares a's isolated profile and execs claude against it,
// taking over this terminal. Shared by the CLI `run` and the UI.
func launchSession(cmd *cobra.Command, st *account.Store, a account.Account, noShare bool, extra []string) error {
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
	return runClaude(cmd, claudeBin, dir, extra)
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
