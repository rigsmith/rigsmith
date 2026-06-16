package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
// Two mechanisms, deliberately separated. Session mode (`run`) gives each
// account an isolated, self-refreshing config dir and never touches the live
// login — the safe, primary path. Global `switch` changes the machine-wide
// login and is guarded by live-session detection (mutating the credential under
// a running Claude Code instance forces a re-login).
//
// Concept and safety mechanisms credited to claude-swap by realiti4
// (github.com/realiti4/claude-swap, MIT); clean-room Go reimplementation.
func NewAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "account",
		Aliases: []string{"acct"},
		Short:   "Manage multiple Claude Code logins (isolated sessions or machine-wide swap)",
		Long: "Run several Claude Code accounts from one machine.\n\n" +
			"  add     capture the currently logged-in account into claudeRig's store\n" +
			"  list    show stored accounts and which one is live\n" +
			"  run     launch Claude Code as an account in THIS terminal only\n" +
			"          (isolated, self-refreshing — never touches your live login)\n" +
			"  switch  change the machine-wide login (guarded: refuses while Claude runs)\n" +
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
		newAccountSwitchCmd(), newAccountSessionsCmd(), newAccountRemoveCmd(), newAccountPurgeCmd())
	return cmd
}

func newAccountAddCmd() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "add [--label name]",
		Short: "Capture the currently logged-in account into claudeRig's store",
		Long: "Reads the live Claude Code credential and saves a copy under\n" +
			"~/.clauderig/accounts so claudeRig can run or swap to it later. The captured\n" +
			"account becomes the tracked 'live' one. Log into the account first.",
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
			a, updated, err := st.CaptureLive(raw, label)
			if err != nil {
				return err
			}
			if err := st.SetActive(a.ID); err != nil {
				return err
			}
			verb := "Added"
			if updated {
				verb = "Updated"
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render(verb), accountTitle(a))
			if !labelGiven(cmd) {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("tip: --label <name> gives it a friendly handle"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "friendly name for the account (e.g. work, personal)")
	return cmd
}

func labelGiven(cmd *cobra.Command) bool { return cmd.Flags().Changed("label") }

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
			active, _ := st.Active()
			fmt.Fprintln(out, HeaderStyle.Render("Claude Code accounts"))
			for _, a := range all {
				marker := "  "
				if a.ID == active {
					marker = OkStyle.Render("→ ")
				}
				fmt.Fprintf(out, "%s%s\n", marker, accountTitle(a))
			}
			if active == "" {
				fmt.Fprintf(out, "\n%s\n", DimStyle.Render("(no account marked live — `account add` or `switch` sets it)"))
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
		Long: "Session mode: runs `claude` against the account's own persistent\n" +
			"CLAUDE_CONFIG_DIR, so this terminal is that account while every other\n" +
			"terminal and the VS Code extension stay on your default. The profile\n" +
			"self-refreshes its own token in isolation and never touches your live\n" +
			"login. ~/.claude customizations are shared in by default (--no-share for a\n" +
			"bare profile). Args after `--` pass through to claude.",
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
			warnIfActive(cmd, st, a)
			home, err := account.ClaudeHome()
			if err != nil {
				return err
			}
			dir, err := st.EnsureSession(a, !noShare, home)
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
	var dryRun, force, kill bool
	cmd := &cobra.Command{
		Use:   "switch [<id|label>]",
		Short: "Change the machine-wide login (guarded against live sessions)",
		Long: "Global swap: overwrites the live credential the whole machine reads, so\n" +
			"every Claude Code instance follows. With no argument, rotates to the next\n" +
			"stored account.\n\n" +
			"GUARDED: refuses while any Claude Code instance is running, because\n" +
			"swapping the credential under a live session forces a re-login. Close your\n" +
			"Claude windows first, or use `run` for parallel accounts instead. The\n" +
			"displaced account's current credential is saved back to its store, and a\n" +
			"timestamped backup is kept under ~/.clauderig/cred-backups.\n\n" +
			"--dry-run shows the plan (and any blocking sessions) without changing a thing.\n" +
			"--force overrides the guard and swaps anyway — the running sessions it lists\n" +
			"will have to log in again. --kill terminates those sessions first (SIGTERM,\n" +
			"then SIGKILL), then swaps.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSwitch(cmd, args, dryRun, force, kill)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what switch would do without changing anything")
	cmd.Flags().BoolVar(&force, "force", false, "swap even while Claude Code is running (those sessions will need to re-login)")
	cmd.Flags().BoolVar(&kill, "kill", false, "terminate running Claude Code processes first, then swap")
	return cmd
}

func newAccountSessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "sessions",
		Aliases: []string{"ps"},
		Short:   "List running Claude Code instances (what blocks a switch)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			home, err := account.ClaudeHome()
			if err != nil {
				return err
			}
			live := account.RunningInstances(home)
			if len(live) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no Claude Code instances running"))
				return nil
			}
			fmt.Fprintln(out, HeaderStyle.Render(fmt.Sprintf("%d Claude Code instance(s) running", len(live))))
			printInstances(out, live)
			return nil
		},
	}
}

func newAccountRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <id|label>",
		Aliases: []string{"rm"},
		Short:   "Stop tracking an account (does not log it out of Claude Code)",
		Long: "Delete claudeRig's copy of an account and its session profile. This does\n" +
			"NOT touch the live Claude Code login. Requires an interactive terminal.",
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
			if err != nil || !ok {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("aborted"))
				return err
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
			"claudeRig's store. Does NOT touch the live Claude Code login. Requires an\n" +
			"interactive terminal.",
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
			if err != nil || !ok {
				fmt.Fprintln(out, DimStyle.Render("aborted"))
				return err
			}
			if err := st.Purge(); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %d accounts\n", OkStyle.Render("Purged"), len(all))
			return nil
		},
	}
}

// runAccountUI drives the interactive accounts screen. The model records an
// intent on exit; the work (capture / swap / launch / remove) runs here, outside
// the event loop, then the screen re-opens — except `run`, which is terminal.
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
		active, _ := st.Active()
		var procs []account.Instance
		if home, herr := account.ClaudeHome(); herr == nil {
			procs = account.RunningInstances(home)
		}
		res, err := tea.NewProgram(tui.NewAccount(all, active, procs, note)).Run()
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
			label, perr := promptLabel(cmd)
			if errors.Is(perr, huh.ErrUserAborted) {
				continue
			}
			if perr != nil {
				return perr
			}
			raw, rerr := account.ReadLive()
			if rerr != nil {
				note = ErrStyle.Render(rerr.Error())
				continue
			}
			a, updated, cerr := st.CaptureLive(raw, label)
			if cerr != nil {
				note = ErrStyle.Render(cerr.Error())
				continue
			}
			_ = st.SetActive(a.ID)
			note = "added " + a.ID
			if updated {
				note = "updated " + a.ID
			}
		case "switch":
			target, rerr := st.Resolve(final.Action.ID)
			if rerr != nil {
				note = ErrStyle.Render(rerr.Error())
				continue
			}
			_, blocked, serr := doSwitch(st, target, false)
			if serr != nil {
				note = ErrStyle.Render(serr.Error())
				continue
			}
			if len(blocked) > 0 {
				note = resolveBlockedSwitch(st, target, blocked)
				continue
			}
			note = "switched to " + accountTitle(target)
		case "remove":
			target, rerr := st.Resolve(final.Action.ID)
			if rerr != nil {
				note = ErrStyle.Render(rerr.Error())
				continue
			}
			ok, cerr := confirmDestructive(fmt.Sprintf("Remove account %s? (does not log it out)", accountTitle(target)))
			if cerr != nil {
				return cerr
			}
			if !ok {
				continue
			}
			if err := st.Remove(target.ID); err != nil {
				note = ErrStyle.Render(err.Error())
				continue
			}
			note = "removed " + accountTitle(target)
		case "run":
			target, rerr := st.Resolve(final.Action.ID)
			if rerr != nil {
				return rerr
			}
			warnIfActive(cmd, st, target)
			home, herr := account.ClaudeHome()
			if herr != nil {
				return herr
			}
			dir, derr := st.EnsureSession(target, true, home)
			if derr != nil {
				return derr
			}
			claudeBin, lerr := exec.LookPath("claude")
			if lerr != nil {
				return errors.New("`claude` not found on PATH")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", DimStyle.Render("session:"), accountTitle(target))
			return runClaude(cmd, claudeBin, dir, nil)
		}
	}
}

// resolveBlockedSwitch is the menu's response when `switch` hits live sessions:
// it asks whether to force the swap, kill the sessions first, or cancel, then
// performs the choice and returns a status note for the screen.
func resolveBlockedSwitch(st *account.Store, target account.Account, blocked []account.Instance) string {
	var lines []string
	for _, in := range blocked {
		lines = append(lines, fmt.Sprintf("• pid %d  %s", in.PID, in.Kind))
	}
	var choice string
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("%d Claude Code session(s) are live — switching will disrupt them", len(blocked))).
			Description(strings.Join(lines, "\n")).
			Options(
				huh.NewOption("Cancel — leave them alone", "cancel"),
				huh.NewOption("Kill them, then switch", "kill"),
				huh.NewOption("Force switch (they'll need to re-login)", "force"),
			).
			Value(&choice),
	)).WithKeyMap(huhEscKeyMap()).Run()
	if err != nil || choice == "cancel" || choice == "" {
		return DimStyle.Render("switch cancelled")
	}
	if choice == "kill" {
		if failed := account.KillInstances(blocked, 5*time.Second); len(failed) > 0 {
			// fall through to a forced swap past any straggler
			_ = failed
		}
	}
	if _, _, serr := doSwitch(st, target, true); serr != nil {
		return ErrStyle.Render(serr.Error())
	}
	if choice == "kill" {
		return "killed live sessions, switched to " + accountTitle(target)
	}
	return "force-switched to " + accountTitle(target)
}

// warnIfActive cautions against running your current live account as a separate
// session: it shares a rotating refresh token with the live session, so the
// stored snapshot may be stale and prompt for login. `run` is meant for a
// different, dormant account.
func warnIfActive(cmd *cobra.Command, st *account.Store, a account.Account) {
	if active, _ := st.Active(); active == a.ID {
		fmt.Fprintln(cmd.ErrOrStderr(), WarnStyle.Render(
			"note: "+a.Label+" is your current live account — a separate session of it shares a"))
		fmt.Fprintln(cmd.ErrOrStderr(), WarnStyle.Render(
			"      rotating token and may ask you to log in. Prefer running a different account;"))
		fmt.Fprintln(cmd.ErrOrStderr(), DimStyle.Render(
			"      if it does prompt, re-run `account add` for it while it's your live login."))
	}
}

// promptLabel asks for an optional friendly name when capturing from the UI.
func promptLabel(cmd *cobra.Command) (string, error) {
	var label string
	return label, huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Label for this account (optional)").
			Placeholder("work, personal, …").
			Value(&label),
	)).WithKeyMap(huhEscKeyMap()).Run()
}

// runSwitch performs (or, with dryRun, previews) a guarded global swap.
func runSwitch(cmd *cobra.Command, args []string, dryRun, force, kill bool) error {
	out := cmd.OutOrStdout()
	st, err := account.DefaultStore()
	if err != nil {
		return err
	}
	active, _ := st.Active()

	var target account.Account
	if len(args) == 1 {
		target, err = st.Resolve(args[0])
	} else {
		target, err = nextAccount(st, active)
	}
	if err != nil {
		return err
	}
	if active == target.ID {
		fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already live:"), accountTitle(target))
		return nil
	}

	// The guard: never swap the credential under a running Claude Code instance.
	home, err := account.ClaudeHome()
	if err != nil {
		return err
	}
	live := account.RunningInstances(home)

	if dryRun {
		fmt.Fprintln(out, HeaderStyle.Render("switch --dry-run"))
		if active != "" {
			fmt.Fprintf(out, "  would save displaced %s back to its store\n", DimStyle.Render(active))
		}
		fmt.Fprintf(out, "  would set live login → %s\n", accountTitle(target))
		if len(live) > 0 {
			verb := "switch would refuse (use --force or --kill)"
			switch {
			case kill:
				verb = "--kill would terminate these, then swap"
			case force:
				verb = "--force would override and log these out"
			}
			fmt.Fprintf(out, "  %s\n", WarnStyle.Render(fmt.Sprintf("%d Claude Code instance(s) running — %s", len(live), verb)))
			printInstances(out, live)
		} else {
			fmt.Fprintf(out, "  %s\n", OkStyle.Render("no live sessions — switch would proceed"))
		}
		return nil
	}

	if len(live) > 0 {
		switch {
		case kill:
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("killing %d Claude Code process(es)…", len(live))))
			printInstances(out, live)
			if failed := account.KillInstances(live, 5*time.Second); len(failed) > 0 {
				fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf("  %d could not be killed; forcing the swap anyway:", len(failed))))
				printInstances(out, failed)
			}
			force = true // path cleared (or forced past stragglers)
		case force:
			fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf("--force: swapping despite %d live Claude Code session(s) — they will need to log in again:", len(live))))
			printInstances(out, live)
		default:
			printSwitchRefused(out, live)
			return errors.New("live Claude Code sessions detected")
		}
	}

	backup, _, err := doSwitch(st, target, force)
	if err != nil {
		return err
	}
	if backup != "" {
		fmt.Fprintf(out, "%s %s\n", DimStyle.Render("backed up live credential →"), backup)
	}
	fmt.Fprintf(out, "%s %s\n", OkStyle.Render("Switched to"), accountTitle(target))
	return nil
}

// doSwitch performs the guarded global swap to target (no dry-run, no printing).
// It re-checks for live sessions and returns them as `blocked` (with no
// mutation) if any are running — so every caller, CLI or UI, is guarded. On
// success it round-trips the displaced account's current credential back into
// its store and returns the safety-backup path.
func doSwitch(st *account.Store, target account.Account, force bool) (backup string, blocked []account.Instance, err error) {
	active, _ := st.Active()
	home, err := account.ClaudeHome()
	if err != nil {
		return "", nil, err
	}
	if !force {
		if live := account.RunningInstances(home); len(live) > 0 {
			return "", live, nil
		}
	}
	targetCred, err := st.Credential(target.ID)
	if err != nil {
		return "", nil, fmt.Errorf("read stored credential: %w", err)
	}
	if cur, lerr := account.ReadLive(); lerr == nil {
		backup, _ = st.BackupLive(cur, time.Now().UTC().Format("20060102-150405.000000000"))
		if active != "" && active != target.ID {
			_ = st.SaveCredential(active, cur) // best-effort: keep displaced snapshot fresh
		}
	} else if !errors.Is(lerr, account.ErrNoLive) {
		return "", nil, lerr
	}
	if err := account.WriteLive(targetCred); err != nil {
		return "", nil, err
	}
	if err := st.SetActive(target.ID); err != nil {
		return "", nil, err
	}
	return backup, nil, nil
}

func printSwitchRefused(out interface{ Write([]byte) (int, error) }, live []account.Instance) {
	fmt.Fprintf(out, "%s\n", ErrStyle.Render("Refusing to switch: Claude Code is running."))
	fmt.Fprintln(out, DimStyle.Render("  swapping the live credential now would log those sessions out. Options:"))
	fmt.Fprintln(out, DimStyle.Render("  close them · `--kill` to end them first · `--force` to swap anyway · or `run`"))
	printInstances(out, live)
}

// nextAccount returns the account after the active one in list order (wrapping),
// for a bare `switch`.
func nextAccount(st *account.Store, active string) (account.Account, error) {
	all, err := st.List()
	if err != nil {
		return account.Account{}, err
	}
	if len(all) == 0 {
		return account.Account{}, errors.New("no accounts to rotate between")
	}
	for i, a := range all {
		if a.ID == active {
			return all[(i+1)%len(all)], nil
		}
	}
	return all[0], nil
}

func printInstances(w interface{ Write([]byte) (int, error) }, live []account.Instance) {
	for _, inst := range live {
		fmt.Fprintf(w, "  %s\n", DimStyle.Render(fmt.Sprintf("• pid %d  %s", inst.PID, inst.Kind)))
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

func accountTitle(a account.Account) string {
	name := a.Label
	if name == "" {
		name = a.ID
	} else if a.Label != a.ID {
		name = fmt.Sprintf("%s (%s)", a.Label, a.ID)
	}
	if a.SubscriptionType != "" {
		name += DimStyle.Render(" · " + a.SubscriptionType)
	}
	return name
}
