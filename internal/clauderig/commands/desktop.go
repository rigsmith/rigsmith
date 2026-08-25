package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/dirmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/rigsmith/rigsmith/internal/clauderig/tui"
	"github.com/spf13/cobra"
)

// quitGrace is how long a Desktop instance gets to shut down cleanly before it
// is ended firmly. Electron flushes its own state on quit, so it is worth
// waiting for.
const quitGrace = 10 * time.Second

// NewDesktopCmd builds the `desktop` command group: several Claude Desktop
// accounts, side by side, each in its own permanent profile.
func NewDesktopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "desktop",
		Aliases: []string{"app"},
		Short:   "Run several Claude Desktop accounts side by side, each in its own profile",
		Long: "Claude Desktop holds one login at a time. This gives each account its own\n" +
			"permanent profile, so they all stay logged in and their windows can be open\n" +
			"together — you open the one you want.\n\n" +
			"SETTING UP, in order:\n\n" +
			"  1. `desktop add work`      creates a profile and opens a window\n" +
			"  2. log in as that account  in the window that just opened\n" +
			"  3. repeat 1–2 per account  `desktop add personal`, and so on\n" +
			"  4. close every window      `desktop quit <name>`, or just close them\n\n" +
			"You log in ONCE per profile. From then on `desktop open work` reopens it,\n" +
			"already signed in, and any number of windows can be open together.\n\n" +
			"`clauderig sync` backs every profile up — settings and chat history, never\n" +
			"the login. History stays separate per profile, which is the point: each one\n" +
			"is a distinct account, and Desktop keeps their histories apart.\n\n" +
			"Nothing is copied, swapped or decrypted: clauderig never reads Desktop's\n" +
			"login. Each profile is a directory Claude Desktop owns outright, and all\n" +
			"clauderig decides is which one to launch against. That is what makes this\n" +
			"safe where moving a session around was not.\n\n" +
			"  add    create a profile and open a window to log into\n" +
			"  open   open (or focus) a profile's window\n" +
			"  list   show saved profiles and which are open\n" +
			"  quit   close a profile's window\n" +
			"  map    bind a directory to a profile, for a bare `open` there\n" +
			"  rm     delete a profile (logs that account out of Desktop for good)\n\n" +
			"Separate from `clauderig account`, which switches the Claude Code CLI login.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if Interactive() {
				return runDesktopUI(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newDesktopAddCmd(), newDesktopOpenCmd(), newDesktopListCmd(),
		newDesktopQuitCmd(), newDesktopRemoveCmd(), newDesktopMapCmd(), newDesktopUnmapCmd())
	return cmd
}

// desktopStore roots the profiles beside the rest of clauderig's local state.
// Deliberately under ~/.clauderig and NOT under ~/.claude: these directories
// hold live logged-in sessions and must never reach the sync remote.
func desktopStore() (*desktop.Store, error) { return desktop.DefaultStore() }

func newDesktopAddCmd() *cobra.Command {
	var email string
	var noSeed bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a Desktop profile and open a window to log into",
		Long: "Creates an empty profile and opens a fresh Claude Desktop window bound to\n" +
			"it. Log into that window once; it stays logged in from then on, and no\n" +
			"other profile is touched.\n\n" +
			"The name is yours to choose (work, personal, client-x). If it matches a\n" +
			"stored `clauderig account`, the profile records the link — as a label only:\n" +
			"the CLI login and the Desktop login stay independent.\n\n" +
			"The profile is SEEDED from your existing Claude Desktop install so it is\n" +
			"usable immediately: MCP servers, theme and locale come across. Nothing that\n" +
			"carries the login does — the new profile still starts signed out, which is\n" +
			"the point. `--no-seed` starts from nothing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			app := desktop.New()
			if _, ok := app.Installed(); !ok {
				return desktopUnavailable()
			}
			st, err := desktopStore()
			if err != nil {
				return err
			}
			name := args[0]
			accountID, resolvedEmail := linkedAccount(name, email)
			p, err := st.Create(name, resolvedEmail, accountID)
			if errors.Is(err, desktop.ErrExists) {
				return fmt.Errorf("a Desktop profile named %q already exists — `clauderig desktop open %s` to use it", name, name)
			}
			if err != nil {
				return err
			}
			// Seed BEFORE launching: Desktop writes its own config.json on
			// first run, and seeding underneath a started app would race it.
			var seeded desktop.SeedResult
			if !noSeed {
				var serr error
				if seeded, serr = desktop.Seed(p, desktopInstallDir()); serr != nil {
					return serr
				}
			}
			if lerr := app.Launch(p.DataDir()); lerr != nil {
				return lerr
			}
			_ = st.Touch(p)
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ created"), p.Label())
			if !seeded.Empty() {
				what := "settings"
				if seeded.Config {
					what = "preferences"
				}
				if len(seeded.Files) > 0 {
					what += " and " + fmt.Sprintf("%d config file(s)", len(seeded.Files))
				}
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"  seeded "+what+" from your existing Claude Desktop install (no login copied)"))
			}
			if accountID != "" {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("  linked to stored account "+accountID+" (label only — the two logins stay separate)"))
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"  a fresh Claude Desktop window is opening — log into this account there.\n"+
					"  it stays logged in; `clauderig desktop open "+name+"` reopens it."))
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"  next: add any other accounts the same way. `clauderig sync` backs each\n"+
					"  profile's settings and chat history up — never its login."))
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email to show in the listing (also matches a stored account)")
	cmd.Flags().BoolVar(&noSeed, "no-seed", false,
		"start from an empty profile instead of copying settings from your existing Claude Desktop install")
	return cmd
}

// linkedAccount best-effort matches a profile name or email against the stored
// CLI accounts, purely so the listing can show the connection. A miss is not an
// error: Desktop profiles are allowed to exist for accounts the CLI never saw.
func linkedAccount(name, email string) (id, resolvedEmail string) {
	st, err := account.DefaultStore()
	if err != nil {
		return "", email
	}
	for _, ref := range []string{email, name} {
		if ref == "" {
			continue
		}
		if a, rerr := st.Resolve(ref); rerr == nil {
			if email == "" {
				email = a.Email
			}
			return a.ID, email
		}
	}
	return "", email
}

// errNoOpenProfiles means `quit` was asked to pick and every profile was
// determined to be closed — a statement of fact rather than a failure, so the
// caller reports it and exits 0. A profile whose state could not be established
// is NOT counted as closed, so this never stands in for "we could not look".
var errNoOpenProfiles = errors.New("no profile windows are open")

// resolveDesktopTarget picks the profile a command acts on, in the order a user
// would expect to be asked:
//
//  1. the profile they named;
//  2. the one bound to this directory (`desktop map`);
//  3. on a terminal, one they choose from a picker;
//  4. otherwise an error naming both ways to say which.
//
// Step 3 is why neither command guesses: with several profiles, silently
// picking one is the surprising outcome, and erroring at someone who is sitting
// right there is the unhelpful one.
func resolveDesktopTarget(st *desktop.Store, app desktop.App, args []string, onlyOpen bool) (desktop.Profile, error) {
	if len(args) > 0 && args[0] != "" {
		p, err := st.Resolve(args[0])
		if err != nil {
			return desktop.Profile{}, desktopNotFound(err, args[0])
		}
		return p, nil
	}
	// A directory binding is an explicit answer the user already gave, so a
	// binding that cannot be honoured is reported rather than stepped over.
	// Falling through would ask (or, off a terminal, claim the directory is
	// unmapped) while the user has already said which profile they mean — and a
	// picker would then happily act on a DIFFERENT one.
	if cwd, err := os.Getwd(); err == nil {
		if dm, derr := dirmapStore(); derr == nil {
			if entry, lerr := dm.Lookup(cwd); lerr == nil && entry.Desktop != "" {
				p, rerr := st.Resolve(entry.Desktop)
				if rerr != nil {
					return desktop.Profile{}, fmt.Errorf(
						"this directory is mapped to Desktop profile %q, which could not be resolved: %w\n"+
							"Rebind it with `clauderig desktop map <name>`, or drop the binding with `clauderig desktop unmap`",
						entry.Desktop, rerr)
				}
				return p, nil
			}
		}
	}
	if !Interactive() {
		return desktop.Profile{}, errors.New(
			"no profile named, and this directory is not mapped to one\n" +
				"Name it, or bind this directory with `clauderig desktop map <name>`")
	}
	return pickProfile(st, app, onlyOpen)
}

// profileOptions builds the choices a picker would show — split out from
// pickProfile so the decision can be tested without launching a form (a test
// that reaches the form blocks forever waiting for input it will never get).
func profileOptions(st *desktop.Store, app desktop.App, onlyOpen bool) ([]huh.Option[string], map[string]desktop.Profile, error) {
	all, err := st.List()
	if err != nil {
		return nil, nil, err
	}
	var opts []huh.Option[string]
	byName := map[string]desktop.Profile{}
	for _, p := range all {
		open, rerr := desktop.IsRunning(app, p.DataDir())
		// Hide only what is KNOWN to be closed. A failed scan is not proof of
		// anything — treating it as closed would let `quit` report "no profile
		// windows are open" and exit zero while the state was simply unknown,
		// which is the opposite of what IsRunning promises and of what the
		// named-profile path does.
		if onlyOpen && rerr == nil && !open {
			continue
		}
		label := p.Label()
		switch {
		case rerr != nil:
			label += "  (unknown)"
		case open:
			label += "  (open)"
		default:
			label += "  (closed)"
		}
		opts = append(opts, huh.NewOption(label, p.Name))
		byName[p.Name] = p
	}
	if len(opts) == 0 {
		if onlyOpen {
			return nil, nil, errNoOpenProfiles
		}
		return nil, nil, errors.New("no Desktop profiles yet — `clauderig desktop add <name>` creates one")
	}
	return opts, byName, nil
}

// pickProfile offers the saved profiles for selection, showing which are open so
// the choice is made on the same information the listing shows.
func pickProfile(st *desktop.Store, app desktop.App, onlyOpen bool) (desktop.Profile, error) {
	opts, byName, err := profileOptions(st, app, onlyOpen)
	if err != nil {
		return desktop.Profile{}, err
	}
	title := "Open which profile?"
	if onlyOpen {
		title = "Close which profile?"
	}
	var choice string
	ferr := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title(title).Options(opts...).Value(&choice),
	)).WithKeyMap(huhEscKeyMap()).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	if ferr != nil || choice == "" {
		return desktop.Profile{}, errCancelled
	}
	return byName[choice], nil
}

// errCancelled means the user backed out of the picker — not a failure.
var errCancelled = errors.New("cancelled")

func newDesktopOpenCmd() *cobra.Command {
	var sessionRef string
	var anyway bool
	cmd := &cobra.Command{
		Use:   "open [<name|email>]",
		Short: "Open (or focus) a profile's Claude Desktop window",
		Long: "Opens the profile's window, or focuses it if it is already open.\n\n" +
			"With no profile named: uses the one mapped to this directory\n" +
			"(`clauderig desktop map`, nearest mapped ancestor), and otherwise asks\n" +
			"which — a picker on a terminal, an error naming both ways to say so\n" +
			"off one. It never picks for you.\n\n" +
			"With --session, the window opens on that Claude Code session: pass its\n" +
			"id, or text to match its title or project. Desktop reads the transcript\n" +
			"from ~/.claude/projects, so a session that lives only in the synced repo\n" +
			"must be restored before it can be opened.\n\n" +
			"A deep link is routed by scheme, not to a particular window, so with a\n" +
			"second profile open the OS picks which one imports the session — that is\n" +
			"refused rather than risked, since it would cross an account boundary.\n" +
			"Quit the others, or pass --anyway when any window will do.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			app := desktop.New()
			if _, ok := app.Installed(); !ok {
				return desktopUnavailable()
			}
			st, err := desktopStore()
			if err != nil {
				return err
			}
			p, err := resolveDesktopTarget(st, app, args, false)
			if errors.Is(err, errCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			// Resolve the session BEFORE touching any window: a reference that
			// matches nothing should cost nothing, not leave an app open on the
			// wrong thing.
			var target sessionCandidate
			if sessionRef != "" {
				home, herr := account.ClaudeHome()
				if herr != nil {
					return herr
				}
				cands := findSessions(sessionRef, home, liveSessionIndex())
				target, err = pickSession(sessionRef, cands)
				if errors.Is(err, errCancelled) {
					return nil
				}
				if err != nil {
					return err
				}
				// Refuse before touching a window too: focusing one and then
				// declining to send reads as a half-done action. A listing or scan
				// that FAILS stops here as well — unknown state must not be read as
				// "nothing else is open".
				profiles, lerr := st.List()
				if lerr != nil {
					return fmt.Errorf("could not list Desktop profiles: %w\n"+
						"Sending now could import the session into the wrong account", lerr)
				}
				others, oerr := otherRunningProfiles(app, profiles, p)
				if oerr != nil {
					return fmt.Errorf("%w\nSending now could import the session into the wrong account", oerr)
				}
				if len(others) > 0 && !anyway {
					return ambiguousRoutingError(p, others)
				}
			}

			running, rerr := desktop.IsRunning(app, p.DataDir())
			if rerr != nil {
				return fmt.Errorf("could not tell whether %s is already open: %w\n"+
					"Launching now would risk a second window on the same profile", p.Name, rerr)
			}
			if running {
				if ferr := app.Focus(p.DataDir()); ferr != nil {
					return ferr
				}
				if sessionRef == "" {
					fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already open:"), p.Label())
					return nil
				}
				fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already open:"), p.Label())
			} else {
				if lerr := app.Launch(p.DataDir()); lerr != nil {
					return lerr
				}
				_ = st.Touch(p)
				if sessionRef == "" {
					fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ opened"), p.Label())
					return nil
				}
				// With no instance up, the OS would resolve claude:// by launching
				// the machine-wide install — the wrong profile entirely. Wait for
				// this one to exist first.
				ready, werr := desktop.WaitRunning(app, p.DataDir(), time.Now().Add(desktopReadyTimeout))
				if werr != nil {
					return fmt.Errorf("could not tell whether %s started: %w\n"+
						"Sending now could import the session into the wrong account", p.Name, werr)
				}
				if !ready {
					return fmt.Errorf("%s did not start within %s, so the session was not sent\n"+
						"Open it first (`clauderig desktop open %s`), then re-run with --session",
						p.Name, desktopReadyTimeout, p.Name)
				}
				// Running is not the same as ready to accept a deep link; Electron
				// registers the handler shortly after the process exists.
				time.Sleep(desktopSettle)
			}

			// Re-read: launching this profile above may itself have changed what
			// is running, and --anyway still needs to name what it is competing
			// with. Errors stop the send for the same reason as above.
			profiles, lerr := st.List()
			if lerr != nil {
				return fmt.Errorf("could not list Desktop profiles: %w\n"+
					"Sending now could import the session into the wrong account", lerr)
			}
			others, derr := otherRunningProfiles(app, profiles, p)
			if derr != nil {
				return fmt.Errorf("%w\nSending now could import the session into the wrong account", derr)
			}
			if len(others) > 0 && !anyway {
				return ambiguousRoutingError(p, others)
			}
			if oerr := app.OpenURL(resumeDeepLink(target.ID)); oerr != nil {
				return oerr
			}
			if len(others) > 0 {
				// --anyway: say where it went in terms of what is actually known,
				// which is not "the profile you named".
				fmt.Fprintf(out, "%s %s\n", WarnStyle.Render("⚠ sent, recipient chosen by the OS:"), target.label())
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"With "+strings.Join(others, ", ")+" also open it may have landed there instead."))
				return nil
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ opened in "+p.Name+":"), target.label())
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"If nothing appears, Desktop reports the reason in-app (sign-in, or a missing transcript)."))
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionRef, "session", "", "open this Claude Code session: its id, or text matching its title or project")
	cmd.Flags().BoolVar(&anyway, "anyway", false, "send the session even with other profiles open, when the OS picks which receives it")
	return cmd
}

// desktopReadyTimeout bounds the wait for a just-launched profile to exist, and
// desktopSettle gives Electron a moment past that to register its URL handler.
const (
	desktopReadyTimeout = 20 * time.Second
	desktopSettle       = 1500 * time.Millisecond
)

// liveSessionIndex reads this machine's Desktop sidecars for session titles.
// Titles are a nicety — a session with no sidecar still resolves by its first
// prompt — so an unreadable tree yields an empty index rather than an error.
func liveSessionIndex() session.Index {
	var roots []session.Root
	cfg, err := config.LoadOrDefault()
	if err == nil {
		me := config.Detect(machineName(cfg))
		if loc, _ := cfg.RootLocation("desktop", me); loc != "" {
			roots = append(roots, session.Root{Label: "desktop", Base: loc})
		}
	}
	// Every profile too: a session opened in one of them has its sidecar there
	// and nowhere else, so leaving them out would drop the titles for exactly
	// the sessions this command exists to reach.
	if st, serr := desktopStore(); serr == nil {
		if profiles, lerr := st.List(); lerr == nil {
			for _, pr := range profiles {
				roots = append(roots, session.Root{Label: pr.Name, Base: pr.DataDir()})
			}
		}
	}
	return session.Build(roots)
}

func newDesktopListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show saved Desktop profiles and which are open",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			st, err := desktopStore()
			if err != nil {
				return err
			}
			all, err := st.List()
			if err != nil {
				return err
			}
			if asJSON {
				return printDesktopJSON(out, st, all)
			}
			if len(all) == 0 {
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"no Desktop profiles yet — `clauderig desktop add <name>` creates one"))
				return nil
			}
			app := desktop.New()
			fmt.Fprintln(out, HeaderStyle.Render("Claude Desktop profiles"))
			for _, p := range all {
				marker, state := "  ", DimStyle.Render("closed")
				switch running, rerr := desktop.IsRunning(app, p.DataDir()); {
				case rerr != nil:
					marker, state = WarnStyle.Render("? "), WarnStyle.Render("unknown (process scan failed)")
				case running:
					marker, state = OkStyle.Render("● "), OkStyle.Render("open")
				}
				line := fmt.Sprintf("%s%s  %s", marker, p.Label(), state)
				if p.AccountID != "" {
					line += "  " + DimStyle.Render("↔ "+p.AccountID)
				}
				fmt.Fprintln(out, line)
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"each profile is its own login — opening one never signs another out"))
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"chat history is per profile — `clauderig sync` backs each one up separately"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the profiles and which are open as JSON")
	return cmd
}

func newDesktopQuitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "quit [<name|email>]",
		Short: "Close a profile's Claude Desktop window",
		Long: "Ends the Claude Desktop instance bound to that profile: politely first,\n" +
			"firmly after 10 seconds, and only once it has confirmed the processes are\n" +
			"gone. It touches nothing else — not another profile, not your plain\n" +
			"Claude.app — and logs nothing out.\n\n" +
			"With no profile named: uses the one mapped to this directory, and otherwise\n" +
			"asks which — a picker listing the OPEN windows on a terminal, an error off\n" +
			"one.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			st, err := desktopStore()
			if err != nil {
				return err
			}
			app := desktop.New()
			p, err := resolveDesktopTarget(st, app, args, true)
			switch {
			case errors.Is(err, errCancelled):
				return nil
			case errors.Is(err, errNoOpenProfiles):
				// Nothing to close is an answer, not a failure.
				fmt.Fprintf(out, "%s\n", DimStyle.Render("no profile windows are open"))
				return nil
			case err != nil:
				return err
			}
			running, rerr := desktop.IsRunning(app, p.DataDir())
			if rerr != nil {
				return fmt.Errorf("could not tell whether %s is open: %w", p.Name, rerr)
			}
			if !running {
				fmt.Fprintf(out, "%s %s\n", DimStyle.Render("not open:"), p.Label())
				return nil
			}
			if qerr := app.Quit(p.DataDir(), quitGrace); qerr != nil {
				return qerr
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ closed"), p.Label())
			return nil
		},
	}
}

func newDesktopRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "rm <name|email>",
		Aliases: []string{"remove"},
		Short:   "Delete a Desktop profile (logs that account out of Desktop for good)",
		Long: "Deletes the profile directory. The session lived only there, so this signs\n" +
			"that account out of Claude Desktop permanently — you would log in again to\n" +
			"get it back. The account's Claude Code login is untouched.\n\n" +
			"The profile's SYNCED copy is left alone, the same way deleting a project\n" +
			"locally does not delete it from the repo — another machine may still have\n" +
			"this profile, and its backup is not this machine's to discard. Consequence\n" +
			"worth knowing: a later `clauderig restore` here would bring the directory\n" +
			"back (signed out). `clauderig desktop rm` again after that is all it takes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			st, err := desktopStore()
			if err != nil {
				return err
			}
			p, err := st.Resolve(args[0])
			if err != nil {
				return desktopNotFound(err, args[0])
			}
			app := desktop.New()
			// Deleting a live Electron profile leaves the app writing into
			// unlinked files, so close it first rather than racing it. An
			// UNKNOWN state is treated as open: this deletes a logged-in
			// session, and guessing wrong here is unrecoverable.
			running, rerr := desktop.IsRunning(app, p.DataDir())
			if rerr != nil {
				return fmt.Errorf("could not tell whether %s is open: %w\n"+
					"Refusing to delete a profile that may still be running — close Claude Desktop and retry", p.Name, rerr)
			}
			if running {
				if !force {
					return fmt.Errorf("%s is open — close it first with `clauderig desktop quit %s`, or pass --force to close and delete",
						p.Name, p.Name)
				}
				if qerr := app.Quit(p.DataDir(), quitGrace); qerr != nil {
					return qerr
				}
			}
			if rerr := st.Remove(p.Name); rerr != nil {
				return rerr
			}
			// Same reasoning as `account remove`: a mapping to a profile that no
			// longer exists would fail at the moment it was relied on.
			if dm, derr := dirmapStore(); derr == nil {
				_ = dm.PruneDesktop(p.Name)
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ removed"), p.Label())
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"  that account is signed out of Desktop now; its Claude Code login is untouched"))
			// Say so only when there IS a synced copy: otherwise this is a note
			// about a file that does not exist, on the one command where the
			// user is already being told what they cannot undo.
			if staged := stagedProfilePath(p.Name); staged != "" {
				fmt.Fprintf(out, "%s\n", DimStyle.Render(
					"  its synced copy is kept ("+staged+") — `clauderig restore` here would recreate the directory"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "close the window first, then delete")
	return cmd
}

func desktopNotFound(err error, ref string) error {
	if errors.Is(err, desktop.ErrNotFound) {
		return fmt.Errorf("no Desktop profile %q — `clauderig desktop list` shows the saved ones, "+
			"`clauderig desktop add %s` creates it", ref, ref)
	}
	return err
}

// desktopUnavailable separates the two reasons this can't run, so the user is
// told which one applies instead of watching a launch fail.
func desktopUnavailable() error {
	if !desktop.Supported() {
		return fmt.Errorf("%w — Anthropic ships Claude Desktop for macOS and Windows.\n"+
			"`clauderig account` (the Claude Code CLI login) works everywhere", desktop.ErrUnsupported)
	}
	return fmt.Errorf("%w — install it from https://claude.ai/download", desktop.ErrNotInstalled)
}

// --- JSON + directory mapping ------------------------------------------------

type desktopProfileJSON struct {
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	AccountID   string `json:"accountId,omitempty"`
	Open        bool   `json:"open"`
	OpenUnknown bool   `json:"openUnknown,omitempty"`
	DataDir     string `json:"dataDir"`
	CreatedAt   string `json:"createdAt,omitempty"`
	LastOpened  string `json:"lastOpened,omitempty"`
}

type desktopListJSON struct {
	Supported bool                 `json:"supported"`
	Installed bool                 `json:"installed"`
	AppPath   string               `json:"appPath,omitempty"`
	Profiles  []desktopProfileJSON `json:"profiles"`
}

func printDesktopJSON(w interface{ Write([]byte) (int, error) }, _ *desktop.Store, all []desktop.Profile) error {
	app := desktop.New()
	path, installed := app.Installed()
	out := desktopListJSON{
		Supported: desktop.Supported(),
		Installed: installed,
		AppPath:   path,
		Profiles:  make([]desktopProfileJSON, 0, len(all)),
	}
	for _, p := range all {
		row := desktopProfileJSON{
			Name:       p.Name,
			Email:      p.Email,
			AccountID:  p.AccountID,
			DataDir:    p.DataDir(),
			CreatedAt:  p.CreatedAt,
			LastOpened: p.LastOpened,
		}
		// A failed scan is reported as such rather than as `open: false`, so a
		// script cannot mistake "could not look" for "not running".
		if open, rerr := desktop.IsRunning(app, p.DataDir()); rerr != nil {
			row.OpenUnknown = true
		} else {
			row.Open = open
		}
		out.Profiles = append(out.Profiles, row)
	}
	return emitJSON(w, out)
}

// desktopRefFor resolves which profile a command means: the one named, else the
// one bound to this directory.
func desktopRefFor(args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return args[0], nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dm, err := dirmapStore()
	if err != nil {
		return "", err
	}
	entry, lerr := dm.Lookup(cwd)
	if lerr != nil || entry.Desktop == "" {
		return "", errors.New(
			"no profile named, and this directory is not mapped to one.\n" +
				"Name it (`clauderig desktop open <name>`), or bind this directory with " +
				"`clauderig desktop map <name>`")
	}
	return entry.Desktop, nil
}

func newDesktopMapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "map [<name|email>] [dir]",
		Short: "Bind a directory to a Desktop profile, so a bare `desktop open` there uses it",
		Long: "Maps a directory (the working directory by default) to a Desktop profile,\n" +
			"so a bare `clauderig desktop open` inside it opens that window.\n\n" +
			"Subdirectories inherit the nearest mapped ancestor. With no arguments,\n" +
			"lists every mapping — the same table `clauderig account map` writes, so a\n" +
			"directory can name both the CLI account and the Desktop profile it belongs\n" +
			"to. Mappings are per-machine and never synced.",
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
			st, err := desktopStore()
			if err != nil {
				return err
			}
			p, err := st.Resolve(args[0])
			if err != nil {
				return desktopNotFound(err, args[0])
			}
			dir, err := resolveDirArg(args[1:])
			if err != nil {
				return err
			}
			entry, err := dm.Set(dir, func(e *dirmap.Entry) { e.Desktop = p.Name })
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s → %s\n", OkStyle.Render("✓ mapped"), entry.Dir, p.Label())
			fmt.Fprintf(out, "%s\n", DimStyle.Render("  `clauderig desktop open` there now opens this profile"))
			return nil
		},
	}
}

func newDesktopUnmapCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unmap [dir]",
		Short: "Remove a directory's Desktop binding (defaults to the working directory)",
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
			// Clear only the Desktop binding — the directory may also name a CLI
			// account, and `desktop unmap` has no business dropping that.
			entry, err := dm.Set(dir, func(e *dirmap.Entry) { e.Desktop = "" })
			if err != nil {
				return err
			}
			if entry.Account != "" {
				fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ Desktop binding removed from"), entry.Dir)
				fmt.Fprintf(out, "%s\n", DimStyle.Render("  its account binding ("+entry.Account+") is untouched"))
				return nil
			}
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ unmapped"), entry.Dir)
			return nil
		},
	}
}

// --- interactive screen ------------------------------------------------------

// runDesktopUI drives the Desktop profiles screen. Same shape as the accounts
// screen: the TUI records an intent, this loop performs it outside the event
// loop — process work and app launching must not run under bubbletea — and
// re-opens the screen with a note.
func runDesktopUI(cmd *cobra.Command) error {
	st, err := desktopStore()
	if err != nil {
		return err
	}
	app := desktop.New()
	note := ""
	for {
		all, lerr := st.List()
		if lerr != nil {
			return lerr
		}
		_, installed := app.Installed()
		rows := make([]tui.DesktopRow, 0, len(all))
		for _, p := range all {
			var open, unknown bool
			if installed {
				running, rerr := desktop.IsRunning(app, p.DataDir())
				open, unknown = running, rerr != nil
			}
			rows = append(rows, tui.DesktopRow{
				Name:      p.Name,
				Email:     p.Email,
				AccountID: p.AccountID,
				// Both fields come from ONE scan: on Windows each one starts
				// PowerShell and enumerates Win32_Process, so scanning twice per
				// row would run 2N subprocesses to open the screen — and could
				// derive "open" and "unknown" from different snapshots.
				Open:        open,
				OpenUnknown: unknown,
			})
		}
		res, rerr := tea.NewProgram(tui.NewDesktop(rows, installed, desktop.Supported(), note)).Run()
		if rerr != nil {
			return rerr
		}
		final, ok := res.(tui.DesktopModel)
		if !ok {
			return nil
		}
		note = ""
		switch final.Action.Kind {
		case "":
			return nil
		case "add":
			name, aerr := promptDesktopName()
			if aerr != nil {
				return aerr
			}
			if name == "" {
				continue // backed out
			}
			accountID, email := linkedAccount(name, "")
			p, cerr := st.Create(name, email, accountID)
			if cerr != nil {
				note = ErrStyle.Render(cerr.Error())
				continue
			}
			if lerr := app.Launch(p.DataDir()); lerr != nil {
				note = ErrStyle.Render(lerr.Error())
				continue
			}
			_ = st.Touch(p)
			note = "created " + p.Label() + " — log into the window that just opened"
		case "open":
			p, gerr := st.Get(final.Action.Name)
			if gerr != nil {
				note = ErrStyle.Render(gerr.Error())
				continue
			}
			open, rerr := desktop.IsRunning(app, p.DataDir())
			if rerr != nil {
				note = ErrStyle.Render("could not tell whether " + p.Name + " is open: " + rerr.Error())
				continue
			}
			if open {
				_ = app.Focus(p.DataDir())
				note = "already open: " + p.Label()
				continue
			}
			if lerr := app.Launch(p.DataDir()); lerr != nil {
				note = ErrStyle.Render(lerr.Error())
				continue
			}
			_ = st.Touch(p)
			note = "opened " + p.Label()
		case "quit":
			p, gerr := st.Get(final.Action.Name)
			if gerr != nil {
				note = ErrStyle.Render(gerr.Error())
				continue
			}
			if qerr := app.Quit(p.DataDir(), quitGrace); qerr != nil {
				note = ErrStyle.Render(qerr.Error())
				continue
			}
			note = "closed " + p.Label()
		case "remove":
			p, gerr := st.Get(final.Action.Name)
			if gerr != nil {
				note = ErrStyle.Render(gerr.Error())
				continue
			}
			ok, cerr := confirmDestructive(fmt.Sprintf(
				"Delete Desktop profile %s? This signs that account out of Desktop for good.", p.Label()))
			if cerr != nil {
				return cerr
			}
			if !ok {
				continue
			}
			// Deleting a live profile leaves Electron writing into unlinked
			// files, so close it first — the CLI refuses instead, but here the
			// confirmation already established intent. An unknown state is
			// treated as open, because deleting a live profile is unrecoverable.
			open, rerr := desktop.IsRunning(app, p.DataDir())
			if rerr != nil {
				note = ErrStyle.Render("refusing to delete " + p.Name + ": could not tell whether it is open (" + rerr.Error() + ")")
				continue
			}
			if open {
				if qerr := app.Quit(p.DataDir(), quitGrace); qerr != nil {
					note = ErrStyle.Render(qerr.Error())
					continue
				}
			}
			if rmErr := st.Remove(p.Name); rmErr != nil {
				note = ErrStyle.Render(rmErr.Error())
				continue
			}
			if dm, derr := dirmapStore(); derr == nil {
				_ = dm.PruneDesktop(p.Name)
			}
			note = "removed " + p.Label()
		}
	}
}

// promptDesktopName asks for a new profile's name. Empty (or backing out) means
// "never mind" — validation of the name itself belongs to the store.
func promptDesktopName() (string, error) {
	var name string
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Name for the new Desktop profile").
			Description("e.g. work, personal, client-x — a fresh window opens for you to log into").
			Value(&name),
	)).WithKeyMap(huhEscKeyMap()).WithTheme(brand.Theme(brand.AccentClaude)).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", nil
	}
	return strings.TrimSpace(name), err
}
