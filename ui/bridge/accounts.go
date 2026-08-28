package bridge

import (
	"context"
	"errors"
	"fmt"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// AccountRow is one stored login as the accounts screen lists it.
type AccountRow struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Subscription string `json:"subscription,omitempty"`
	Org          string `json:"org,omitempty"`
	Active       bool   `json:"active"`
	// Profile is the Claude Desktop profile bound to this account, when one
	// exists. The two logins are independent — clauderig never reads Desktop's
	// credential — so this is a binding recorded when the profile was made, not
	// a fact verified against Desktop.
	Profile string `json:"profile,omitempty"`
	// ProfileOpen is whether that profile has a window up. Unknown counts as
	// closed here: the button says "open", and opening one that is already open
	// focuses it rather than launching a second.
	ProfileOpen bool `json:"profileOpen,omitempty"`
}

// LiveSession is one running Claude Code process. `account switch` refuses
// while any exist, so the screen shows what is holding it up rather than
// leaving the button mysteriously disabled.
type LiveSession struct {
	PID int    `json:"pid"`
	Cwd string `json:"cwd,omitempty"`
}

// AccountsView is the whole accounts screen.
type AccountsView struct {
	Accounts []AccountRow  `json:"accounts"`
	Live     []LiveSession `json:"live"`
	// Desynced means the live credential and ~/.claude.json disagree about who
	// is logged in — clauderig's arrow says one thing and the server sees
	// another. It is the failure this screen exists to catch, so it is a field
	// rather than a note in prose.
	Desynced bool `json:"desynced"`
	// SwitchBlocked is true when a switch would be refused right now. The UI
	// must surface the guard, never route around it.
	SwitchBlocked bool   `json:"switchBlocked"`
	Error         string `json:"error,omitempty"`
}

// Accounts is the read side of the account screen. Every write — switch, add,
// remove — goes through the CLI, because the both-halves credential swap and
// its live-session guard live there and must not be reimplemented here.
type Accounts struct{}

// NewAccounts builds the accounts service.
func NewAccounts() *Accounts { return &Accounts{} }

// Get reads the current account picture.
func (a *Accounts) Get(ctx context.Context) (AccountsView, error) {
	var v AccountsView

	st, err := account.DefaultStore()
	if err != nil {
		v.Error = err.Error()
		return v, nil // a missing store is a state to render, not a failure
	}

	all, err := st.List()
	if err != nil {
		v.Error = err.Error()
		return v, nil
	}
	active, _ := st.Active()
	profiles, openProfiles := desktopProfiles()
	for _, acc := range all {
		row := AccountRow{
			ID: acc.ID, Email: acc.Email,
			Subscription: acc.SubscriptionType, Org: acc.OrganizationUUID,
			Active: acc.ID == active,
		}
		row.Profile = profiles[acc.ID]
		row.ProfileOpen = row.Profile != "" && openProfiles[row.Profile]
		v.Accounts = append(v.Accounts, row)
	}

	// Checked even with no stored accounts: a machine that has never run
	// `account add` can still have a live login whose two halves disagree.
	v.Desynced = !st.Diagnose().InSync

	for _, inst := range account.RunningInstances(claudeHome()) {
		v.Live = append(v.Live, LiveSession{PID: inst.PID, Cwd: inst.Cwd})
	}
	v.SwitchBlocked = len(v.Live) > 0
	return v, nil
}

// claudeHome resolves ~/.claude for the live-session scan.
func claudeHome() string {
	cfg, err := config.LoadOrDefault()
	if err == nil {
		me := config.DetectFor(cfg)
		if loc, st := cfg.RootLocation("cli", me); st == pathmap.StatusResolved && loc != "" {
			return loc
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// desktopProfiles maps an account id to the Desktop profile bound to it, and
// reports which of those profiles have a window up.
//
// Best-effort throughout: Desktop is a separate application with its own login,
// and a machine with no profiles at all is the ordinary case. A failure here
// costs the launch buttons, never the accounts list they sit beside.
func desktopProfiles() (byAccount map[string]string, open map[string]bool) {
	byAccount, open = map[string]string{}, map[string]bool{}
	st, err := desktop.DefaultStore()
	if err != nil {
		return byAccount, open
	}
	profiles, err := st.List()
	if err != nil {
		return byAccount, open
	}
	app := desktop.New()
	for _, p := range profiles {
		if p.AccountID != "" {
			byAccount[p.AccountID] = p.Name
		}
		// An unreadable process scan is not proof of anything, and treating it
		// as open would disable the button that would have worked. `desktop
		// open` focuses an already-open window anyway, so guessing closed is
		// the harmless direction.
		if running, rerr := desktop.IsRunning(app, p.DataDir()); rerr == nil && running {
			open[p.Name] = true
		}
	}
	return byAccount, open
}

// OpenDesktop launches (or focuses) the Claude Desktop profile bound to an
// account. The write stays in the CLI: `desktop open` owns the profile
// directory, the Electron flags and the already-running case, and none of that
// should exist twice.
func (a *Accounts) OpenDesktop(ctx context.Context, profile string) error {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return errors.New("no Desktop profile is bound to that account")
	}
	// Validated by membership, not by pattern: the set is known, so anything
	// outside it is refused without guessing what a legal name looks like.
	known, err := managedProfiles()
	if err != nil {
		return err
	}
	if !slices.Contains(known, profile) {
		return fmt.Errorf("%q is not a clauderig-managed Desktop profile", profile)
	}
	bin, err := resolveCLI()
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, bin, "desktop", "open", profile).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// RunCLI opens a terminal running Claude Code as one account.
//
// `account run`, not `account switch`: run scopes the credential to that one
// terminal, so starting a second account does not log the first out and the
// live-session guard never comes into it. Switching is the machine-wide change
// and stays where it is, behind its guard.
func (a *Accounts) RunCLI(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("name an account")
	}
	st, err := account.DefaultStore()
	if err != nil {
		return err
	}
	all, err := st.List()
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(all, func(acc account.Account) bool { return acc.ID == id }) {
		return fmt.Errorf("no stored account %q", id)
	}
	bin, err := resolveCLI()
	if err != nil {
		return err
	}
	// A script rather than an argument: it survives quoting, and the terminal is
	// left sitting there when claude exits instead of vanishing with whatever it
	// last printed.
	return runInTerminal(ctx, "account-"+id, "", []string{bin, "account", "run", id})
}
