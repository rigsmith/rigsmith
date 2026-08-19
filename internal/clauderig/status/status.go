// Package status gathers a read-only snapshot of clauderig's sync state, shared
// by the `status` command and the `ui` dashboard. It does only local work (no
// network); remote reachability is left to the caller so the TUI never blocks.
package status

import (
	"context"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/allowlist"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/hooks"
)

// RootInfo is one root's local state.
type RootInfo struct {
	ID      string
	Files   int
	Present bool
}

// Info is the gathered snapshot.
type Info struct {
	Machine    config.Machine
	Remote     string
	HasStaging bool
	LastSync   string // "hash when — subject", or "" when never
	Dirty      bool
	Roots      []RootInfo
	Hooks      []string
	Devices    []devices.Device
	Account    AccountInfo
}

// AccountInfo is who the machine-wide Claude Code CLI is logged in as.
//
// Reported here because `status` is where people look to answer "what state is
// this machine in", and the answer is incomplete without the identity every
// `claude` invocation will authenticate with — especially on a machine that
// tracks several accounts, where the live login is a thing that CHANGES and is
// otherwise invisible until something fails.
type AccountInfo struct {
	// Email is what ~/.claude.json's oauthAccount block names — the account
	// Claude Code displays and, absent a desync, authenticates as.
	Email string
	// Subscription is the plan on the live credential ("max", "pro", …).
	Subscription string
	// Alias is the short handle the user gave that account, "" when none.
	Alias string
	// PointerEmail is the account clauderig's own active pointer names, set ONLY
	// when it disagrees with the live login. That disagreement makes the arrow
	// in `account list` a lie, which is worth saying where people look.
	PointerEmail string
	// Untracked reports a live login clauderig has never captured — `switch`
	// cannot return to it, so it is worth naming.
	Untracked bool
	// Desynced reports SPECIFICALLY that the two identity halves name different
	// organizations — the failure `account doctor` exists to catch, where
	// requests authenticate as one account while the UI shows another.
	//
	// Deliberately not Observation.InSync: that is the aggregate of every
	// diagnostic problem, including pointer drift and an unreadable half, so
	// reading it as "desynced" would report a desync that is not there.
	Desynced bool
	// LoggedOut reports that there is simply no credential — an ordinary state,
	// not a failure to read one.
	LoggedOut bool
	// Problem is set when the identity could not be read at all.
	Problem string
}

// Gather collects the local snapshot. settingsPath points at ~/.claude/settings.json.
func Gather(ctx context.Context, cfg *config.Config, me config.Machine, staging, settingsPath string) Info {
	info := Info{Machine: me, Remote: cfg.Remote}

	if _, err := os.Stat(filepath.Join(staging, ".git")); err == nil {
		info.HasStaging = true
		if repo, err := gitrepo.Open(ctx, staging); err == nil {
			if h, subj, when, err := repo.LastCommit(ctx); err == nil {
				info.LastSync = h + " " + when + " — " + subj
			}
			info.Dirty, _ = repo.Dirty(ctx)
		}
	}

	for _, r := range cfg.Roots {
		if !r.Enabled {
			continue
		}
		ri := RootInfo{ID: r.ID}
		loc, st := cfg.RootLocation(r.ID, me)
		if st == pathmap.StatusResolved && dirExists(loc) {
			ri.Present = true
			files, _, _ := allowlist.Walk(loc, allowlist.For(r.ID))
			ri.Files = len(files)
		}
		info.Roots = append(info.Roots, ri)
	}

	info.Hooks, _ = hooks.Status(settingsPath)

	info.Account = gatherAccount()

	if reg, err := devices.Load(staging); err == nil {
		info.Devices = reg.List()
	}
	return info
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// gatherAccount reads the live login. Best-effort by design: status is a report,
// so an unreadable Keychain becomes a line that says so rather than an error
// that costs the reader every other line.
func gatherAccount() AccountInfo {
	st, err := account.DefaultStore()
	if err != nil {
		return AccountInfo{Problem: "could not open the account store"}
	}
	o := st.Diagnose()
	ai := AccountInfo{
		Email:        o.BlockEmail,
		Subscription: o.CredSubscription,
		// The desync proper: two identity halves naming different orgs.
		Desynced: o.CredOrg != "" && o.BlockOrg != "" && o.CredOrg != o.BlockOrg,
	}

	// Pointer drift is measured against the CREDENTIAL, not the profile block —
	// the credential is what the server authenticates, and during a desync the
	// block is precisely the half that cannot be trusted. Same comparison
	// Observation.Problems makes.
	if o.ActiveOrg != "" && o.CredOrg != "" && o.ActiveOrg != o.CredOrg {
		ai.PointerEmail = o.ActiveEmail
	}

	switch {
	case o.CredErr == account.ErrNoLive.Error():
		// An ordinary logout, not a failure. Compared as a string because that
		// is what the observation records; the sentinel is the source of truth
		// for the text.
		ai.LoggedOut = true
	case o.CredErr != "":
		ai.Problem = "the live credential could not be read"
	case o.BlockErr != "":
		ai.Problem = "logged in, but ~/.claude.json could not be read"
	case o.BlockEmail == "":
		ai.Problem = "logged in, but ~/.claude.json names no account"
	}
	if ai.LoggedOut {
		return AccountInfo{LoggedOut: true}
	}
	return ai
}
