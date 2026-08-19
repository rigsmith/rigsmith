package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
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
			"Claude Code only. Claude Desktop keeps its own separate login and is\n" +
			"never read or written here — switching accounts does not sign Desktop\n" +
			"in or out.\n\n" +
			"  add     capture the currently logged-in account into claudeRig's store\n" +
			"  list    show stored accounts and which one is live\n" +
			"  run     launch Claude Code as an account in THIS terminal only\n" +
			"          (isolated, self-refreshing — never touches your live login)\n" +
			"  switch  change the machine-wide login (guarded: refuses while Claude runs)\n" +
			"  alias   give an account a short handle (`switch dev`)\n" +
			"  map     bind a directory to an account, for a bare `run` there\n" +
			"  disable hold an account out of automatic rotation\n" +
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
		newAccountSwitchCmd(), newAccountSessionsCmd(), newAccountRemoveCmd(), newAccountPurgeCmd(),
		newAccountDoctorCmd(), newAccountWatchCmd(),
		newAccountAliasCmd(), newAccountDisableCmd(), newAccountEnableCmd(),
		newAccountMapCmd(), newAccountUnmapCmd())
	return cmd
}

// printDiagnosis renders an observation: both halves of the identity side by
// side, then any problems. The two-column shape is the point — the whole class of
// bug is that these are separate writes that nothing reconciles.
func printDiagnosis(out interface{ Write([]byte) (int, error) }, o account.Observation) {
	fmt.Fprintln(out, HeaderStyle.Render("Claude Code identity"))

	cred := "unreadable"
	if o.CredErr == "" {
		cred = fmt.Sprintf("org %s  %s", o.CredOrg, o.CredSubscription)
	}
	fmt.Fprintf(out, "  %s  %s\n", "credential      ", cred)
	fmt.Fprintf(out, "  %s  %s\n", DimStyle.Render("                "),
		DimStyle.Render("keychain/file — what the SERVER authenticates you as"))

	block := "(absent)"
	if o.BlockEmail != "" || o.BlockOrg != "" {
		block = fmt.Sprintf("%s  org %s", o.BlockEmail, o.BlockOrg)
	}
	fmt.Fprintf(out, "  %s  %s\n", "profile block   ", block)
	fmt.Fprintf(out, "  %s  %s\n", DimStyle.Render("                "),
		DimStyle.Render("~/.claude.json oauthAccount — what Claude Code DISPLAYS"))

	if o.ActiveID != "" {
		fmt.Fprintf(out, "  %s  %s  (%s)\n", "clauderig active", o.ActiveEmail, o.ActiveID)
	}
	if o.ConfigModified != "" {
		fmt.Fprintf(out, "  %s  %s\n", DimStyle.Render("~/.claude.json  "),
			DimStyle.Render("last written "+o.ConfigModified))
	}

	problems := o.Problems()
	if len(problems) == 0 {
		fmt.Fprintf(out, "\n%s\n", OkStyle.Render("✓ both halves agree"))
		return
	}
	fmt.Fprintln(out)
	for _, p := range problems {
		fmt.Fprintf(out, "%s %s\n", ErrStyle.Render("✗"), p)
	}
	fmt.Fprintf(out, "\n%s\n", DimStyle.Render(
		"Fix: log in as the account you want (`claude` → /login), or `clauderig account switch <name>`,\n"+
			"     so the credential and the profile block move together. Never from inside a live session."))
}

// printStoredAccounts renders each tracked account's health: whether its stored
// credential would survive a `switch`, and whether its session profile can
// still authenticate.
func printStoredAccounts(out interface{ Write([]byte) (int, error) }, statuses []account.StoredStatus) {
	if len(statuses) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s\n", HeaderStyle.Render("Stored accounts"))
	anyDead := false
	for _, st := range statuses {
		marker := "  "
		if st.Active {
			marker = OkStyle.Render("→ ")
		}
		cred := OkStyle.Render("credential ✓")
		if !st.CredentialTokens {
			anyDead = true
			cred = ErrStyle.Render("credential ✗ no tokens")
		}
		var sess string
		switch st.Session {
		case account.SessionOK:
			sess = OkStyle.Render("session ✓")
		case account.SessionNoTokens:
			sess = ErrStyle.Render("session ✗ no tokens")
		case account.SessionUnknown:
			sess = WarnStyle.Render("session ? (keychain unreadable)")
		default:
			sess = DimStyle.Render("session —")
		}
		fmt.Fprintf(out, "%s%s  %s  %s\n", marker, accountTitle(st.Account), cred, sess)
	}
	if anyDead {
		fmt.Fprintf(out, "%s\n", DimStyle.Render(
			"  a token-less stored credential can't be switched to — repair with\n"+
				"  `clauderig account add --from-session <id>` (needs session ✓), or log in live and `account add`"))
	}
}

func newAccountDoctorCmd() *cobra.Command {
	var asJSON bool
	var showJournal int
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check whether the live credential and ~/.claude.json name the same account",
		Long: "Claude Code's identity is stored in two independent places: the credential\n" +
			"(Keychain, or ~/.claude/.credentials.json) and the oauthAccount block in\n" +
			"~/.claude.json. The server authenticates you as the credential; every screen\n" +
			"shows you the block. Nothing reconciles them, so they can drift — and when they\n" +
			"do, published artifacts, usage and rate limits silently land on an account the\n" +
			"UI never names.\n\n" +
			"Each run is recorded to ~/.clauderig/account-journal.jsonl when the identity\n" +
			"changed since the last observation. Exits non-zero on a desync.\n\n" +
			"Also lists every stored account with the health of its stored credential\n" +
			"(would `switch` accept it?) and its session profile (can it authenticate?).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			if showJournal != 0 {
				entries, jerr := st.Journal(showJournal)
				if jerr != nil {
					return jerr
				}
				if len(entries) == 0 {
					fmt.Fprintf(out, "%s\n", DimStyle.Render(
						"no observations recorded yet — run `clauderig account doctor` or `account watch`"))
					return nil
				}
				return printJournal(out, entries)
			}

			o := st.Diagnose()
			if _, rerr := st.Record(o); rerr != nil {
				// Diagnostics go to stderr: in --json mode a styled line on stdout
				// would leave machine consumers parsing something that is no longer
				// a single JSON value.
				fmt.Fprintf(cmd.ErrOrStderr(), "%s %v\n", WarnStyle.Render("could not record observation:"), rerr)
			}
			statuses, serr := st.StoredStatuses()
			if serr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s %v\n", WarnStyle.Render("could not read stored accounts:"), serr)
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				// Additive wrapper: every Observation field stays flat and
				// unchanged for existing consumers; only "accounts" is new.
				if err := enc.Encode(struct {
					account.Observation
					Accounts []account.StoredStatus `json:"accounts,omitempty"`
				}{o, statuses}); err != nil {
					return err
				}
			} else {
				printDiagnosis(out, o)
				printStoredAccounts(out, statuses)
			}
			if o.InSync {
				return nil
			}
			if !fix {
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"     `clauderig account doctor --fix` aligns the display to the credential without logging anyone out."))
				// Match `clauderig doctor`: the findings are already printed, so
				// signal failure by exit code rather than a duplicate error line.
				os.Exit(1)
			}

			repaired, ferr := st.RepairProfileBlock()
			if ferr != nil {
				return ferr
			}
			fmt.Fprintf(out, "\n%s %s\n", OkStyle.Render("Repaired — ~/.claude.json now names"), accountTitle(repaired))
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"The credential was NOT touched, so nothing was logged out. This makes the display\n"+
					"tell the truth about who you already are; to change account, use `account switch`."))
			after := st.Diagnose()
			if _, rerr := st.Record(after); rerr != nil {
				fmt.Fprintf(out, "%s %v\n", WarnStyle.Render("could not record observation:"), rerr)
			}
			if !after.InSync {
				fmt.Fprintln(out)
				printDiagnosis(out, after)
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the raw observation as JSON")
	cmd.Flags().IntVar(&showJournal, "journal", 0, "print the last N recorded observations instead of checking")
	cmd.Flags().BoolVar(&fix, "fix", false,
		"rewrite ~/.claude.json's profile block to match the live credential (never touches the credential, so nothing is logged out)")
	return cmd
}

func printJournal(out interface{ Write([]byte) (int, error) }, entries []account.Observation) error {
	fmt.Fprintln(out, HeaderStyle.Render("Identity change journal"))
	for _, e := range entries {
		mark := OkStyle.Render("✓")
		if !e.InSync {
			mark = ErrStyle.Render("✗")
		}
		fmt.Fprintf(out, "\n%s %s\n", mark, e.At)
		if e.BlockEmail != "" {
			fmt.Fprintf(out, "    block %s (org %s)\n", e.BlockEmail, e.BlockOrg)
		}
		if e.CredOrg != "" {
			fmt.Fprintf(out, "    cred  org %s\n", e.CredOrg)
		}
		for _, c := range e.Changed {
			fmt.Fprintf(out, "    %s %s\n", WarnStyle.Render("changed"), c)
		}
		// Changed is empty for the baseline entry and for any entry whose failure
		// isn't a field transition (an unreadable half, a stale active pointer), so
		// without this a red line could render as nothing but ✗ and a timestamp.
		if !e.InSync {
			for _, p := range e.Problems() {
				fmt.Fprintf(out, "    %s %s\n", ErrStyle.Render("problem"), p)
			}
		}
		if len(e.Live) > 0 {
			fmt.Fprintf(out, "    %s\n", DimStyle.Render("running at this moment:"))
			for _, in := range e.Live {
				fmt.Fprintf(out, "      %s\n", DimStyle.Render(
					fmt.Sprintf("• pid %d  %s  %s", in.PID, in.Kind, in.Cwd)))
			}
		}
	}
	return nil
}

func newAccountWatchCmd() *cobra.Command {
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch for an identity desync and record what was running when it happened",
		Long: "Polls both halves of the Claude Code identity and appends to\n" +
			"~/.clauderig/account-journal.jsonl whenever they change — recording the Claude\n" +
			"Code processes alive at that moment, so a flip can be attributed to the process\n" +
			"that caused it. Read-only: never writes a credential. Ctrl-C to stop.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			if every < time.Second {
				every = time.Second
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				fmt.Sprintf("watching every %s — Ctrl-C to stop", every)))

			// Always print the starting state so the log has a baseline, then
			// report only transitions.
			o := st.Diagnose()
			if _, rerr := st.Record(o); rerr != nil {
				fmt.Fprintf(out, "%s %v\n", WarnStyle.Render("could not record observation:"), rerr)
			}
			printDiagnosis(out, o)

			ticker := time.NewTicker(every)
			defer ticker.Stop()
			for {
				select {
				case <-cmd.Context().Done():
					return nil
				case <-ticker.C:
				}
				next := st.Diagnose()
				recorded, rerr := st.Record(next)
				if rerr != nil {
					fmt.Fprintf(out, "%s %v\n", WarnStyle.Render("could not record observation:"), rerr)
					continue
				}
				if !recorded {
					continue
				}
				fmt.Fprintf(out, "\n%s %s\n", WarnStyle.Render("IDENTITY CHANGED"), next.At)
				printDiagnosis(out, next)
				// Sampled at observation time, not at write time: this is a poll, so
				// the change may have happened up to one interval earlier and the
				// writer may already have exited. Report what was seen, and don't
				// let an empty list read as proof of anything.
				if len(next.Live) > 0 {
					fmt.Fprintf(out, "%s\n", DimStyle.Render("Claude Code running when the change was observed:"))
					printInstances(out, next.Live)
				} else {
					fmt.Fprintf(out, "%s\n", DimStyle.Render(
						"no Claude Code process was running when this was observed — but the change may have\n"+
							"occurred up to one poll interval earlier, so the writer could already have exited"))
				}
			}
		},
	}
	cmd.Flags().DurationVar(&every, "every", 5*time.Second, "poll interval")
	return cmd
}

func newAccountAddCmd() *cobra.Command {
	var fromSession string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Capture the currently logged-in account into claudeRig's store",
		Long: "Reads the live Claude Code credential and the account profile from\n" +
			"~/.claude.json (email + plan) and saves them under ~/.clauderig/accounts so\n" +
			"claudeRig can run or swap to this account later. Accounts are keyed by email;\n" +
			"the captured account becomes the tracked 'live' one. Log in first.\n\n" +
			"--from-session <id|email> instead repairs an already-tracked account's STORED\n" +
			"credential from its own session profile (the per-profile Keychain entry, or\n" +
			".credentials.json off macOS) — for when the stored copy lost its tokens but\n" +
			"the session still authenticates. It never touches the live login or the\n" +
			"active pointer.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			if fromSession != "" {
				a, err := st.Resolve(fromSession)
				if err != nil {
					return err
				}
				if err := st.CaptureFromSession(a); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s %s %s\n", OkStyle.Render("Recaptured"), accountTitle(a),
					DimStyle.Render("from its session profile"))
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"The session keeps rotating this token, so the snapshot can go stale — if a later\n"+
						"`switch` refuses it, re-run this capture (or log in live and `account add`)."))
				return nil
			}
			a, updated, err := captureCurrent(st)
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
			// `add` pairs a credential (Keychain) with an email read from
			// ~/.claude.json. If those two halves have drifted apart, what we just
			// stored is MISLABELED — one identity's email wrapped around the
			// other's token — and every later switch would propagate the lie. Warn
			// here, where a human is watching, rather than fail: refusing would
			// also block the capture-then-repair path.
			if o := st.Diagnose(); !o.InSync {
				fmt.Fprintln(out)
				printDiagnosis(out, o)
				fmt.Fprintf(out, "\n%s\n", WarnStyle.Render(
					"The account just captured may be mislabeled. Re-login so both halves agree, then `account add` again."))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fromSession, "from-session", "",
		"repair a tracked account's stored credential from its own session profile instead of the live login")
	return cmd
}

// captureCurrent reads the live credential + oauthAccount and stores them as an
// account (keyed by email). Shared by the CLI `add` and the UI.
// isolatedProfileDir reports a profile directory this process is pointed at that
// is NOT the default one, or "" when neither variable diverges.
//
// The two variables select INDEPENDENT surfaces, which is why both are checked
// rather than the first one set: CLAUDE_SECURESTORAGE_CONFIG_DIR selects where
// the credential lives, CLAUDE_CONFIG_DIR selects the identity and config
// profile. Either one pointing away from ~/.claude is enough to make `add`
// capture a mismatched pair — the credential of one profile filed under the
// identity of another — so a divergence in either is refused.
func isolatedProfileDir() string {
	home, herr := account.ClaudeHome()
	for _, k := range []string{"CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CONFIG_DIR"} {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			continue
		}
		// A home we cannot resolve means we cannot prove the profile is the
		// default one; refuse rather than capture on an assumption.
		if herr != nil || !sameDirPath(v, home) {
			return v
		}
	}
	return ""
}

// sameDirPath compares two directory paths, resolving symlinks where it can, so
// a profile that merely spells ~/.claude differently is not mistaken for one.
func sameDirPath(a, b string) bool {
	clean := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return clean(a) == clean(b)
}

func captureCurrent(st *account.Store) (account.Account, bool, error) {
	// `add` captures the LIVE, machine-wide login — ReadLive deliberately ignores
	// the profile env vars. So inside a session terminal (`clauderig account run`,
	// or any shell that exports these) the credential it reads is NOT the account
	// this terminal is running as, and capturing it would file the default
	// profile's credential under whatever `~/.claude.json` happens to name.
	// Refuse and point at the two paths that do what was actually meant.
	//
	// Claude Code resolves its credential store through CLAUDE_SECURESTORAGE_CONFIG_DIR
	// first, then CLAUDE_CONFIG_DIR (verified in the 2.1.227 bundle), so both count.
	// Credit: claude-swap #190/#205 found this failure mode first.
	if profile := isolatedProfileDir(); profile != "" {
		return account.Account{}, false, fmt.Errorf(
			"refusing to capture: this terminal is running an isolated profile (%s), but `add` "+
				"reads the machine-wide login — it would store the WRONG account's credential.\n"+
				"Fix: run `clauderig account add` from a normal terminal to capture the live login, "+
				"or `clauderig account add --from-session <id|email>` to repair a tracked account "+
				"from its own session profile",
			profile)
	}
	cred, err := account.ReadLive()
	if err != nil {
		return account.Account{}, false, err
	}
	oauth, _ := account.ReadOAuthAccount()
	return st.CaptureLive(cred, oauth)
}

func newAccountListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
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
			// JSON is the whole answer — accounts, health, active pointer — so it
			// short-circuits before the human rendering and its desync warning,
			// which belongs on stderr-shaped output, not in a parsed document.
			if asJSON {
				return printAccountsJSON(out, st)
			}
			all, err := st.List()
			if err != nil {
				return err
			}
			// An empty store is exactly when a desync is most likely to be missed,
			// so check BEFORE the early return: a machine that has never run
			// `account add` can still have a live login whose two halves disagree.
			desynced := !st.Diagnose().InSync
			warnDesync := func() {
				if desynced {
					fmt.Fprintf(out, "\n%s %s\n", ErrStyle.Render("✗"), WarnStyle.Render(
						"the live login is desynced — run `clauderig account doctor`"))
				}
			}
			if len(all) == 0 {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("no accounts yet — run `clauderig account add` while logged in"))
				warnDesync()
				return nil
			}
			active, _ := st.Active()
			fmt.Fprintln(out, HeaderStyle.Render("Claude Code accounts"))
			anyDead := false
			for _, a := range all {
				marker := "  "
				if a.ID == active {
					marker = OkStyle.Render("→ ")
				}
				// A stored credential can silently lose its tokens (an expired
				// login round-tripped over it); `switch` would refuse it, so
				// surface that here rather than at switch time.
				health := ""
				if !st.CredentialHealthy(a.ID) {
					anyDead = true
					health = "  " + ErrStyle.Render("✗ stored credential has no tokens")
				}
				if a.Disabled {
					health += "  " + DimStyle.Render("(disabled)")
				}
				fmt.Fprintf(out, "%s%s%s\n", marker, accountTitle(a), health)
			}
			if anyDead {
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"  repair: `clauderig account add --from-session <id>`, or log in live and `account add`"))
			}
			if active == "" {
				fmt.Fprintf(out, "\n%s\n", DimStyle.Render("(no account marked live — `account add` or `switch` sets it)"))
			}
			// Scope, stated where the accounts are listed: Claude Desktop is a
			// separate login with its own token store, and users reasonably assume
			// one "Claude account" covers both.
			fmt.Fprintf(out, "%s\n", DimStyle.Render("Claude Code logins only — Claude Desktop signs in separately and is unaffected"))
			// The arrow above marks clauderig's POINTER, not proof of what the
			// server sees. If the live credential disagrees with ~/.claude.json,
			// say so here — this listing is the screen most likely to be trusted.
			warnDesync()
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit accounts, health and the active pointer as JSON")
	return cmd
}

func newAccountRunCmd() *cobra.Command {
	var noShare bool
	cmd := &cobra.Command{
		Use:   "run [<id|email|alias>] [-- claude args...]",
		Short: "Launch Claude Code as an account in THIS terminal only",
		Long: "Session mode: runs `claude` against the account's own persistent\n" +
			"CLAUDE_CONFIG_DIR, so this terminal is that account while every other\n" +
			"terminal and the VS Code extension stay on your default. The profile\n" +
			"self-refreshes its own token in isolation and never touches your live\n" +
			"login. ~/.claude customizations are shared in by default (--no-share for a\n" +
			"bare profile). Args after `--` pass through to claude.\n\n" +
			"With no account named, uses the one mapped to this directory\n" +
			"(`clauderig account map`), inheriting the nearest mapped ancestor.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := account.DefaultStore()
			if err != nil {
				return err
			}
			var a account.Account
			if len(args) > 0 {
				a, err = st.Resolve(args[0])
				if err != nil {
					return err
				}
				args = args[1:]
			} else {
				// No account named: fall back to this directory's mapping. An
				// unmapped directory is an error rather than a silent launch of
				// the live login — `run` promises an isolated profile, and
				// quietly giving you the machine-wide one instead is exactly the
				// kind of surprise this command exists to avoid.
				cwd, cerr := os.Getwd()
				if cerr != nil {
					return cerr
				}
				mapped, ok := mappedAccount(st, cwd)
				if !ok {
					return errors.New(
						"no account named, and this directory is not mapped to one.\n" +
							"Name it (`clauderig account run <id|email|alias>`), or bind this directory " +
							"with `clauderig account map <id|email|alias>`")
				}
				a = mapped
				fmt.Fprintf(cmd.ErrOrStderr(), "%s %s\n",
					DimStyle.Render("mapped:"), DimStyle.Render(cwd))
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
			return runClaude(cmd, claudeBin, dir, args)
		},
	}
	cmd.Flags().BoolVar(&noShare, "no-share", false, "don't share ~/.claude customizations into the session (bare profile)")
	return cmd
}

func newAccountSwitchCmd() *cobra.Command {
	var dryRun, force, kill, asJSON bool
	cmd := &cobra.Command{
		Use:   "switch [<id|email>]",
		Short: "Change the machine-wide login (guarded against live sessions)",
		Long: "Global swap: overwrites the live credential the whole machine reads, so\n" +
			"every Claude Code instance follows. With no argument, rotates to the next\n" +
			"stored account, skipping any that are disabled (`account disable`); a\n" +
			"disabled account is still a valid target when named explicitly.\n\n" +
			"SCOPE: the Claude Code CLI login only. Claude Desktop authenticates\n" +
			"separately — its own token store and its own claude.ai session — so it\n" +
			"stays signed in as whatever account it was, and a switch neither moves\n" +
			"it nor logs it out. To change Desktop, sign in from Desktop itself.\n\n" +
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
			return runSwitch(cmd, args, dryRun, force, kill, asJSON)
		},
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what switch would do without changing anything")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "swap even while Claude Code is running (those sessions will need to re-login)")
	cmd.Flags().BoolVarP(&kill, "kill", "k", false, "terminate running Claude Code processes first, then swap")
	cmd.Flags().BoolVar(&asJSON, "json", false, "report the outcome as JSON (including refusals)")
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
		Use:     "remove <id|email>",
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
			// A directory mapping naming a removed account would do nothing at
			// exactly the moment it was expected to work, so it goes with it.
			pruneMappingsForAccount(a.ID)
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
		statuses, err := st.StoredStatuses()
		if err != nil {
			return err
		}
		var procs []account.Instance
		if home, herr := account.ClaudeHome(); herr == nil {
			procs = account.RunningInstances(home)
		}
		res, err := tea.NewProgram(tui.NewAccount(statuses, procs, note)).Run()
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
			a, updated, cerr := captureCurrent(st)
			if cerr != nil {
				note = ErrStyle.Render(cerr.Error())
				continue
			}
			if serr := st.SetActive(a.ID); serr != nil {
				note = ErrStyle.Render(serr.Error())
				continue
			}
			note = "added " + a.Email
			if updated {
				note = "updated " + a.Email
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
		case "repair":
			target, rerr := st.Resolve(final.Action.ID)
			if rerr != nil {
				note = ErrStyle.Render(rerr.Error())
				continue
			}
			if cerr := st.CaptureFromSession(target); cerr != nil {
				note = ErrStyle.Render(cerr.Error())
				continue
			}
			note = "recaptured " + accountTitle(target) + " from its session"
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
		case "toggle-disable":
			target, rerr := st.Resolve(final.Action.ID)
			if rerr != nil {
				note = ErrStyle.Render(rerr.Error())
				continue
			}
			if derr := st.SetDisabled(target.ID, !target.Disabled); derr != nil {
				note = ErrStyle.Render(derr.Error())
				continue
			}
			if target.Disabled {
				note = "enabled " + target.Email
			} else {
				note = "disabled " + target.Email + " — a bare switch will skip it"
			}
		case "alias":
			target, rerr := st.Resolve(final.Action.ID)
			if rerr != nil {
				note = ErrStyle.Render(rerr.Error())
				continue
			}
			alias, aerr := promptAlias(target)
			if aerr != nil {
				return aerr
			}
			switch {
			case alias == target.Alias:
				continue // unchanged (or backed out)
			case alias == "":
				if cerr := st.ClearAlias(target.ID); cerr != nil {
					note = ErrStyle.Render(cerr.Error())
					continue
				}
				note = "alias removed from " + target.Email
			default:
				if serr := st.SetAlias(target.ID, alias); serr != nil {
					note = ErrStyle.Render(serr.Error())
					continue
				}
				note = "alias " + alias + " → " + target.Email
			}
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
	)).WithKeyMap(huhEscKeyMap()).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	if err != nil || choice == "cancel" || choice == "" {
		return DimStyle.Render("switch cancelled")
	}
	killNote := ""
	if choice == "kill" {
		failed := account.KillInstances(blocked, 5*time.Second)
		if n := len(failed); n > 0 {
			killNote = fmt.Sprintf("killed %d/%d (%d wouldn't die, forced past), ", len(blocked)-n, len(blocked), n)
		} else {
			killNote = "killed live sessions, "
		}
	}
	if _, _, serr := doSwitch(st, target, true); serr != nil {
		return ErrStyle.Render(serr.Error())
	}
	if choice == "kill" {
		return killNote + "switched to " + accountTitle(target)
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
			"note: "+a.Email+" is your current live account — a separate session of it shares a"))
		fmt.Fprintln(cmd.ErrOrStderr(), WarnStyle.Render(
			"      rotating token and may ask you to log in. Prefer running a different account;"))
		fmt.Fprintln(cmd.ErrOrStderr(), DimStyle.Render(
			"      if it does prompt, re-run `account add` for it while it's your live login."))
	}
}

// runSwitch performs (or, with dryRun, previews) a guarded global swap.
func runSwitch(cmd *cobra.Command, args []string, dryRun, force, kill, asJSON bool) error {
	out := cmd.OutOrStdout()
	// With --json, stdout carries exactly one object and every human line moves
	// to stderr — otherwise a caller piping to jq has to strip prose first.
	report := func(switchJSON) {}
	if asJSON {
		out = cmd.ErrOrStderr()
		report = func(r switchJSON) { _ = emitJSON(cmd.OutOrStdout(), r) }
	}
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
		report(switchJSON{From: active, To: target.ID, ToEmail: target.Email, Reason: "already-live"})
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
		report(switchJSON{DryRun: true, From: active, To: target.ID, ToEmail: target.Email,
			Blocking: toInstancesJSON(live)})
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
			report(switchJSON{From: active, To: target.ID, ToEmail: target.Email,
				Reason: "live-sessions", Blocking: toInstancesJSON(live)})
			return errors.New("live Claude Code sessions detected")
		}
	}

	// doSwitch re-checks for live sessions and can refuse with `blocked` set and
	// no error — a Claude Code instance that started between the check above and
	// the swap. Discarding it reported "Switched to …" for a swap that never
	// happened.
	backup, blocked, err := doSwitch(st, target, force)
	if err != nil {
		// A refusal is the outcome a script most needs to see, so it is reported
		// as JSON as well as returned — the error still sets the exit code.
		report(switchJSON{From: active, To: target.ID, ToEmail: target.Email, Reason: err.Error()})
		return err
	}
	if len(blocked) > 0 {
		printSwitchRefused(out, blocked)
		return errors.New("live Claude Code sessions detected")
	}
	if backup != "" {
		fmt.Fprintf(out, "%s %s\n", DimStyle.Render("backed up live credential →"), backup)
	}
	fmt.Fprintf(out, "%s %s\n", OkStyle.Render("Switched to"), accountTitle(target))
	report(switchJSON{Switched: true, From: active, To: target.ID, ToEmail: target.Email, Backup: backup})
	return nil
}

// doSwitch performs the guarded global swap to target (no dry-run, no printing).
// It re-checks for live sessions and returns them as `blocked` (with no
// mutation) if any are running — so every caller, CLI or UI, is guarded. On
// success it round-trips the displaced account's current credential back into
// its store and returns the safety-backup path.
func doSwitch(st *account.Store, target account.Account, force bool) (backup string, blocked []account.Instance, err error) {
	home, err := account.ClaudeHome()
	if err != nil {
		return "", nil, err
	}
	if !force {
		live, scanErr := account.RunningInstancesScan(home)
		if len(live) > 0 {
			return "", live, nil
		}
		// "I could not look" is not "nothing is running". Proceeding here would
		// be the guard silently permitting the one swap it exists to refuse.
		if scanErr != nil {
			return "", nil, fmt.Errorf(
				"refusing to switch: %w, so whether Claude Code is running is unknown.\n"+
					"Close your Claude Code windows and retry, or `--force` to swap anyway "+
					"(any running session will need to log in again)", scanErr)
		}
	}
	// Hold Claude Code's OWN credential locks BEFORE reading any of the state
	// this swap depends on. Acquiring later would serialize the writes of two
	// concurrent switches while leaving both working from a snapshot taken
	// before the other ran: both read active=X, the first switches to Y, and the
	// second then saves Y's live credential back into X's store. Read-then-lock
	// is not the same as lock-then-read.
	//
	// The lock also closes the race with Claude Code itself. Its token refresh
	// reads the credential, does a network round trip, and saves — all under
	// these locks — so a swap landing inside that window is overwritten by the
	// refreshed OLD account's token, and the backup taken a moment earlier
	// preserves a refresh token that has already been rotated away. Under the
	// lock, Claude Code's own double-checked re-read sees the swapped
	// (non-expired) credential and abandons the refresh instead.
	//
	// This matters most in exactly the case the guard above permits: `--force`,
	// or a session the process scan could not see. It costs nothing when nothing
	// is running — an uncontended mkdir.
	release, lockCompromised, lerr := account.LockCredentials(home, 0)
	if lerr != nil {
		return "", nil, fmt.Errorf(
			"refusing to switch: %w.\n"+
				"A token refresh is in flight; swapping the credential underneath it would be "+
				"overwritten by the old account's refreshed token. Retry in a few seconds", lerr)
	}
	defer release()

	// Everything below is read under the lock.
	active, _ := st.Active()
	targetCred, err := st.Credential(target.ID)
	if err != nil {
		return "", nil, fmt.Errorf("read stored credential: %w", err)
	}
	// The stored credential must actually authenticate BEFORE it is written over
	// the live one. A blob whose tokens Claude Code has blanked — an expired
	// refresh token, or a logout — parses perfectly well and writes perfectly
	// well, and the result is a machine that is simply logged out. `list` and
	// `doctor` already flag this state ("credential ✗ no tokens"); switching into
	// it was still permitted, which is how a switch could log you out of an
	// account that was working a moment earlier.
	//
	// #197 added the mirror of this guard on the way IN (SaveCredential refuses to
	// store a token-less blob). This is the way OUT.
	if !account.HasTokens(targetCred) {
		return "", nil, fmt.Errorf(
			"refusing to switch: the stored credential for %s has no tokens, so switching "+
				"to it would log this machine out.\n"+
				"Fix: `clauderig account add --from-session %s` to repair it from that "+
				"account's own session, or log in as %s and run `clauderig account add`",
			target.Email, target.ID, target.Email)
	}
	// The profile block must move WITH the credential, so fetch it BEFORE any
	// mutation and refuse the switch if it's missing. Swapping the credential
	// alone leaves ~/.claude.json naming the previous account while every request
	// authenticates as the new one — a silent desync that is very hard to spot
	// later, because the UI, the plan display and the per-org caches all read the
	// block, not the token. Previously this was a best-effort write after the
	// credential had already moved, so a target with no stored block produced
	// exactly that state and still reported "Switched to …".
	tgtOAuth, err := st.OAuth(target.ID)
	if err != nil {
		return "", nil, fmt.Errorf("read stored account profile: %w", err)
	}
	if len(tgtOAuth) == 0 {
		return "", nil, fmt.Errorf(
			"%s has no stored account profile, so switching would desync this login: "+
				"requests would authenticate as %s while Claude Code kept displaying the current account.\n"+
				"Fix: log in as %s, then run `clauderig account add` to capture its profile, then switch",
			target.Email, target.Email, target.Email)
	}
	// Non-empty is not enough: the two stored halves must describe the SAME
	// account. `add` deliberately allows capturing a desynced live login (it warns
	// rather than refuses, so the capture-then-repair path stays open), which means
	// a mislabeled pair can reach the store. Switching one in would propagate a
	// known-bad identity machine-wide, so verify the pair here and refuse.
	tgtCredOrg, cerr := account.CredentialOrg(targetCred)
	if cerr != nil {
		return "", nil, fmt.Errorf("parse stored credential for %s: %w\n"+
			"Fix: `clauderig account add --from-session %s` (recapture from its session profile), "+
			"or log in as %s and run `clauderig account add`", target.Email, cerr, target.ID, target.Email)
	}
	if tgtBlockOrg := account.ProfileOrg(tgtOAuth); tgtCredOrg != "" && tgtBlockOrg != "" && tgtCredOrg != tgtBlockOrg {
		return "", nil, fmt.Errorf(
			"the stored copy of %s is itself desynced: its credential belongs to org %s but its "+
				"profile block says org %s. Switching would propagate that mismatch machine-wide.\n"+
				"Fix: log in as %s and run `clauderig account add` to recapture a consistent pair",
			target.Email, tgtCredOrg, tgtBlockOrg, target.Email)
	}
	// WriteOAuthAccount no-ops when ~/.claude.json is absent, so without this the
	// swap would move the credential and report success having written no profile
	// at all — the same desync, freshly manufactured.
	if !account.GlobalConfigExists() {
		return "", nil, errors.New(
			"~/.claude.json does not exist, so the account profile cannot be swapped alongside the credential.\n" +
				"Run `claude` once to create it, then switch")
	}
	// Held for the whole swap so the credential can be put back if the profile
	// write fails partway — a half-applied swap is the desync itself.
	var displacedCred []byte
	if cur, lerr := account.ReadLive(); lerr == nil {
		displacedCred = cur
		var berr error
		// The backup is the safety net before we overwrite the live credential —
		// abort the switch if we can't write it rather than mutate unprotected.
		if backup, berr = st.BackupLive(cur, time.Now().UTC().Format("20060102-150405.000000000")); berr != nil {
			return "", nil, fmt.Errorf("back up live credential before switch: %w", berr)
		}
		if active != "" && active != target.ID {
			_ = st.SaveCredential(active, cur) // best-effort: keep displaced snapshot fresh
		}
	} else if !errors.Is(lerr, account.ErrNoLive) {
		return "", nil, lerr
	}
	// Round-trip the displaced account's oauthAccount block (identity + plan) so
	// its stored copy stays current too.
	if active != "" && active != target.ID {
		if curOAuth, _ := account.ReadOAuthAccount(); len(curOAuth) > 0 {
			_ = st.SaveOAuth(active, curOAuth)
		}
	}
	// Last check before the one write that cannot be taken back. If our lock was
	// judged stale and taken over while we were preparing — a suspended laptop is
	// enough — then nothing we do from here is exclusive, and Claude Code may be
	// mid-refresh. Stop while the only casualty is a switch that did not happen.
	if lockCompromised() {
		return "", nil, errors.New(
			"refusing to switch: another process took over Claude Code's credential lock while " +
				"this swap was being prepared, so the write would not be exclusive.\n" +
				"Nothing was changed — retry in a few seconds")
	}
	if err := account.WriteLive(targetCred); err != nil {
		return "", nil, err
	}
	// Swap the plan/identity block too, so Claude Code shows the right plan
	// immediately instead of the previous account's tier until a login refresh.
	// Unconditional: tgtOAuth was verified non-empty before the credential moved.
	//
	// If this fails the credential has already moved, so put it back rather than
	// leaving the machine in the half-swapped state this whole change exists to
	// prevent. A failed rollback is reported alongside the original error — that
	// combination is the one case where the caller genuinely must intervene.
	if werr := account.WriteOAuthAccount(tgtOAuth); werr != nil {
		if displacedCred != nil {
			if rerr := account.WriteLive(displacedCred); rerr != nil {
				return "", nil, fmt.Errorf(
					"swap account profile: %w; AND the credential could not be rolled back: %v.\n"+
						"The login is now desynced — restore from %s and run `clauderig account doctor`",
					werr, rerr, backup)
			}
		}
		return "", nil, fmt.Errorf("swap account profile (credential rolled back, nothing changed): %w", werr)
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
// nextAccount picks the rotation target for a bare `switch`.
//
// Rotation walks the FULL list to find where we are, then takes the next
// ENABLED account: disabled accounts must not be landed on by accident, but the
// active account may itself be disabled (it was disabled while live, or switched
// to by name), and dropping it from the walk would restart rotation from the top
// instead of moving on from where you are.
func nextAccount(st *account.Store, active string) (account.Account, error) {
	all, err := st.List()
	if err != nil {
		return account.Account{}, err
	}
	if len(all) == 0 {
		return account.Account{}, errors.New("no accounts to rotate between")
	}
	start := 0
	for i, a := range all {
		if a.ID == active {
			start = i + 1
			break
		}
	}
	for off := 0; off < len(all); off++ {
		cand := all[(start+off)%len(all)]
		if !cand.Disabled && cand.ID != active {
			return cand, nil
		}
	}
	enabled, _ := st.Enabled()
	if len(enabled) == 0 {
		return account.Account{}, errors.New(
			"every account is disabled, so there is nothing to rotate to — " +
				"`clauderig account enable <id|email>`, or `switch <id|email>` by name")
	}
	return account.Account{}, errors.New("no other account to rotate to")
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
	)).WithKeyMap(huhEscKeyMap()).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return false, nil
	}
	return ok, err
}

// promptAlias asks for an account's short handle, pre-filled with the current
// one so the prompt doubles as "rename" and "clear" (submit it empty). Backing
// out returns the existing alias, which the caller reads as "no change".
func promptAlias(a account.Account) (string, error) {
	alias := a.Alias
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Alias for " + a.Email).
			Description("a short handle usable anywhere an id or email is (empty clears it)").
			Value(&alias),
	)).WithKeyMap(huhEscKeyMap()).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return a.Alias, nil
	}
	return strings.TrimSpace(alias), err
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
	name := a.Email
	if name == "" {
		name = a.ID
	}
	// The alias leads: it is the handle the user chose and the one they will
	// type again, and it is meaningless unless it is visible somewhere.
	if a.Alias != "" {
		name = a.Alias + DimStyle.Render(" · "+name)
	}
	if a.SubscriptionType != "" {
		name += DimStyle.Render(" · " + a.SubscriptionType)
	}
	return name
}
