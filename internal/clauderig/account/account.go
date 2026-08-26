// Package account manages multiple Claude Code logins from one machine.
//
// The model is built around what live testing proved about Claude Code:
//
//   - Refresh tokens ROTATE on every refresh, so a credential can't be a stable
//     identity and a captured snapshot of an actively-used account goes stale.
//     Accounts are therefore keyed by the account EMAIL (from ~/.claude.json's
//     oauthAccount; same email in two orgs gets a numeric suffix), and which
//     account is live is tracked by an explicit pointer — never inferred from a
//     rotating token. Accounts resolve by a unique substring of email/id.
//
//   - Mutating the live credential (the macOS Keychain / ~/.claude file) out
//     from under a running Claude Code instance forces a re-login. So `switch`
//     is guarded by process detection (see livesession.go) and round-trips the
//     displaced account's current credential back into its store.
//
//   - Session mode (`run`) never touches the live store: each account gets a
//     persistent CLAUDE_CONFIG_DIR that self-refreshes its own tokens in
//     isolation. That's the safe, primary path.
//
//   - On macOS, Claude Code keeps a session profile's tokens in a per-profile
//     Keychain entry (service "Claude Code-credentials-<sha256(configDir)[:8]>")
//     and leaves the profile's .credentials.json as a token-less stub — see
//     sessionstore.go. All session credential reads/seeds must go through that
//     layer; the file alone lies.
//
// The idea — and the safety mechanisms (process detection, security -i writes,
// round-trip backup) — are credited to claude-swap by realiti4
// (https://github.com/realiti4/claude-swap, MIT). This is a clean-room Go
// reimplementation inside clauderig.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// ErrNoLive means no live Claude Code credential was found — the machine isn't
// logged in (no Keychain entry / no .credentials.json).
var ErrNoLive = errors.New("no live Claude Code credential found (run `claude` and log in first)")

// SharedEntries are the ~/.claude customizations linked into a session profile
// when sharing is on (the default). Credentials, history, and global state are
// deliberately absent so sessions stay isolated where it matters.
var SharedEntries = []string{
	"settings.json",
	"settings.local.json",
	"CLAUDE.md",
	"keybindings.json",
	"skills",
	"commands",
	"agents",
	"output-styles",
	"plugins",
}

// Account is the metadata clauderig tracks for one login. The credential itself
// lives next to it on disk, never in this struct. Identity is the account EMAIL
// (from ~/.claude.json oauthAccount); ID is a filesystem-safe slug of it. Never
// derived from a token (those rotate).
type Account struct {
	ID               string `json:"id"`
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType,omitempty"`
	OrganizationUUID string `json:"organizationUuid,omitempty"`
	// AccountUUID is the account's own uuid from ~/.claude.json's oauthAccount.
	//
	// It is the join key everything else uses: Desktop names the account by
	// uuid in its sidecar path, and the ledger records attribution by uuid. An
	// email is only ever a label a person types. Without it here, resolving an
	// alias to a uuid depends on the device registry, which holds only each
	// device's LATEST account — so once every device has synced under a
	// different login, an older account's alias stops resolving even though its
	// sessions are still attributed.
	//
	// Empty for accounts captured before this was recorded; the registry
	// remains the fallback for those.
	AccountUUID string `json:"accountUuid,omitempty"`
	AddedAt     string `json:"addedAt,omitempty"` // RFC3339
	// Alias is a short handle the user chose — usable anywhere an id or email
	// is, so `switch dev` works. Optional and unique across the store.
	Alias string `json:"alias,omitempty"`
	// Disabled holds an account out of AUTOMATIC selection (bare `switch`
	// rotation) while leaving it fully tracked and switchable by name. It is a
	// "not this one, not right now" marker, not a soft delete: a work account
	// nobody should land on by accident, or one being rested.
	Disabled bool `json:"disabled,omitempty"`
}

// Title renders the account for a listing: its alias if it has one, else email.
func (a Account) Title() string {
	if a.Alias != "" {
		return a.Alias + " · " + a.Email
	}
	return a.Email
}

// blob mirrors the shape Claude Code persists. Only display metadata is modeled;
// the raw bytes are stored verbatim so unknown fields survive.
type blob struct {
	ClaudeAiOauth struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
	OrganizationUUID string `json:"organizationUuid"`
}

// metaFromBlob pulls display-only metadata (subscription, org) from a credential.
// It never derives identity from the token. A blob with no OAuth token is an
// error — that's not a logged-in credential.
func metaFromBlob(raw []byte) (subscription, org string, err error) {
	var b blob
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", "", fmt.Errorf("parse credential: %w", err)
	}
	if b.ClaudeAiOauth.AccessToken == "" && b.ClaudeAiOauth.RefreshToken == "" {
		return "", "", errors.New("credential has no OAuth token (is Claude Code logged in?)")
	}
	return b.ClaudeAiOauth.SubscriptionType, b.OrganizationUUID, nil
}

// Slugify turns a label into a filesystem-safe, stable account id.
func Slugify(label string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(label)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Store is clauderig's on-disk account registry, rooted at ~/.clauderig.
type Store struct{ Root string }

// DefaultStore roots the registry at clauderig's config dir (~/.clauderig).
func DefaultStore() (*Store, error) {
	d, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return &Store{Root: d}, nil
}

func (s *Store) accountsDir() string      { return filepath.Join(s.Root, "accounts") }
func (s *Store) backupsDir() string       { return filepath.Join(s.Root, "cred-backups") }
func (s *Store) acctDir(id string) string { return filepath.Join(s.accountsDir(), id) }
func (s *Store) activePath() string       { return filepath.Join(s.accountsDir(), "active.json") }

// ConfigDir is the persistent CLAUDE_CONFIG_DIR for an account's sessions.
func (s *Store) ConfigDir(id string) string { return filepath.Join(s.acctDir(id), "config") }

func (s *Store) credPath(id string) string { return filepath.Join(s.acctDir(id), "credential.json") }
func (s *Store) metaPath(id string) string { return filepath.Join(s.acctDir(id), "meta.json") }
func (s *Store) oauthPath(id string) string {
	return filepath.Join(s.acctDir(id), "oauth-account.json")
}

// SaveOAuth stores an account's oauthAccount block (identity + plan); no-op if
// empty. OAuth reads it back ((nil, nil) when absent).
func (s *Store) SaveOAuth(id string, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	return os.WriteFile(s.oauthPath(id), raw, 0o600)
}

func (s *Store) OAuth(id string) ([]byte, error) {
	b, err := os.ReadFile(s.oauthPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}
func (s *Store) stalePath(id string) string { return filepath.Join(s.ConfigDir(id), ".rig-stale") }

// CaptureLive builds (or updates) an account from the live credential (cred) and
// the live oauthAccount block (oauth, from ~/.claude.json), which supplies the
// email identity and the plan. The email is required — it's how accounts are
// keyed — so a credential with no associated oauthAccount/email is an error.
// Returns the account and whether an existing one (same email) was updated.
func (s *Store) CaptureLive(cred, oauth []byte) (Account, bool, error) {
	sub, blobOrg, err := metaFromBlob(cred)
	if err != nil {
		return Account{}, false, err
	}
	m := parseOAuthMeta(oauth)
	email := m.EmailAddress
	if email == "" {
		return Account{}, false, errors.New("could not determine the account email from ~/.claude.json (is Claude Code fully logged in?)")
	}
	org := m.OrganizationUUID // the account/org identity; falls back to the credential's
	if org == "" {
		org = blobOrg
	}
	id, existed := s.idFor(email, org)

	a := Account{
		ID:               id,
		Email:            email,
		SubscriptionType: sub,
		OrganizationUUID: org,
		AccountUUID:      m.AccountUUID,
		AddedAt:          time.Now().UTC().Format(time.RFC3339),
	}
	// Re-capturing an existing account must not undo the user's own settings.
	// This builds a fresh record from the live login, which knows nothing about
	// an alias or a disable — so without carrying them over, `account add` (the
	// documented way to refresh an expired credential) would silently drop an
	// alias and put a deliberately disabled account back into rotation.
	if existed {
		if prev, ok := s.read(id); ok {
			a.Alias = prev.Alias
			a.Disabled = prev.Disabled
			// A live block that omits the uuid must not erase one already
			// recorded — losing it silently breaks alias resolution for every
			// session attributed to this account.
			if a.AccountUUID == "" {
				a.AccountUUID = prev.AccountUUID
			}
			if prev.AddedAt != "" {
				a.AddedAt = prev.AddedAt // when it was first tracked, not last refreshed
			}
		}
	}
	if err := s.save(a, cred, authoritative); err != nil {
		return Account{}, false, err
	}
	if err := s.SaveOAuth(id, oauth); err != nil {
		return Account{}, false, err
	}
	return a, existed, nil
}

// idFor returns the store id for an (email, org) account. Re-capturing the same
// account (same email AND org) reuses its id. A genuinely new account gets
// Slugify(email); only when that email is already taken by a DIFFERENT org does
// it fall back to a numeric suffix (-2, -3, …) — claude-swap's scheme, used only
// when emails actually collide.
func (s *Store) idFor(email, org string) (id string, existed bool) {
	all, _ := s.List()
	for _, a := range all {
		if a.Email == email && a.OrganizationUUID == org {
			return a.ID, true
		}
	}
	base := Slugify(email)
	used := make(map[string]bool, len(all))
	for _, a := range all {
		used[a.ID] = true
	}
	if !used[base] {
		return base, false
	}
	for n := 2; ; n++ {
		if cand := fmt.Sprintf("%s-%d", base, n); !used[cand] {
			return cand, false
		}
	}
}

// save writes an account's metadata and credential (0600), marking any existing
// session profile stale so the next `run` re-seeds from the fresh credential.
// mergeCredential carries forward top-level fields the incoming blob does not
// carry itself, so a narrower source cannot silently strip the account's record.
//
// What may be carried forward depends on WHO is writing, because the two sources
// mean different things by an absent field:
//
//   - partial (a session profile's Keychain entry, which Claude Code writes
//     holding only `claudeAiOauth`): absence carries no information at all. Every
//     missing field is filled in from the stored copy. Observed on a real
//     machine: a credential that had all three fields at 20:08 had exactly one by
//     04:26, because `add --from-session` stored the narrow blob verbatim and the
//     next switch wrote the reduced version live.
//
//   - authoritative (the live credential itself): absence usually MEANS removed,
//     so a capture must be able to delete. Log out of an MCP server and run
//     `account add`, and `mcpOAuth` is legitimately gone; carrying it forward
//     would make the removal impossible to express and let a later switch restore
//     tokens the user revoked.
//
// The one exception is `organizationUuid`, which is carried forward from either
// source. Claude Code does not always write it — verified on this machine, where
// one account's live credential carries it and the other's does not, and the
// identity journal recorded `credOrg: f1eab509… → (none)` across a switch. It is
// also the only field `doctor` can compare against the profile block, so losing
// it does not degrade the desync check, it silently turns it into an
// unconditional all-clear. Treating its absence as a deletion would therefore
// delete exactly the wrong thing.
//
// Merging is safe because this is always the SAME account's own record — a
// repair or a round-trip of its own credential — never another account's.
func (s *Store) mergeCredential(id string, raw []byte, src credentialSource) []byte {
	var incoming map[string]json.RawMessage
	if json.Unmarshal(raw, &incoming) != nil {
		return raw // not an object we understand; store it untouched
	}
	prev, err := os.ReadFile(s.credPath(id))
	if err != nil {
		return raw
	}
	var existing map[string]json.RawMessage
	if json.Unmarshal(prev, &existing) != nil {
		return raw
	}
	added := false
	for k, v := range existing {
		if _, present := incoming[k]; present {
			continue
		}
		if src == authoritative && k != "organizationUuid" {
			continue // an authoritative omission is a deletion
		}
		incoming[k] = v
		added = true
	}
	if !added {
		return raw
	}
	merged, merr := json.MarshalIndent(incoming, "", "  ")
	if merr != nil {
		return raw
	}
	return merged
}

// credentialSource distinguishes a complete credential from the partial one the
// session-repair path reads, because only the caller knows which it holds.
type credentialSource bool

const (
	// authoritative: the blob is the whole credential as Claude Code has it, so
	// what it omits is genuinely gone and must be stored as omitted.
	authoritative credentialSource = false
	// partial: a per-profile Keychain entry, which holds only `claudeAiOauth`.
	// Storing it verbatim would strip `organizationUuid` and `mcpOAuth` from the
	// account's record.
	partial credentialSource = true
)

func (s *Store) save(a Account, raw []byte, src credentialSource) error {
	dir := s.acctDir(a.ID)
	hadConfig := dirExists(s.ConfigDir(a.ID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	meta, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.metaPath(a.ID), meta, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(s.credPath(a.ID), s.mergeCredential(a.ID, raw, src), 0o600); err != nil {
		return err
	}
	if hadConfig {
		_ = os.WriteFile(s.stalePath(a.ID), []byte("credential updated\n"), 0o600)
	}
	return nil
}

// List returns all tracked accounts, sorted by email.
func (s *Store) List() ([]Account, error) {
	entries, err := os.ReadDir(s.accountsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Account
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if a, ok := s.read(e.Name()); ok {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

func (s *Store) read(id string) (Account, bool) {
	raw, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return Account{}, false
	}
	var a Account
	if json.Unmarshal(raw, &a) != nil {
		return Account{}, false
	}
	return a, true
}

// Resolve finds an account by exact id or email, otherwise by a unique
// case-insensitive substring of the email or id. Ambiguous matches error.
func (s *Store) Resolve(ref string) (Account, error) {
	all, err := s.List()
	if err != nil {
		return Account{}, err
	}
	if len(all) == 0 {
		return Account{}, errors.New("no accounts yet — run `clauderig account add` while logged in")
	}
	// An exact id, email or alias wins outright (even if it's a substring of
	// another). Aliases are compared case-insensitively: they are typed by hand,
	// and SetAlias already refuses one that would shadow another account.
	for _, a := range all {
		if a.ID == ref || a.Email == ref {
			return a, nil
		}
		if a.Alias != "" && strings.EqualFold(a.Alias, ref) {
			return a, nil
		}
	}
	// Otherwise fuzzy: a case-insensitive substring of the email or id — so
	// "relate"/"rel" find john@relatecpa.com and "bright"/"bri" find brightshore.
	lc := strings.ToLower(strings.TrimSpace(ref))
	var matches []Account
	if lc != "" {
		for _, a := range all {
			if strings.Contains(strings.ToLower(a.Email), lc) || strings.Contains(strings.ToLower(a.ID), lc) ||
				(a.Alias != "" && strings.Contains(strings.ToLower(a.Alias), lc)) {
				matches = append(matches, a)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Account{}, fmt.Errorf("no account matches %q", ref)
	default:
		emails := make([]string, len(matches))
		for i, a := range matches {
			emails[i] = a.Email
		}
		return Account{}, fmt.Errorf("%q matches %d accounts (%s) — be more specific", ref, len(matches), strings.Join(emails, ", "))
	}
}

// aliasRe keeps an alias short, typeable, and unambiguous against the ids and
// emails it shares a namespace with.
var aliasRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,31}$`)

// SetAlias gives an account a short handle. The alias shares a namespace with
// ids and emails — Resolve matches all three — so it is refused when it would
// shadow another account's identity, which would silently redirect a switch.
func (s *Store) SetAlias(id, alias string) error {
	alias = strings.TrimSpace(alias)
	if !aliasRe.MatchString(alias) {
		return fmt.Errorf("invalid alias %q: use letters, digits, dot, dash or underscore (max 32, must start alphanumeric)", alias)
	}
	all, err := s.List()
	if err != nil {
		return err
	}
	lc := strings.ToLower(alias)
	for _, a := range all {
		if a.ID == id {
			continue
		}
		if strings.ToLower(a.ID) == lc || strings.ToLower(a.Email) == lc || strings.ToLower(a.Alias) == lc {
			return fmt.Errorf("alias %q already identifies %s — pick another", alias, a.Email)
		}
	}
	return s.updateMeta(id, func(a *Account) { a.Alias = alias })
}

// ClearAlias removes an account's alias.
func (s *Store) ClearAlias(id string) error {
	return s.updateMeta(id, func(a *Account) { a.Alias = "" })
}

// SetDisabled holds an account out of automatic selection, or returns it.
func (s *Store) SetDisabled(id string, disabled bool) error {
	return s.updateMeta(id, func(a *Account) { a.Disabled = disabled })
}

// updateMeta rewrites ONLY an account's meta.json.
//
// Deliberately not routed through save(), which also writes the credential:
// these are display/policy edits, and re-writing a credential to change a label
// would put the account's tokens through a needless read-modify-write — the
// exact operation that has already cost this project two logouts.
func (s *Store) updateMeta(id string, mutate func(*Account)) error {
	a, ok := s.read(id)
	if !ok {
		return fmt.Errorf("no such account: %s", id)
	}
	mutate(&a)
	body, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(id), body, 0o600)
}

// Enabled returns the accounts eligible for automatic selection, in List order.
func (s *Store) Enabled() ([]Account, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(all))
	for _, a := range all {
		if !a.Disabled {
			out = append(out, a)
		}
	}
	return out, nil
}

// Credential reads a stored account's raw credential blob.
func (s *Store) Credential(id string) ([]byte, error) {
	return os.ReadFile(s.credPath(id))
}

// SaveCredential overwrites a stored account's credential (used by `switch` to
// round-trip the displaced account's fresh credential back into its store).
// A token-less blob is refused: Claude Code blanks the live tokens when a login
// expires or logs out, and round-tripping that stub would destroy the last good
// stored credential (observed live 2026-08-18).
func (s *Store) SaveCredential(id string, raw []byte) error {
	a, ok := s.read(id)
	if !ok {
		return fmt.Errorf("no account %q to update", id)
	}
	if !hasTokens(raw) {
		return fmt.Errorf("refusing to overwrite the stored credential for %s with a token-less blob (expired or logged-out login)", a.Email)
	}
	return s.save(a, raw, authoritative)
}

// Remove deletes a tracked account — credential, metadata, and session profile.
// It never touches the live login. If the removed account was active, the active
// pointer is cleared.
func (s *Store) Remove(id string) error {
	if active, _ := s.Active(); active == id {
		_ = os.Remove(s.activePath())
	}
	return os.RemoveAll(s.acctDir(id))
}

// Purge removes all account data (accounts + credential backups). Never touches
// the live login.
func (s *Store) Purge() error {
	for _, d := range []string{s.accountsDir(), s.backupsDir()} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	return nil
}

// Active returns the id of the account clauderig last set as the live login, or
// "" if none is tracked. It's an explicit pointer, not inferred from the token.
func (s *Store) Active() (string, error) {
	raw, err := os.ReadFile(s.activePath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	return v.ID, nil
}

// SetActive records which account is now the live login.
func (s *Store) SetActive(id string) error {
	if err := os.MkdirAll(s.accountsDir(), 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(struct {
		ID string `json:"id"`
	}{id})
	return os.WriteFile(s.activePath(), raw, 0o600)
}

// BackupLive saves a credential before a swap overwrites the live store, so a
// bad swap is recoverable. Returns the backup path; an empty blob is a no-op.
func (s *Store) BackupLive(raw []byte, stamp string) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(s.backupsDir(), "live-"+stamp+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// EnsureSession makes the account's persistent CLAUDE_CONFIG_DIR ready to run and
// returns it. The credential is (re)seeded only when the profile has no usable
// credential (file or per-profile Keychain entry — see sessionstore.go) or is
// marked stale — otherwise the session's own self-refreshed token is left intact
// (it rotates independently of the store). A token-less STORED credential is
// never seeded: over a healthy session it would log the session out, so it's
// skipped there and an error anywhere else. When share is true, SharedEntries
// from claudeHome are linked in (idempotent).
func (s *Store) EnsureSession(a Account, share bool, claudeHome string) (string, error) {
	dir := s.ConfigDir(a.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	usable, uerr := sessionCredentialUsable(dir)
	if uerr != nil {
		// Can't tell whether the session still authenticates (e.g. locked
		// Keychain) — refuse to guess: seeding could clobber a live login, and
		// skipping could hand out a dead profile.
		return "", fmt.Errorf("read session credential: %w", uerr)
	}
	stale := fileExists(s.stalePath(a.ID))
	if !usable || stale {
		raw, err := s.Credential(a.ID)
		if err != nil {
			return "", fmt.Errorf("read stored credential: %w", err)
		}
		switch {
		case hasTokens(raw):
			if err := seedSessionCredential(dir, raw); err != nil {
				return "", err
			}
		case !usable:
			return "", fmt.Errorf("the stored credential for %s has no OAuth token — log in (`claude` → /login as %s) and run `clauderig account add`", a.Email, a.Email)
		}
		_ = os.Remove(s.stalePath(a.ID))
	}
	if share {
		for _, name := range SharedEntries {
			src := filepath.Join(claudeHome, name)
			if _, err := os.Lstat(src); err != nil {
				continue
			}
			if err := linkOrCopy(src, filepath.Join(dir, name)); err != nil {
				return "", fmt.Errorf("share %s: %w", name, err)
			}
		}
	}
	return dir, nil
}

// linkOrCopy points dst at src via symlink, replacing any existing link.
// Where symlinks aren't permitted (Windows without Developer Mode) it copies.
func linkOrCopy(src, dst string) error {
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return nil // a real file/dir the user customized inside the session — keep it
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	// A plain recursive copy — NOT a filtered one — so a shared customization
	// (notably plugins/ with bundled node_modules) is never silently truncated.
	return copyAll(src, dst)
}

func copyAll(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case fi.IsDir():
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyAll(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		return copyFile(src, dst, fi.Mode().Perm())
	}
}

func copyFile(src, dst string, perm os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, perm)
}

// ClaudeHome is the default Claude Code config dir (~/.claude) that sessions
// share customizations from and that process detection reads.
func ClaudeHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude"), nil
}

func dirExists(p string) bool  { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
func statOK(p string) (os.FileInfo, bool) {
	fi, err := os.Stat(p)
	return fi, err == nil
}
