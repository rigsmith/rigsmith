package commands

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
)

// `account prepare` is `run` without the exec: it makes an account's session
// profile ready and hands back the CLAUDE_CONFIG_DIR, so a program that spawns
// `claude` itself (Tweed is the first) can launch under that account without
// re-deriving what EnsureSession learned the hard way — when to seed, when to
// leave a self-refreshed token alone, what to share in. Until this existed the
// only way to get a ready profile was to go through `run`, which owns the
// terminal; a launcher that constructed the path by hand got a directory that
// might never have been seeded.
//
// It never touches the machine-wide login. The prepared profile is the same one
// `run` would use, so a session started by a launcher and one started from a
// terminal are the same account with the same history.

// Stable refusal codes for `prepare --json`. A launcher branches on these; the
// sentence lives in `message` and may change wording freely.
const (
	prepareNoSuchAccount  = "no-such-account"    // nothing matches the reference (or there are no accounts at all)
	prepareAmbiguous      = "ambiguous-account"  // the reference matches more than one account
	prepareUnmapped       = "unmapped-directory" // no reference and no directory mapping
	prepareNoTokens       = "no-tokens"          // the stored credential has nothing to seed the profile with
	prepareSessionUnknown = "session-unknown"    // the profile's credential could not be read (locked Keychain)
	prepareFailed         = "failed"             // anything else; read message
)

// prepareJSON reports the outcome — including the refusals, which are what a
// launcher most needs to branch on before it spawns anything.
type prepareJSON struct {
	Prepared bool   `json:"prepared"`
	ID       string `json:"id,omitempty"`
	Email    string `json:"email,omitempty"`
	Alias    string `json:"alias,omitempty"`
	// ConfigDir is the value to export as CLAUDE_CONFIG_DIR. Absent on refusal.
	ConfigDir string `json:"configDir,omitempty"`
	// Session is the profile's state after preparation, in `list --json`'s
	// vocabulary. On a success it is always "ok" — anything else is refused
	// with the matching reason, because "prepared" is what the launcher spawns
	// on. Kept as a field so the object reads like `list`'s.
	Session string `json:"session,omitempty"`
	// Shared is the sharing mode the profile was prepared with: true unless
	// --no-share. It says what was asked for, not an inventory of what was
	// linked — an entry absent from ~/.claude is skipped either way.
	Shared bool `json:"shared"`
	// Reason is a stable code from the list above — branch on this.
	Reason string `json:"reason,omitempty"`
	// Message is the human sentence behind Reason. Never parse it.
	Message string `json:"message,omitempty"`
}

func newAccountPrepareCmd() *cobra.Command {
	var noShare, asJSON bool
	cmd := &cobra.Command{
		Use:   "prepare [<id|email|alias>]",
		Short: "Make an account's session profile ready and print its CLAUDE_CONFIG_DIR",
		Long: "For programs that launch `claude` themselves. Does everything `run` does\n" +
			"short of starting Claude Code — seeds the profile's credential if it needs\n" +
			"it, leaves a live profile's own refreshed token alone, links ~/.claude\n" +
			"customizations in (--no-share skips that; links an earlier shared run\n" +
			"made are kept) — and prints the CLAUDE_CONFIG_DIR to export. Never\n" +
			"touches your machine-wide login.\n\n" +
			"With no account named, uses the one mapped to this directory\n" +
			"(`clauderig account map`), inheriting the nearest mapped ancestor.\n\n" +
			"--json emits one object on stdout, refusals included, with a stable\n" +
			"`reason` code; every human line goes to stderr.",
		Args: cobra.MaximumNArgs(1),
		// A refusal is an outcome, not a usage error — and with --json the
		// usage text would land on stdout after the object, breaking the one
		// promise a launcher relies on.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) > 0 {
				ref = args[0]
			}
			return runPrepare(cmd, ref, !noShare, asJSON)
		},
	}
	cmd.Flags().BoolVar(&noShare, "no-share", false, "don't link ~/.claude customizations into the profile (links an earlier shared run made are kept)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the outcome as JSON (one object on stdout; prose on stderr)")
	return cmd
}

func runPrepare(cmd *cobra.Command, ref string, share, asJSON bool) error {
	// stdout carries the VALUE and nothing else — one object with --json, the
	// bare directory without — so `$(clauderig account prepare work)` and a
	// JSON reader never have to strip prose. Every human line goes to stderr in
	// both modes.
	report := func(prepareJSON) error { return nil }
	if asJSON {
		report = func(j prepareJSON) error { return emitJSON(cmd.OutOrStdout(), j) }
	}
	refuse := func(a account.Account, reason string, err error) error {
		// The refusal is the outcome a launcher branches on, so a failure to
		// deliver it is reported alongside the refusal itself, never instead.
		if rerr := report(prepareJSON{
			ID: a.ID, Email: a.Email, Alias: a.Alias,
			Shared: share, Reason: reason, Message: err.Error(),
		}); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	}

	st, err := account.DefaultStore()
	if err != nil {
		return refuse(account.Account{}, prepareFailed, err)
	}
	a, err := sessionAccount(cmd, st, ref)
	if err != nil {
		return refuse(account.Account{}, classifyPrepareResolve(err), err)
	}
	warnIfActive(cmd, st, a)
	home, err := account.ClaudeHome()
	if err != nil {
		return refuse(a, prepareFailed, err)
	}
	dir, err := st.EnsureSession(a, share, home)
	if err != nil {
		return refuse(a, classifyPrepareFailure(err), err)
	}
	// Re-read the profile AFTER preparing it, and let only a profile that can
	// authenticate through. EnsureSession holds no lock, so a concurrent
	// `remove`/`purge`/re-login can change the answer between the seed and the
	// report — and "prepared" is a promise the launcher spawns on. Anything but
	// "ok" is therefore a refusal with the matching code, never a status.
	session := st.SessionStatus(a.ID)
	switch session {
	case account.SessionOK:
	case account.SessionNoTokens:
		return refuse(a, prepareNoTokens, fmt.Errorf("the profile at %s has no usable credential after preparation", dir))
	case account.SessionUnknown:
		return refuse(a, prepareSessionUnknown, fmt.Errorf("%w back after preparation", account.ErrSessionUnreadable))
	case account.SessionNone:
		return refuse(a, prepareFailed, fmt.Errorf("the profile at %s disappeared while it was being prepared", dir))
	default:
		return refuse(a, prepareFailed, fmt.Errorf("unexpected session status %q", session))
	}

	if err := report(prepareJSON{
		Prepared: true, ID: a.ID, Email: a.Email, Alias: a.Alias,
		ConfigDir: dir, Session: session, Shared: share,
	}); err != nil {
		return err
	}
	if !asJSON {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", DimStyle.Render("prepared:"), accountTitle(a))
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), dir); err != nil {
			return err
		}
	}
	return nil
}

// sessionAccount picks the account a session command (`run`, `prepare`) acts
// on: the named one, or — with no name — the one mapped to the working
// directory. An unmapped directory is an error rather than a silent fallback to
// the live login: both commands promise an isolated profile, and quietly handing
// over the machine-wide one instead is exactly the surprise they exist to avoid.
// A mapping that exists but cannot be honoured is its own error, not "unmapped".
// Notes go to stderr: stdout is the value in both commands.
func sessionAccount(cmd *cobra.Command, st *account.Store, ref string) (account.Account, error) {
	if ref != "" {
		return st.Resolve(ref)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return account.Account{}, err
	}
	mapped, ok, err := mappedAccount(st, cwd)
	if err != nil {
		return account.Account{}, err
	}
	if !ok {
		return account.Account{}, fmt.Errorf("%w: no account named, and this directory is not mapped to one.\n"+
			"Name it (`clauderig account %s <id|email|alias>`), or bind this directory "+
			"with `clauderig account map <id|email|alias>`", errUnmappedDirectory, cmd.Name())
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n", DimStyle.Render("mapped:"), DimStyle.Render(cwd))
	return mapped, nil
}

var errUnmappedDirectory = errors.New("unmapped directory")

// classifyPrepareResolve names why no account was picked. Only an explicit miss
// is "no-such-account": an ambiguous reference, an unreadable directory map, a
// mapped account that no longer resolves, or a failed Getwd are different
// problems with different fixes, and calling them "no such account" would send
// a launcher's user to add an account they may already have.
func classifyPrepareResolve(err error) string {
	switch {
	case errors.Is(err, errUnmappedDirectory):
		return prepareUnmapped
	case errors.Is(err, account.ErrNoSuchAccount), errors.Is(err, account.ErrNoAccounts):
		return prepareNoSuchAccount
	case errors.Is(err, account.ErrAmbiguousRef):
		return prepareAmbiguous
	default:
		return prepareFailed
	}
}

// classifyPrepareFailure maps an EnsureSession error onto a stable code. Both
// interesting failures are sentinels the store wraps, so this never matches
// prose — and falls back to "failed" rather than guessing, so a launcher can
// always rely on the code being one it knows or the catch-all.
func classifyPrepareFailure(err error) string {
	switch {
	case errors.Is(err, account.ErrStoredNoTokens):
		return prepareNoTokens
	case errors.Is(err, account.ErrSessionUnreadable):
		return prepareSessionUnknown
	default:
		return prepareFailed
	}
}
