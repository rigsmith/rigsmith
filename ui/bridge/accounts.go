package bridge

import (
	"context"
	"os"
	"path/filepath"

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
	for _, acc := range all {
		v.Accounts = append(v.Accounts, AccountRow{
			ID: acc.ID, Email: acc.Email,
			Subscription: acc.SubscriptionType, Org: acc.OrganizationUUID,
			Active: acc.ID == active,
		})
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
