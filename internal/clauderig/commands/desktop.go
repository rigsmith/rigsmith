package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
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
			"Nothing is copied, swapped or decrypted: clauderig never reads Desktop's\n" +
			"login. Each profile is a directory Claude Desktop owns outright, and all\n" +
			"clauderig decides is which one to launch against. That is what makes this\n" +
			"safe where moving a session around was not.\n\n" +
			"  add    create a profile and open a window to log into\n" +
			"  open   open (or focus) a profile's window\n" +
			"  list   show saved profiles and which are open\n" +
			"  quit   close a profile's window\n" +
			"  rm     delete a profile (logs that account out of Desktop for good)\n\n" +
			"Separate from `clauderig account`, which switches the Claude Code CLI login.",
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newDesktopAddCmd(), newDesktopOpenCmd(), newDesktopListCmd(),
		newDesktopQuitCmd(), newDesktopRemoveCmd())
	return cmd
}

// desktopStore roots the profiles beside the rest of clauderig's local state.
// Deliberately under ~/.clauderig and NOT under ~/.claude: these directories
// hold live logged-in sessions and must never reach the sync remote.
func desktopStore() (*desktop.Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return desktop.NewStore(filepath.Join(home, ".clauderig", "desktop")), nil
}

func newDesktopAddCmd() *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Create a Desktop profile and open a window to log into",
		Long: "Creates an empty profile and opens a fresh Claude Desktop window bound to\n" +
			"it. Log into that window once; it stays logged in from then on, and no\n" +
			"other profile is touched.\n\n" +
			"The name is yours to choose (work, personal, client-x). If it matches a\n" +
			"stored `clauderig account`, the profile records the link — as a label only:\n" +
			"the CLI login and the Desktop login stay independent.",
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
			if lerr := app.Launch(p.DataDir()); lerr != nil {
				return lerr
			}
			_ = st.Touch(p)
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ created"), p.Label())
			if accountID != "" {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("  linked to stored account "+accountID+" (label only — the two logins stay separate)"))
			}
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"  a fresh Claude Desktop window is opening — log into this account there.\n"+
					"  it stays logged in; `clauderig desktop open "+name+"` reopens it."))
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email to show in the listing (also matches a stored account)")
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

func newDesktopOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <name|email>",
		Short: "Open (or focus) a profile's Claude Desktop window",
		Args:  cobra.ExactArgs(1),
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
			p, err := st.Resolve(args[0])
			if err != nil {
				return desktopNotFound(err, args[0])
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
				fmt.Fprintf(out, "%s %s\n", DimStyle.Render("already open:"), p.Label())
				return nil
			}
			if lerr := app.Launch(p.DataDir()); lerr != nil {
				return lerr
			}
			_ = st.Touch(p)
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ opened"), p.Label())
			return nil
		},
	}
}

func newDesktopListCmd() *cobra.Command {
	return &cobra.Command{
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
			return nil
		},
	}
}

func newDesktopQuitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "quit <name|email>",
		Short: "Close a profile's Claude Desktop window",
		Args:  cobra.ExactArgs(1),
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
			"get it back. The account's Claude Code login is untouched.",
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
			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("✓ removed"), p.Label())
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"  that account is signed out of Desktop now; its Claude Code login is untouched"))
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
