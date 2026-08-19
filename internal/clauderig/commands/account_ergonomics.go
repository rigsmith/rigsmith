package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/dirmap"
	"github.com/spf13/cobra"
)

// dirmapStore is the one directory→identity table, shared by `account` and
// `desktop`: a directory that belongs to the work account usually wants the work
// Desktop window too, and one file means either command can show the whole
// picture. It sits under ~/.clauderig, outside every sync root, because the
// absolute paths in it mean nothing on another machine.
func dirmapStore() (*dirmap.Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return dirmap.New(filepath.Join(home, ".clauderig", "dir-map.json")), nil
}

// resolveDirArg turns an optional directory argument into an absolute path,
// defaulting to the working directory — the case `map`/`unmap` are used in most.
func resolveDirArg(args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return filepath.Abs(args[0])
	}
	return os.Getwd()
}

// --- JSON output ------------------------------------------------------------
//
// One object on stdout, human notices on stderr, so `clauderig account list
// --json | jq` works without the caller filtering prose out of the stream.

type accountJSON struct {
	ID               string `json:"id"`
	Email            string `json:"email"`
	Alias            string `json:"alias,omitempty"`
	SubscriptionType string `json:"subscriptionType,omitempty"`
	OrganizationUUID string `json:"organizationUuid,omitempty"`
	Active           bool   `json:"active"`
	Disabled         bool   `json:"disabled"`
	CredentialTokens bool   `json:"credentialTokens"`
	Session          string `json:"session"`
}

func toAccountJSON(st account.StoredStatus) accountJSON {
	return accountJSON{
		ID:               st.ID,
		Email:            st.Email,
		Alias:            st.Alias,
		SubscriptionType: st.SubscriptionType,
		OrganizationUUID: st.OrganizationUUID,
		Active:           st.Active,
		Disabled:         st.Disabled,
		CredentialTokens: st.CredentialTokens,
		Session:          st.Session,
	}
}

type accountListJSON struct {
	Active   string        `json:"active"`
	Accounts []accountJSON `json:"accounts"`
}

// emitJSON writes one indented object and a trailing newline.
func emitJSON(w interface{ Write([]byte) (int, error) }, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(body, '\n'))
	return err
}

func printAccountsJSON(w interface{ Write([]byte) (int, error) }, st *account.Store) error {
	statuses, err := st.StoredStatuses()
	if err != nil {
		return err
	}
	active, _ := st.Active()
	out := accountListJSON{Active: active, Accounts: make([]accountJSON, 0, len(statuses))}
	for _, s := range statuses {
		out.Accounts = append(out.Accounts, toAccountJSON(s))
	}
	return emitJSON(w, out)
}

// Stable refusal codes for `switch --json`. A script branches on these; the
// human sentence lives in `message` and may change wording freely.
const (
	switchAlreadyLive  = "already-live"  // the target is already the live account
	switchLiveSessions = "live-sessions" // Claude Code is running (see blocking)
	switchNoTokens     = "no-tokens"     // the stored credential would log the machine out
	switchNoProfile    = "no-profile"    // no stored oauthAccount block to move with it
	switchDesynced     = "stored-desync" // the stored pair names two different accounts
	switchNoConfig     = "no-global-config"
	switchClaudeBusy   = "claude-busy" // a credential lock is held (refresh in flight)
	switchScanFailed   = "process-scan-failed"
	switchFailed       = "failed" // anything else; read message
)

// switchJSON reports the outcome of a switch — including the refusals, which are
// the interesting cases for a script deciding what to do next.
type switchJSON struct {
	Switched bool   `json:"switched"`
	DryRun   bool   `json:"dryRun,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to"`
	ToEmail  string `json:"toEmail,omitempty"`
	Backup   string `json:"backup,omitempty"`
	// Reason is a stable code from the list above — branch on this.
	Reason string `json:"reason,omitempty"`
	// Message is the human sentence behind Reason. Never parse it.
	Message  string         `json:"message,omitempty"`
	Blocking []instanceJSON `json:"blocking,omitempty"`
}

// classifySwitchFailure maps a doSwitch error onto a stable code. The errors are
// constructed in one file a few hundred lines away, so this matches on the
// sentinel where there is one and on a distinctive fragment otherwise — and
// falls back to "failed" rather than guessing, so a caller can always rely on
// the code being one it knows or the catch-all.
func classifySwitchFailure(err error) string {
	switch {
	case errors.Is(err, account.ErrClaudeBusy):
		return switchClaudeBusy
	case errors.Is(err, account.ErrProcessScan):
		return switchScanFailed
	case strings.Contains(err.Error(), "has no tokens"):
		return switchNoTokens
	case strings.Contains(err.Error(), "no stored account profile"):
		return switchNoProfile
	case strings.Contains(err.Error(), "is itself desynced"):
		return switchDesynced
	case strings.Contains(err.Error(), "~/.claude.json does not exist"):
		return switchNoConfig
	default:
		return switchFailed
	}
}

type instanceJSON struct {
	PID  int    `json:"pid"`
	Kind string `json:"kind"`
	CWD  string `json:"cwd,omitempty"`
}

func toInstancesJSON(live []account.Instance) []instanceJSON {
	out := make([]instanceJSON, 0, len(live))
	for _, i := range live {
		out = append(out, instanceJSON{PID: i.PID, Kind: i.Kind, CWD: i.Cwd})
	}
	return out
}

// --- alias ------------------------------------------------------------------

func newAccountAliasCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "alias [<id|email> <alias>]",
		Short: "Give an account a short handle (usable anywhere an id or email is)",
		Long: "An alias is a short name you pick — `switch dev` instead of\n" +
			"`switch john@company-with-a-long-domain.com`. It works anywhere an id or\n" +
			"email does: switch, run, remove, map.\n\n" +
			"With no arguments, lists the aliases in use. An alias that would shadow\n" +
			"another account's id, email or alias is refused, because the shadowing\n" +
			"would silently redirect a switch.",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return listAliases(out, st)
			}
			a, err := st.Resolve(args[0])
			if err != nil {
				return err
			}
			if unset {
				if aerr := st.ClearAlias(a.ID); aerr != nil {
					return aerr
				}
				fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ alias removed from"), a.Email)
				return nil
			}
			if len(args) < 2 {
				return errors.New("give the alias to set, or --unset to remove it")
			}
			if aerr := st.SetAlias(a.ID, args[1]); aerr != nil {
				return aerr
			}
			fmt.Fprintf(out, "%s %s → %s\n", OkStyle.Render("✓ alias"), args[1], a.Email)
			return nil
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "remove the account's alias")
	return cmd
}

func listAliases(out interface{ Write([]byte) (int, error) }, st *account.Store) error {
	all, err := st.List()
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
			fmt.Fprintf(out, "  %s  %s\n", a.Alias, DimStyle.Render(a.Email))
		}
	}
	if !any {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("no aliases yet — `clauderig account alias <id|email> <alias>` sets one"))
	}
	return nil
}

// --- disable / enable -------------------------------------------------------

func newAccountDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <id|email|alias>",
		Short: "Hold an account out of automatic rotation (keeps it logged in and tracked)",
		Long: "A disabled account is skipped by a bare `clauderig account switch`, which\n" +
			"rotates to the next account — for a work login nothing should land on by\n" +
			"accident, or one you are resting.\n\n" +
			"It stays fully tracked: its credential is still stored and refreshed, and\n" +
			"`switch <name>` still switches to it. This is not a soft delete; `remove`\n" +
			"is how you stop tracking an account.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return setDisabled(cmd, args[0], true) },
	}
}

func newAccountEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <id|email|alias>",
		Short: "Return a disabled account to automatic rotation",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return setDisabled(cmd, args[0], false) },
	}
}

func setDisabled(cmd *cobra.Command, ref string, disabled bool) error {
	out := cmd.OutOrStdout()
	st, err := account.DefaultStore()
	if err != nil {
		return err
	}
	a, err := st.Resolve(ref)
	if err != nil {
		return err
	}
	if err := st.SetDisabled(a.ID, disabled); err != nil {
		return err
	}
	if disabled {
		fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ disabled"), a.Email)
		fmt.Fprintf(out, "%s\n", DimStyle.Render("  skipped by a bare `switch`; `switch "+a.ID+"` still works"))
		// Rotation with nothing left to rotate to is a state worth naming now
		// rather than at the moment someone needs to switch.
		if enabled, eerr := st.Enabled(); eerr == nil && len(enabled) == 0 {
			fmt.Fprintf(out, "%s\n", WarnStyle.Render("  every account is now disabled — a bare `switch` has nowhere to go"))
		}
		return nil
	}
	fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ enabled"), a.Email)
	return nil
}

// --- map / unmap ------------------------------------------------------------

func newAccountMapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "map [<id|email|alias>] [dir]",
		Short: "Bind a directory to an account, so a bare `account run` there uses it",
		Long: "Maps a directory (the working directory by default) to an account. A bare\n" +
			"`clauderig account run` inside it then launches that account in session\n" +
			"mode — work account in work repos, personal elsewhere.\n\n" +
			"Subdirectories inherit the nearest mapped ancestor, so mapping a repo root\n" +
			"covers the whole tree and a mapping deeper inside still wins.\n\n" +
			"With no arguments, lists the mappings. Mappings are per-machine and are\n" +
			"never synced — they name absolute paths that mean nothing elsewhere. The\n" +
			"same table holds Desktop bindings (`clauderig desktop map`).",
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dm, err := dirmapStore()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return printMappings(out, dm)
			}
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			a, err := st.Resolve(args[0])
			if err != nil {
				return err
			}
			dir, err := resolveDirArg(args[1:])
			if err != nil {
				return err
			}
			entry, err := dm.Set(dir, func(e *dirmap.Entry) { e.Account = a.ID })
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s → %s\n", OkStyle.Render("✓ mapped"), entry.Dir, accountTitle(a))
			fmt.Fprintf(out, "%s\n", DimStyle.Render("  `clauderig account run` there now launches this account"))
			return nil
		},
	}
	return cmd
}

func newAccountUnmapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unmap [dir]",
		Short: "Remove a directory's account binding (defaults to the working directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			dm, err := dirmapStore()
			if err != nil {
				return err
			}
			dir, err := resolveDirArg(args)
			if err != nil {
				return err
			}
			// Clear only the account binding: the directory may also name a
			// Desktop profile, and `account unmap` has no business dropping it.
			entry, err := dm.Set(dir, func(e *dirmap.Entry) { e.Account = "" })
			if err != nil {
				return err
			}
			if entry.Desktop != "" {
				fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ account binding removed from"), entry.Dir)
				fmt.Fprintf(out, "%s\n", DimStyle.Render("  its Desktop profile binding ("+entry.Desktop+") is untouched"))
				return nil
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ unmapped"), entry.Dir)
			return nil
		},
	}
}

func printMappings(out interface{ Write([]byte) (int, error) }, dm *dirmap.Store) error {
	all, err := dm.List()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("no directory mappings — `clauderig account map <id|email>` maps this directory"))
		return nil
	}
	fmt.Fprintln(out, HeaderStyle.Render("Directory mappings"))
	for _, e := range all {
		var bindings []string
		if e.Account != "" {
			bindings = append(bindings, "account "+e.Account)
		}
		if e.Desktop != "" {
			bindings = append(bindings, "desktop "+e.Desktop)
		}
		fmt.Fprintf(out, "  %s\n    %s\n", e.Dir, DimStyle.Render(joinWords(bindings, " · ")))
	}
	return nil
}

func joinWords(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// mappedAccount resolves the account bound to dir, for a bare `account run`.
func mappedAccount(st *account.Store, dir string) (account.Account, bool) {
	dm, err := dirmapStore()
	if err != nil {
		return account.Account{}, false
	}
	entry, err := dm.Lookup(dir)
	if err != nil || entry.Account == "" {
		return account.Account{}, false
	}
	a, rerr := st.Resolve(entry.Account)
	if rerr != nil {
		return account.Account{}, false
	}
	return a, true
}

// pruneMappingsForAccount drops a removed account's bindings.
//
// Called from EVERY path that removes an account — the CLI command, the
// interactive screen, and `purge` — because the promise is that a mapping never
// outlives its target, and a guarantee honoured on one path out of three is not
// a guarantee. Best effort: the account is already gone, and failing the removal
// over a stale mapping would be worse than the stale mapping.
func pruneMappingsForAccount(id string) {
	if dm, err := dirmapStore(); err == nil {
		_ = dm.PruneAccount(id)
	}
}
