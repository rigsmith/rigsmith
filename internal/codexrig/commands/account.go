package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/spf13/cobra"
)

// NewAccountCmd builds the `codexrig account` group: several Codex logins on one
// machine, each with its own isolated CODEX_HOME, plus the machine-wide swap.
func NewAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "account",
		Aliases: []string{"acct"},
		Short:   "Manage several Codex logins on this machine",
		Long: "Keep more than one Codex login on one machine.\n\n" +
			"`add` captures the login you are currently signed in as. `run` starts Codex\n" +
			"under a chosen account without disturbing the others — each gets its own\n" +
			"CODEX_HOME, so two can run at once. `switch` changes which login plain\n" +
			"`codex` uses.\n\n" +
			"Nothing here logs you out, and no credential ever leaves this machine:\n" +
			"auth.json is never synced.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newAccountAddCmd(),
		newAccountListCmd(),
		newAccountRunCmd(),
		NewAccountPrepareCmd(),
		newAccountSwitchCmd(),
		newAccountSessionsCmd(),
		newAccountRemoveCmd(),
		newAccountPurgeCmd(),
		newAccountDoctorCmd(),
		newAccountAliasCmd(),
		newAccountDisableCmd(),
		newAccountEnableCmd(),
		newAccountMapCmd(),
		newAccountUnmapCmd(),
	)
	return cmd
}

// openStore is the one place a command reaches the account store, so a refusal
// about the environment is stated once rather than in a dozen RunE bodies.
func openStore() (*account.Store, error) { return account.DefaultStore() }

// refuseIfRelocated stops a command that acts on the MACHINE's login while
// CODEX_HOME points at something else. Without it, `account add` run inside an
// `account run` session files that account's credential under whatever name the
// enclosing shell implies — one login's token stored under another's identity,
// which is unrecoverable without noticing it happened.
func refuseIfRelocated(verb string) error {
	home, err := codexhome.Default()
	if err != nil {
		return err
	}
	if env, set := codexhome.Env(); set && !codexhome.SameDir(env, home) {
		return fmt.Errorf("%s points at %s, not this machine's Codex home — `account %s` acts on the machine-wide login; unset it first", codexhome.EnvHome, env, verb)
	}
	return nil
}

func newAccountAddCmd() *cobra.Command {
	var fromHome string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Track the Codex login this machine is signed in as",
		Long: "Records the current `codex` login so codexrig can run it, switch to it and\n" +
			"name it later. The credential is copied into codexrig's own store and never\n" +
			"leaves this machine.\n\n" +
			"--from-home re-reads an account's credential out of its own isolated home\n" +
			"instead — the repair for an account you logged in via `codexrig account run`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			s, err := openStore()
			if err != nil {
				return err
			}
			if fromHome != "" {
				a, err := s.Resolve(fromHome)
				if err != nil {
					return err
				}
				if err := s.CaptureFromHome(a); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s %s from its own home\n", OkStyle.Render("Recaptured"), accountTitle(a))
				return nil
			}
			if err := refuseIfRelocated("add"); err != nil {
				return err
			}
			cred, err := account.ReadLive()
			if err != nil {
				return err
			}
			a, existed, err := s.CaptureLive(cred)
			if err != nil {
				return err
			}
			if err := s.SetActive(a.ID); err != nil {
				return err
			}
			verb := "Added"
			if existed {
				verb = "Updated"
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render(verb), accountTitle(a))
			fmt.Fprintf(out, "  %s\n", DimStyle.Render("run it with `codexrig account run "+a.ID+"`"))
			return nil
		},
	}
	cmd.Flags().StringVar(&fromHome, "from-home", "", "re-read this account's credential from its own isolated home")
	return cmd
}

// accountTitle is the styled listing label: the alias leads, then the email,
// then the plan. Dimming everything after the name keeps a column of these
// scannable when they differ only in the tail.
func accountTitle(a account.Account) string {
	name := a.Email
	if name == "" {
		name = a.ID
	}
	title := name
	if a.Alias != "" {
		title = a.Alias + DimStyle.Render(" · "+name)
	}
	if a.PlanType != "" {
		title += DimStyle.Render(" · " + a.PlanType)
	}
	return title
}

// accountJSON is one row of `account list --json`.
//
// The field names are clauderig's, not new ones, and that is the contract rather
// than an accident: a caller that reads accounts from either rig — and that
// falls back to accounts/<id>/meta.json directly when the binary is absent —
// parses ONE shape. `subscriptionType` is the plan, `session` is the
// state of the isolated profile directory. Renaming either to something more
// Codex-flavoured would fork a working integration for a nicer noun.
type accountJSON struct {
	ID               string `json:"id"`
	Email            string `json:"email,omitempty"`
	Name             string `json:"name,omitempty"`
	Alias            string `json:"alias,omitempty"`
	AccountID        string `json:"accountId,omitempty"`
	SubscriptionType string `json:"subscriptionType,omitempty"`
	AuthMode         string `json:"authMode,omitempty"`
	Active           bool   `json:"active"`
	Disabled         bool   `json:"disabled"`
	CredentialTokens bool   `json:"credentialTokens"`
	Session          string `json:"session"`
}

type accountListJSON struct {
	Active string `json:"active"`
	// Desynced means codexrig's pointer names a different login than the
	// credential does — the same question clauderig's flag of this name asks,
	// even though the shape of the disagreement differs between the two CLIs.
	Desynced bool          `json:"desynced"`
	Accounts []accountJSON `json:"accounts"`
	// Problems carries the doctor's findings so a caller polling `list --json`
	// does not need a second invocation to learn the picture is inconsistent.
	Problems []string `json:"problems,omitempty"`
}

func newAccountListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "status"},
		Short:   "List the Codex logins codexrig tracks",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			s, err := openStore()
			if err != nil {
				return err
			}
			rows, err := s.StoredStatuses()
			if err != nil {
				return err
			}
			obs := s.Diagnose()

			if asJSON {
				doc := accountListJSON{
					Accounts: []accountJSON{},
					Desynced: obs.PointerEmail != "",
					Problems: obs.Problems(),
				}
				for _, r := range rows {
					if r.Active {
						doc.Active = r.ID
					}
					doc.Accounts = append(doc.Accounts, accountJSON{
						ID: r.ID, Email: r.Email, Name: r.Name, Alias: r.Alias,
						AccountID: r.AccountID, SubscriptionType: r.PlanType, AuthMode: r.AuthMode,
						Active: r.Active, Disabled: r.Disabled,
						CredentialTokens: r.CredentialTokens, Session: r.Home,
					})
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(doc)
			}

			if len(rows) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no accounts yet — run `codexrig account add` while logged in"))
				return nil
			}
			fmt.Fprintln(out, HeaderStyle.Render("Codex logins"))
			for _, r := range rows {
				marker := "  "
				if r.Active {
					marker = AccentStyle.Render("→ ")
				}
				line := marker + accountTitle(r.Account)
				if !r.CredentialTokens {
					line += " " + ErrStyle.Render("✗ stored credential cannot authenticate")
				}
				if r.Disabled {
					line += " " + DimStyle.Render("(disabled)")
				}
				fmt.Fprintln(out, line)
			}
			for _, p := range obs.Problems() {
				fmt.Fprintf(out, "\n%s %s\n", WarnStyle.Render("!"), p)
			}
			if obs.EnvHome != "" {
				fmt.Fprintf(out, "\n%s\n", DimStyle.Render(codexhome.EnvHome+" points at "+obs.EnvHome+" — this shell is not using the machine's Codex home"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the listing as JSON (for scripts and pollers)")
	return cmd
}

func newAccountRunCmd() *cobra.Command {
	var noShare bool
	cmd := &cobra.Command{
		Use:   "run [<id|email|alias>] [-- codex args...]",
		Short: "Run Codex under one account, without disturbing the others",
		Long: "Starts `codex` with CODEX_HOME pointed at that account's own directory, so it\n" +
			"authenticates as that login and keeps its own sessions and thread history.\n" +
			"Several accounts can run at once, and the machine-wide login is untouched.\n\n" +
			"Your setup is shared in by default — config.toml, AGENTS.md, skills, rules —\n" +
			"so the accounts differ only in who they are signed in as. --no-share gives the\n" +
			"account an entirely separate setup.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// ArgsLenAtDash keeps `run -- --help` from being read as an account
			// reference: everything past the dash belongs to Codex.
			ref := ""
			passthrough := args
			if d := cmd.ArgsLenAtDash(); d >= 0 {
				if d > 0 {
					ref = args[0]
				}
				passthrough = args[d:]
			} else if len(args) > 0 {
				ref = args[0]
				passthrough = args[1:]
			}

			s, err := openStore()
			if err != nil {
				return err
			}
			a, err := resolveAccountRef(s, ref)
			if err != nil {
				return err
			}
			home, err := s.EnsureHome(a, !noShare)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%s %s (%s=%s)\n",
				DimStyle.Render("session:"), a.Title(), codexhome.EnvHome, home)
			return runCodex(home, passthrough)
		},
	}
	cmd.Flags().BoolVar(&noShare, "no-share", false, "give this account its own setup instead of sharing the machine's")
	return cmd
}

// resolveAccountRef picks the account a command should act on: the one named,
// the one bound to this directory, or the single enabled account. It refuses to
// guess between several — running the wrong login is worse than being asked
// which.
func resolveAccountRef(s *account.Store, ref string) (account.Account, error) {
	if ref != "" {
		return s.Resolve(ref)
	}
	// A directory binding is an answer the user already gave, so it outranks
	// "there happens to be only one".
	if cwd, err := os.Getwd(); err == nil {
		if id := mappedAccount(cwd); id != "" {
			if a, rerr := s.Resolve(id); rerr == nil {
				return a, nil
			}
		}
	}
	enabled, err := s.Enabled()
	if err != nil {
		return account.Account{}, err
	}
	switch len(enabled) {
	case 0:
		return account.Account{}, account.ErrNoAccounts
	case 1:
		return enabled[0], nil
	default:
		names := make([]string, 0, len(enabled))
		for _, a := range enabled {
			names = append(names, a.ID)
		}
		sort.Strings(names)
		return account.Account{}, fmt.Errorf("%w: %s — name one, or bind this directory with `codexrig account map <account>`", account.ErrUnmapped, strings.Join(names, ", "))
	}
}

// runCodex execs the Codex CLI with its home relocated, inheriting stdio and
// propagating the exit code so a wrapper is invisible to whatever called it.
func runCodex(home string, args []string) error {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return errors.New("`codex` not found on PATH")
	}
	c := exec.Command(bin, args...)
	c.Env = append(os.Environ(), codexhome.EnvHome+"="+home)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

func newAccountSessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "sessions",
		Aliases: []string{"ps"},
		Short:   "Show the Codex processes running against this machine's home",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			home, err := codexhome.Default()
			if err != nil {
				return err
			}
			insts, scanErr := account.RunningInstancesScan(home)
			if scanErr != nil {
				fmt.Fprintf(out, "%s %s\n", WarnStyle.Render("!"), "could not scan the process table — this list may be incomplete")
			}
			if len(insts) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no Codex processes running against this home"))
				return nil
			}
			fmt.Fprintf(out, "%d Codex process(es) running\n", len(insts))
			for _, in := range insts {
				fmt.Fprintf(out, "  • pid %d  %s\n", in.PID, DimStyle.Render(in.Kind))
			}
			return nil
		},
	}
}

func newAccountRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <id|email|alias>",
		Aliases: []string{"rm"},
		Short:   "Forget an account (does not log it out)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			a, err := s.Resolve(args[0])
			if err != nil {
				return err
			}
			if !interactive() {
				return errors.New("refusing to remove without a terminal to confirm")
			}
			ok, err := confirm(fmt.Sprintf("Forget %s and delete its isolated home? (does not log it out)", a.Title()))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("aborted"))
				return nil
			}
			if err := s.Remove(a.ID); err != nil {
				return err
			}
			// A binding to an account that no longer exists would silently
			// resolve to nothing, which reads as "no account mapped here".
			if dm, derr := dirMap(); derr == nil {
				_ = dm.PruneAccount(a.ID)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", OkStyle.Render("Removed"), a.Title())
			return nil
		},
	}
}

func newAccountPurgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "purge",
		Short: "Forget every account (does not log any of them out)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore()
			if err != nil {
				return err
			}
			all, err := s.List()
			if err != nil {
				return err
			}
			if len(all) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("nothing to purge — no accounts tracked"))
				return nil
			}
			if !interactive() {
				return errors.New("refusing to purge without a terminal to confirm")
			}
			ok, err := confirm(fmt.Sprintf("Delete ALL %d tracked accounts and their isolated homes? (does not log out)", len(all)))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("aborted"))
				return nil
			}
			if err := s.Purge(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %d accounts\n", OkStyle.Render("Purged"), len(all))
			return nil
		},
	}
}

func newAccountAliasCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "alias [<id|email> <alias>]",
		Short: "Give an account a short name",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			s, err := openStore()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				all, err := s.List()
				if err != nil {
					return err
				}
				any := false
				for _, a := range all {
					if a.Alias != "" {
						if !any {
							fmt.Fprintln(out, HeaderStyle.Render("Aliases"))
							any = true
						}
						fmt.Fprintf(out, "  %-16s %s\n", a.Alias, a.Email)
					}
				}
				if !any {
					fmt.Fprintln(out, DimStyle.Render("no aliases yet — `codexrig account alias <id> <name>`"))
				}
				return nil
			}
			a, err := s.Resolve(args[0])
			if err != nil {
				return err
			}
			if unset {
				if err := s.ClearAlias(a.ID); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s alias removed from %s\n", OkStyle.Render("✓"), a.Email)
				return nil
			}
			if len(args) < 2 {
				return errors.New("give the alias to set, or --unset to remove it")
			}
			if err := s.SetAlias(a.ID, args[1]); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s alias %s → %s\n", OkStyle.Render("✓"), args[1], a.Email)
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "remove the alias instead of setting one")
	return cmd
}

func newAccountDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <id|email|alias>",
		Short: "Hold an account out of automatic selection",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return setDisabled(cmd, args[0], true) },
	}
}

func newAccountEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <id|email|alias>",
		Short: "Put a disabled account back into automatic selection",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return setDisabled(cmd, args[0], false) },
	}
}

func setDisabled(cmd *cobra.Command, ref string, disabled bool) error {
	s, err := openStore()
	if err != nil {
		return err
	}
	a, err := s.Resolve(ref)
	if err != nil {
		return err
	}
	if err := s.SetDisabled(a.ID, disabled); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if disabled {
		fmt.Fprintf(out, "%s disabled %s\n", OkStyle.Render("✓"), a.Email)
		fmt.Fprintf(out, "  %s\n", DimStyle.Render("skipped when codexrig picks an account for you; naming it explicitly still works"))
		if enabled, _ := s.Enabled(); len(enabled) == 0 {
			fmt.Fprintf(out, "  %s\n", WarnStyle.Render("every account is now disabled"))
		}
		return nil
	}
	fmt.Fprintf(out, "%s enabled %s\n", OkStyle.Render("✓"), a.Email)
	return nil
}

// writeJSON emits one object on stdout with the indentation every rigsmith
// --json surface uses.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
