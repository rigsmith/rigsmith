package account

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrNothingToRepair means both halves already name the same account.
var ErrNothingToRepair = errors.New("credential and profile block already agree")

// ErrNoStoredMatch means no stored account matches the live credential, so the
// truthful profile block isn't available locally to write back.
var ErrNoStoredMatch = errors.New("no stored account matches the live credential")

// Identity desync detection and an append-only observation journal.
//
// Claude Code's signed-in identity has TWO halves, written independently by
// different code paths:
//
//   - the CREDENTIAL — macOS Keychain "Claude Code-credentials", else
//     ~/.claude/.credentials.json. This is the bearer token, so the SERVER
//     attributes every authenticated request to this account. Ground truth.
//   - the PROFILE BLOCK — ~/.claude.json "oauthAccount" (email, org, plan).
//     Purely local belief. Claude Code's UI reads it, and its per-org caches
//     (clientDataCacheSlots) are keyed from it.
//
// Nothing enforces that the two agree. When they drift apart, every human-visible
// surface reports the profile block while every request goes to the credential's
// account — so published artifacts, usage and rate limits land on an account the
// UI never names. Observed live on 2026-08-06: the Keychain held one org for two
// weeks while the block named another, and artifacts published to the account the
// UI insisted was not signed in.
//
// The invariant that actually holds for real Claude Code state is
// credential.organizationUuid == oauthAccount.organizationUuid. (The account
// package's own test fixtures do not model this — they generate the two orgs from
// unrelated strings — which is why nothing here caught the drift.)
//
// Diagnose reports the current state; Record appends it to the journal only when
// the identity-bearing fields CHANGE, so the journal is a compact event log of
// every flip, together with the Claude Code processes that were running at the
// moment it happened.

// journalName is the append-only identity observation log under the store root.
const journalName = "account-journal.jsonl"

func (s *Store) journalPath() string { return filepath.Join(s.Root, journalName) }

// Observation is one snapshot of the machine's Claude Code identity.
type Observation struct {
	At string `json:"at"` // RFC3339Nano, UTC

	// Credential half (server truth).
	CredOrg          string `json:"credOrg,omitempty"`
	CredSubscription string `json:"credSubscription,omitempty"`
	CredErr          string `json:"credErr,omitempty"`

	// Profile-block half (~/.claude.json oauthAccount — local belief).
	BlockEmail            string `json:"blockEmail,omitempty"`
	BlockOrg              string `json:"blockOrg,omitempty"`
	BlockAccount          string `json:"blockAccount,omitempty"`
	BlockProfileFetchedAt int64  `json:"blockProfileFetchedAt,omitempty"`
	BlockErr              string `json:"blockErr,omitempty"`

	// ~/.claude.json file identity — which write produced this state.
	ConfigModified string `json:"claudeJsonModified,omitempty"`
	ConfigSize     int64  `json:"claudeJsonSize,omitempty"`

	// clauderig's own pointer.
	ActiveID    string `json:"activeId,omitempty"`
	ActiveEmail string `json:"activeEmail,omitempty"`
	ActiveOrg   string `json:"activeOrg,omitempty"`

	InSync bool `json:"inSync"`

	// Forensics: what changed since the previous journal entry, and what was
	// running when it changed. A flip whose Live list names one process is a
	// strong pointer at the writer.
	PreviousAt string     `json:"previousAt,omitempty"`
	Changed    []string   `json:"changed,omitempty"`
	Live       []Instance `json:"live,omitempty"`
}

// Diagnose snapshots both halves of the identity plus clauderig's pointer. It
// never fails: unreadable halves are recorded as errors on the observation, since
// "the Keychain refused" is itself a finding worth journaling.
func (s *Store) Diagnose() Observation {
	o := Observation{At: time.Now().UTC().Format(time.RFC3339Nano)}

	if cred, err := ReadLive(); err != nil {
		o.CredErr = err.Error()
	} else if sub, org, merr := metaFromBlob(cred); merr != nil {
		o.CredErr = merr.Error()
	} else {
		o.CredSubscription, o.CredOrg = sub, org
	}

	if raw, err := ReadOAuthAccount(); err != nil {
		o.BlockErr = err.Error()
	} else if len(raw) > 0 {
		m := parseOAuthMeta(raw)
		o.BlockEmail = m.EmailAddress
		o.BlockOrg = m.OrganizationUUID
		o.BlockAccount = m.AccountUUID
		o.BlockProfileFetchedAt = m.ProfileFetchedAt
	}

	if p, err := globalConfigPath(); err == nil {
		if fi, serr := os.Stat(p); serr == nil {
			o.ConfigModified = fi.ModTime().UTC().Format(time.RFC3339Nano)
			o.ConfigSize = fi.Size()
		}
	}

	o.ActiveID, _ = s.Active()
	if o.ActiveID != "" {
		if a, ok := s.read(o.ActiveID); ok {
			o.ActiveEmail, o.ActiveOrg = a.Email, a.OrganizationUUID
		}
	}

	if home, err := ClaudeHome(); err == nil {
		o.Live = RunningInstances(home)
	}

	o.InSync = len(o.Problems()) == 0
	return o
}

// Problems lists the identity inconsistencies in human-readable form, most
// serious first. Empty means the halves agree (or one is simply unknown — an
// absent value is not evidence of a conflict).
func (o Observation) Problems() []string {
	var p []string
	if o.CredOrg != "" && o.BlockOrg != "" && o.CredOrg != o.BlockOrg {
		p = append(p, fmt.Sprintf(
			"DESYNC: the live credential belongs to org %s, but ~/.claude.json says %s (org %s).\n"+
				"        Requests authenticate as the credential; everything displayed names the block.\n"+
				"        Artifacts, usage and limits are landing on the credential's account.",
			shortUUID(o.CredOrg), o.BlockEmail, shortUUID(o.BlockOrg)))
	}
	// A credential with no profile organization is its own anomaly, not a clean
	// bill of health: Claude Code has nothing to display you as, and `add` cannot
	// key an account without it. Reporting "both halves agree" here would be the
	// same false reassurance this command exists to remove.
	if o.CredOrg != "" && o.BlockOrg == "" {
		p = append(p, fmt.Sprintf(
			"the credential names org %s, but ~/.claude.json has no oauthAccount organization.\n"+
				"        Claude Code has no identity to display, and `account add` cannot key an account from it.",
			shortUUID(o.CredOrg)))
	}
	if o.ActiveOrg != "" && o.CredOrg != "" && o.ActiveOrg != o.CredOrg {
		p = append(p, fmt.Sprintf(
			"clauderig's active account (%s, org %s) is not the live credential (org %s)",
			o.ActiveEmail, shortUUID(o.ActiveOrg), shortUUID(o.CredOrg)))
	}
	if o.CredErr != "" {
		p = append(p, "credential unreadable: "+o.CredErr)
	}
	if o.BlockErr != "" {
		p = append(p, "profile block unreadable: "+o.BlockErr)
	}
	return p
}

// fingerprint is the identity-bearing subset of an observation. Deliberately
// EXCLUDES the file mtime/size and the live-process list: those churn constantly,
// and including them would append a journal line on every poll instead of only on
// a real identity change.
func (o Observation) fingerprint() string {
	return strings.Join([]string{
		o.CredOrg, o.CredErr,
		o.BlockEmail, o.BlockOrg, o.BlockAccount, o.BlockErr,
		o.ActiveID,
	}, "|")
}

// Record appends o to the journal, but only when its identity fingerprint differs
// from the last entry (or the journal is empty). Returns whether it wrote.
// Fills in Changed/PreviousAt so each line explains itself.
//
// The read/compare/append is a transaction guarded by a lock file, because
// running `doctor` while `watch` is polling (or two watchers at once) is an
// ordinary thing to do: unguarded, both processes read the same previous entry
// and both append the same transition, duplicating events and cross-linking
// PreviousAt to the wrong line — corrupting exactly the evidence this journal
// exists to preserve.
func (s *Store) Record(o Observation) (bool, error) {
	unlock, err := s.lockJournal()
	if err != nil {
		return false, err
	}
	defer unlock()

	prev, had, err := s.LastObservation()
	if err != nil {
		return false, err
	}
	if had {
		if prev.fingerprint() == o.fingerprint() {
			return false, nil
		}
		o.PreviousAt = prev.At
		o.Changed = changedFields(prev, o)
	}
	line, err := json.Marshal(o)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(s.journalPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

// journalLockStale is how long a lock file may sit before another process treats
// it as abandoned. A crashed writer must not wedge the journal forever, and the
// guarded section is a few file operations, so anything older than this is dead.
const journalLockStale = 30 * time.Second

// lockJournal takes an advisory cross-process lock via exclusive file creation —
// portable to every OS Go builds for, unlike flock. Returns a release function
// that is always safe to call.
func (s *Store) lockJournal() (func(), error) {
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return nil, err
	}
	lock := s.journalPath() + ".lock"
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("lock journal: %w", err)
		}
		if fi, serr := os.Stat(lock); serr == nil && time.Since(fi.ModTime()) > journalLockStale {
			_ = os.Remove(lock) // abandoned by a dead writer; reclaim and retry
			continue
		}
		if time.Now().After(deadline) {
			// Never block a diagnostic forever on a lock. Proceeding unguarded
			// risks a duplicate line; refusing risks losing the observation
			// entirely, which is worse for a forensic log.
			return func() {}, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func changedFields(prev, cur Observation) []string {
	var out []string
	add := func(name, a, b string) {
		if a != b {
			out = append(out, fmt.Sprintf("%s: %s → %s", name, orNone(a), orNone(b)))
		}
	}
	add("credOrg", prev.CredOrg, cur.CredOrg)
	add("blockEmail", prev.BlockEmail, cur.BlockEmail)
	add("blockOrg", prev.BlockOrg, cur.BlockOrg)
	add("blockAccount", prev.BlockAccount, cur.BlockAccount)
	add("activeId", prev.ActiveID, cur.ActiveID)
	add("credErr", prev.CredErr, cur.CredErr)
	add("blockErr", prev.BlockErr, cur.BlockErr)
	return out
}

// Journal returns the recorded observations, oldest first. limit <= 0 means all.
// Unparseable lines are skipped rather than failing the read — a corrupt line
// must not cost you the rest of the history.
func (s *Store) Journal(limit int) ([]Observation, error) {
	f, err := os.Open(s.journalPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var all []Observation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var o Observation
		if json.Unmarshal([]byte(line), &o) == nil {
			all = append(all, o)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}

// LastObservation returns the newest journal entry.
func (s *Store) LastObservation() (Observation, bool, error) {
	all, err := s.Journal(0)
	if err != nil || len(all) == 0 {
		return Observation{}, false, err
	}
	return all[len(all)-1], true, nil
}

// RepairProfileBlock rewrites ~/.claude.json's oauthAccount from the stored
// profile of whichever account matches the LIVE CREDENTIAL's org, and points
// active.json at it.
//
// The direction is deliberate and is the only safe one. The credential is what
// the server authenticates you as, so it is the truth; the block is local belief.
// Repairing means making the belief tell the truth — which never touches the
// credential, so no running Claude Code session is logged out. (Mutating the
// credential instead would log every live session out, which is why `switch` is
// guarded and why --fix must never do it.)
//
// This makes the display honest; it does not choose an account for you. To
// actually change which account is live, use `switch`.
func (s *Store) RepairProfileBlock() (Account, error) {
	o := s.Diagnose()
	if o.CredErr != "" {
		return Account{}, fmt.Errorf("read live credential: %s", o.CredErr)
	}
	if o.CredOrg == "" {
		return Account{}, errors.New(
			"the live credential carries no organizationUuid, so there is nothing to match a stored account against")
	}
	if o.InSync {
		return Account{}, ErrNothingToRepair
	}

	all, err := s.List()
	if err != nil {
		return Account{}, err
	}
	match := matchByOrg(all, o.CredOrg)
	if match == nil {
		return Account{}, fmt.Errorf(
			"%w: the credential belongs to org %s. Log in as that account and run "+
				"`clauderig account add` to capture its profile, then retry",
			ErrNoStoredMatch, o.CredOrg)
	}

	raw, err := s.OAuth(match.ID)
	if err != nil {
		return Account{}, fmt.Errorf("read stored profile for %s: %w", match.Email, err)
	}
	if len(raw) == 0 {
		return Account{}, fmt.Errorf(
			"%s matches the live credential but has no stored profile block to restore — "+
				"run `clauderig account add` while logged in as it", match.Email)
	}
	if err := WriteOAuthAccount(raw); err != nil {
		return Account{}, fmt.Errorf("write profile block: %w", err)
	}
	if err := s.SetActive(match.ID); err != nil {
		return Account{}, fmt.Errorf("update active pointer: %w", err)
	}
	return *match, nil
}

// CredentialOrg reports the organization a credential blob belongs to ("" when
// the blob records none).
func CredentialOrg(raw []byte) (string, error) {
	_, org, err := metaFromBlob(raw)
	return org, err
}

// ProfileOrg reports the organization an oauthAccount block names.
func ProfileOrg(raw []byte) string { return parseOAuthMeta(raw).OrganizationUUID }

// GlobalConfigExists reports whether ~/.claude.json is present. Callers that are
// about to move a credential must check this first: WriteOAuthAccount silently
// no-ops when the file is absent, so without this a swap would move the
// credential and leave no profile block to match it.
func GlobalConfigExists() bool {
	p, err := globalConfigPath()
	if err != nil {
		return false
	}
	_, serr := os.Stat(p)
	return serr == nil
}

// matchByOrg finds the stored account belonging to org. An empty org never
// matches: accounts captured before the org was recorded carry "", and treating
// that as a wildcard would let a repair write the WRONG identity's profile block
// — the precise failure this whole file exists to prevent.
func matchByOrg(all []Account, org string) *Account {
	if org == "" {
		return nil
	}
	for i := range all {
		if all[i].OrganizationUUID == org {
			return &all[i]
		}
	}
	return nil
}

// shortUUID abbreviates a UUID for display; identity comparisons always use the
// full value.
func shortUUID(u string) string {
	if len(u) > 8 {
		return u[:8] + "…"
	}
	return u
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
