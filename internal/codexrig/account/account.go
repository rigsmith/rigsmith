package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
)

// Account is one tracked Codex login. The credential is never a field — it sits
// beside this record on disk, so a listing can be printed, logged and marshalled
// without carrying a secret through the program.
type Account struct {
	ID        string `json:"id"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	// PlanType is spelled subscriptionType on disk, matching clauderig's
	// Account record. Both stores are read directly by outside callers —
	// accounts/<id>/meta.json is the documented fallback when the rig binary is
	// not installed — so a field that means the same thing must not have two
	// names depending on which rig wrote it.
	PlanType string `json:"subscriptionType,omitempty"`
	AuthMode string `json:"authMode,omitempty"`
	AddedAt  string `json:"addedAt,omitempty"` // RFC3339
	Alias    string `json:"alias,omitempty"`
	// Disabled holds an account out of AUTOMATIC rotation only. A bare
	// `switch` skips it; `switch <id>` still works. It is not a soft delete.
	Disabled bool `json:"disabled,omitempty"`
}

// Title is the account's plain, unstyled label: the alias leads when there is
// one, because someone who named an account meant to be shown that name.
func (a Account) Title() string {
	name := a.Email
	if name == "" {
		name = a.ID
	}
	if a.Alias != "" {
		return a.Alias + " · " + name
	}
	return name
}

// Store is the on-disk account registry, rooted at ~/.codexrig.
type Store struct {
	Root string

	// scan is the running-Codex check Switch consults, injectable so a test can
	// pin it. Without that, every test of the swap asserts something about
	// whatever the developer happens to have open, and the one case that most
	// needs covering — "a live session refuses the swap" — cannot be reached at
	// all on a quiet machine.
	scan func(home string) ([]Instance, error)
}

// scanner returns the process check, defaulting to the real one.
func (s *Store) scanner() func(string) ([]Instance, error) {
	if s.scan != nil {
		return s.scan
	}
	return RunningInstancesScan
}

// DefaultStore opens the store at codexrig's config directory.
func DefaultStore() (*Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return &Store{Root: dir}, nil
}

// Session-state vocabulary, shared by the CLI, the --json contract and the TUI.
// One spelling everywhere means a script and a screen can never disagree about
// what a profile's state is called.
const (
	SessionNone     = "none"      // the account has no isolated home yet
	SessionOK       = "ok"        // the home holds a credential that can authenticate
	SessionNoTokens = "no-tokens" // a credential is there and cannot authenticate
	SessionUnknown  = "unknown"   // the home's credential could not be read
)

// Sentinel errors, so callers classify by identity rather than by message text.
var (
	ErrNoAccounts     = errors.New("no accounts yet — run `codexrig account add` while logged in")
	ErrNoSuchAccount  = errors.New("no account matches")
	ErrAmbiguousRef   = errors.New("ambiguous account reference")
	ErrStoredNoTokens = errors.New("the stored credential cannot authenticate")
	ErrHomeUnreadable = errors.New("could not read the account's credential")
	ErrCodexBusy      = errors.New("Codex is running")
	ErrProcessScan    = errors.New("could not scan for running Codex processes")
	// ErrUnmapped means there are several accounts and nothing said which.
	// Its own error rather than a generic failure, because a caller can act
	// on it: name an account, or bind the directory to one.
	ErrUnmapped = errors.New("several accounts, and this directory is not bound to one")
)

// SharedEntries is what an account's isolated home links back to the machine's
// own, so several logins share one setup instead of drifting into several.
//
// The list is conservative in one direction only: everything omitted is simply
// per-account, which costs convenience and never safety. auth.json is absent and
// must stay absent — it is the whole reason the homes are separate. So are
// sessions/ and the state databases: two logins sharing a thread index would
// each list the other's conversations, and Codex's own writer locks are keyed per
// home.
var SharedEntries = []string{
	"config.toml",
	"AGENTS.md",
	"skills",
	"rules",
	"prompts",
	"themes",
}

var (
	aliasRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,31}$`)
	slugRe  = regexp.MustCompile(`[^a-z0-9]+`)
)

// Slugify turns a label into a stable, filesystem-safe id.
func Slugify(label string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(label)), "-")
	return strings.Trim(s, "-")
}

func (s *Store) accountsDir() string  { return filepath.Join(s.Root, "accounts") }
func (s *Store) dir(id string) string { return filepath.Join(s.accountsDir(), id) }

// HomeDir is the account's isolated CODEX_HOME. Persistent: Codex refreshes its
// own tokens in there, and a home that is re-seeded on every run would throw
// that refresh away and eventually hand back an expired credential.
func (s *Store) HomeDir(id string) string { return filepath.Join(s.dir(id), "home") }

func (s *Store) metaPath(id string) string  { return filepath.Join(s.dir(id), "meta.json") }
func (s *Store) credPath(id string) string  { return filepath.Join(s.dir(id), "auth.json") }
func (s *Store) stalePath(id string) string { return filepath.Join(s.HomeDir(id), ".rig-stale") }
func (s *Store) activePath() string         { return filepath.Join(s.accountsDir(), "active.json") }

// CaptureLive records the machine's live login as an account, creating it or
// refreshing an existing one.
//
// Naming is by EMAIL, never by a token: Codex rotates its refresh token on every
// refresh, so a token-keyed account would become a different account several
// times a day. An API-key login has no email, so it is named by the key's own
// account id, and failing that refused — an account codexrig cannot name is one
// it can never resolve again.
func (s *Store) CaptureLive(cred []byte) (Account, bool, error) {
	if !HasTokens(cred) {
		return Account{}, false, errors.New("the live credential has no usable token (is `codex login` finished?)")
	}
	id := IdentityOf(cred)
	label := id.Email
	if label == "" {
		label = id.AccountID
	}
	if label == "" {
		return Account{}, false, errors.New("could not determine who this credential belongs to — codexrig needs an email (ChatGPT login) or an account id to name it by")
	}

	existing, _ := s.List()
	accountID := strings.TrimSpace(id.AccountID)
	slug := Slugify(label)
	if slug == "" {
		slug = "account"
	}
	// Reuse the id of the account with the same (email, account id) pair; only
	// a genuine collision — same email, different account — gets a suffix.
	chosen := ""
	taken := map[string]bool{}
	for _, a := range existing {
		taken[a.ID] = true
		if strings.EqualFold(a.Email, id.Email) && a.AccountID == accountID {
			chosen = a.ID
		}
	}
	created := chosen == ""
	if created {
		chosen = slug
		for n := 2; taken[chosen]; n++ {
			chosen = fmt.Sprintf("%s-%d", slug, n)
		}
	}

	acct := Account{
		ID:        chosen,
		Email:     id.Email,
		Name:      id.Name,
		AccountID: accountID,
		PlanType:  id.PlanType,
		AuthMode:  id.AuthMode,
		AddedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	// Carry forward what belongs to the account rather than to the credential.
	for _, prev := range existing {
		if prev.ID != chosen {
			continue
		}
		acct.Alias = prev.Alias
		acct.Disabled = prev.Disabled
		if prev.AddedAt != "" {
			acct.AddedAt = prev.AddedAt
		}
		if acct.AccountID == "" {
			acct.AccountID = prev.AccountID
		}
	}
	if err := s.save(acct, cred); err != nil {
		return Account{}, false, err
	}
	return acct, !created, nil
}

func (s *Store) save(a Account, cred []byte) error {
	if err := os.MkdirAll(s.dir(a.ID), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.metaPath(a.ID), append(b, '\n'), 0o600); err != nil {
		return err
	}
	if len(cred) > 0 {
		if err := os.WriteFile(s.credPath(a.ID), cred, 0o600); err != nil {
			return err
		}
		// An isolated home that already exists is holding the PREVIOUS
		// credential, and nothing about its own files changed when this one
		// did — so EnsureHome's "it already authenticates" test would keep
		// handing back the stale login forever. Mark it instead.
		if dirExists(s.HomeDir(a.ID)) {
			_ = os.WriteFile(s.stalePath(a.ID), []byte("credential updated\n"), 0o600)
		}
	}
	return nil
}

func (s *Store) updateMeta(id string, mutate func(*Account)) error {
	a, err := s.load(id)
	if err != nil {
		return err
	}
	mutate(&a)
	return s.save(a, nil)
}

func (s *Store) load(id string) (Account, error) {
	var a Account
	b, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return a, err
	}
	if a.ID == "" {
		a.ID = id
	}
	return a, nil
}

// List returns every tracked account, sorted by email so the order a person sees
// does not change between runs.
func (s *Store) List() ([]Account, error) {
	entries, err := os.ReadDir(s.accountsDir())
	if os.IsNotExist(err) {
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
		a, err := s.load(e.Name())
		if err != nil {
			continue // a directory without readable meta is not an account
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Email != out[j].Email {
			return out[i].Email < out[j].Email
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Enabled is List minus the accounts held out of automatic rotation.
func (s *Store) Enabled() ([]Account, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []Account
	for _, a := range all {
		if !a.Disabled {
			out = append(out, a)
		}
	}
	return out, nil
}

// Resolve turns a user-typed reference into an account: an exact id, then an
// alias, then a full email, then a unique substring of any of the three.
//
// Ambiguity is always an error, never a guess. The same email in two Codex
// accounts is a real case, and picking one of them silently would switch the
// machine to the wrong login and report success.
func (s *Store) Resolve(ref string) (Account, error) {
	ref = strings.TrimSpace(ref)
	all, err := s.List()
	if err != nil {
		return Account{}, err
	}
	if len(all) == 0 {
		return Account{}, ErrNoAccounts
	}
	if ref == "" {
		return Account{}, ErrNoSuchAccount
	}
	for _, a := range all {
		if a.ID == ref {
			return a, nil
		}
	}
	for _, a := range all {
		if a.Alias != "" && strings.EqualFold(a.Alias, ref) {
			return a, nil
		}
	}
	var byEmail []Account
	for _, a := range all {
		if strings.EqualFold(a.Email, ref) {
			byEmail = append(byEmail, a)
		}
	}
	if len(byEmail) == 1 {
		return byEmail[0], nil
	}
	if len(byEmail) > 1 {
		return Account{}, fmt.Errorf("%w: %q names %d accounts (%s) — use an id", ErrAmbiguousRef, ref, len(byEmail), joinIDs(byEmail))
	}
	needle := strings.ToLower(ref)
	var part []Account
	for _, a := range all {
		if strings.Contains(strings.ToLower(a.Email), needle) ||
			strings.Contains(strings.ToLower(a.ID), needle) ||
			(a.Alias != "" && strings.Contains(strings.ToLower(a.Alias), needle)) {
			part = append(part, a)
		}
	}
	switch len(part) {
	case 0:
		return Account{}, fmt.Errorf("%w: %q", ErrNoSuchAccount, ref)
	case 1:
		return part[0], nil
	default:
		return Account{}, fmt.Errorf("%w: %q matches %d accounts (%s)", ErrAmbiguousRef, ref, len(part), joinIDs(part))
	}
}

func joinIDs(as []Account) string {
	ids := make([]string, 0, len(as))
	for _, a := range as {
		ids = append(ids, a.ID)
	}
	return strings.Join(ids, ", ")
}

// SetAlias names an account. It refuses an alias that already resolves to a
// different account by id, email or alias: shadowing would silently redirect a
// switch to the wrong login, which is the one mistake this store must not allow.
func (s *Store) SetAlias(id, alias string) error {
	alias = strings.TrimSpace(alias)
	if !aliasRe.MatchString(alias) {
		return fmt.Errorf("alias %q must start alphanumeric and use only letters, digits, dot, dash or underscore (max 32)", alias)
	}
	all, err := s.List()
	if err != nil {
		return err
	}
	for _, a := range all {
		if a.ID == id {
			continue
		}
		if strings.EqualFold(a.ID, alias) || strings.EqualFold(a.Email, alias) || (a.Alias != "" && strings.EqualFold(a.Alias, alias)) {
			return fmt.Errorf("alias %q already resolves to %s — pick another", alias, a.Title())
		}
	}
	return s.updateMeta(id, func(a *Account) { a.Alias = alias })
}

// ClearAlias removes an account's alias.
func (s *Store) ClearAlias(id string) error {
	return s.updateMeta(id, func(a *Account) { a.Alias = "" })
}

// SetDisabled holds an account out of automatic rotation, or puts it back.
func (s *Store) SetDisabled(id string, disabled bool) error {
	return s.updateMeta(id, func(a *Account) { a.Disabled = disabled })
}

// Credential reads an account's stored credential.
func (s *Store) Credential(id string) ([]byte, error) { return os.ReadFile(s.credPath(id)) }

// SaveCredential stores a fresh credential for an account — the round trip that
// keeps a displaced login usable after Codex has refreshed it.
//
// It REFUSES a blob that cannot authenticate. That refusal is the whole point:
// the stored copy is the only one left once the machine has switched away, and
// overwriting it with a logged-out file would lose the account for good.
func (s *Store) SaveCredential(id string, raw []byte) error {
	if !HasTokens(raw) {
		return fmt.Errorf("refusing to store a credential for %s that cannot authenticate", id)
	}
	a, err := s.load(id)
	if err != nil {
		return err
	}
	return s.save(a, raw)
}

// Remove forgets an account and deletes its isolated home. It never touches the
// machine's live login: forgetting an account is not logging it out.
func (s *Store) Remove(id string) error {
	if err := os.RemoveAll(s.dir(id)); err != nil {
		return err
	}
	if cur, _ := s.Active(); cur == id {
		return os.Remove(s.activePath())
	}
	return nil
}

// Purge forgets every account and deletes every isolated home and backup.
func (s *Store) Purge() error {
	if err := os.RemoveAll(s.accountsDir()); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.Root, "cred-backups"))
}

// Active reports which account codexrig believes the machine is logged into.
//
// An explicit pointer, never inferred from the credential. Two accounts can
// share an email, an expired id_token still names its owner, and an API-key
// login names nobody — so "which one is live" is a fact codexrig recorded when
// it made the swap, not something it re-derives and gets wrong.
func (s *Store) Active() (string, error) {
	b, err := os.ReadFile(s.activePath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	return v.ID, nil
}

// SetActive records which account is live.
func (s *Store) SetActive(id string) error {
	if err := os.MkdirAll(s.accountsDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(struct {
		ID string `json:"id"`
	}{id}, "", "  ")
	return os.WriteFile(s.activePath(), append(b, '\n'), 0o600)
}

// BackupLive keeps a timestamped copy of the credential a switch is about to
// displace, so a swap that goes wrong is recoverable by hand.
func (s *Store) BackupLive(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	dir := filepath.Join(s.Root, "cred-backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "live-"+time.Now().UTC().Format("20060102-150405.000000000")+".json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// EnsureHome makes an account's isolated CODEX_HOME runnable and returns its
// path. When share is true, the shared setup is linked in from the machine's own
// home.
//
// Seeding policy, in order, and each clause is load-bearing:
//   - the home already authenticates and is not marked stale ⇒ leave it alone.
//     It has been refreshing its own tokens; the stored copy is older.
//   - its credential is unreadable ⇒ refuse both ways rather than overwrite
//     something that might be fine.
//   - it needs seeding and the stored credential cannot authenticate ⇒ refuse,
//     naming the repair.
func (s *Store) EnsureHome(a Account, share bool) (string, error) {
	home := s.HomeDir(a.ID)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}

	stale := fileExists(s.stalePath(a.ID))
	usable, readErr := homeAuthenticates(home)
	if readErr != nil {
		return "", fmt.Errorf("%w for %s: %v", ErrHomeUnreadable, a.Title(), readErr)
	}
	if !usable || stale {
		cred, err := s.Credential(a.ID)
		if err != nil {
			return "", fmt.Errorf("%s has no stored credential: %w", a.Title(), err)
		}
		if !HasTokens(cred) {
			if usable {
				// The home still works. Do not break it to install a copy
				// that does not.
				_ = os.Remove(s.stalePath(a.ID))
			} else {
				return "", fmt.Errorf("%w: %s — run `codexrig account run %s` and `codex login`, then `codexrig account add --from-home %s`", ErrStoredNoTokens, a.Title(), a.ID, a.ID)
			}
		} else {
			if err := writeAuthAt(home, cred); err != nil {
				return "", err
			}
			_ = os.Remove(s.stalePath(a.ID))
		}
	}

	if share {
		machineHome, err := codexhome.Default()
		if err != nil {
			return "", err
		}
		for _, name := range SharedEntries {
			if err := linkOrCopy(filepath.Join(machineHome, name), filepath.Join(home, name)); err != nil {
				return "", fmt.Errorf("share %s: %w", name, err)
			}
		}
	}
	return home, nil
}

// CaptureFromHome repairs an account's stored credential from its own isolated
// home — the path back from "I logged this account in inside its own home".
func (s *Store) CaptureFromHome(a Account) error {
	home := s.HomeDir(a.ID)
	cred, err := readAuthAt(home)
	if err != nil {
		return err
	}
	if !HasTokens(cred) {
		return fmt.Errorf("%s's home holds no usable credential — run `codexrig account run %s` and log in first", a.Title(), a.ID)
	}
	// Refuse to file one login's credential under another's name.
	got := IdentityOf(cred)
	if a.Email != "" && got.Email != "" && !strings.EqualFold(a.Email, got.Email) {
		return fmt.Errorf("%s's home now authenticates as %s — capture it as its own account instead of overwriting this one", a.Title(), got.Email)
	}
	if err := s.SaveCredential(a.ID, cred); err != nil {
		return err
	}
	return os.Remove(s.stalePath(a.ID))
}

// CredentialHealthy reports whether a switch would accept the stored credential.
func (s *Store) CredentialHealthy(id string) bool {
	b, err := s.Credential(id)
	return err == nil && HasTokens(b)
}

// HomeStatus reports the state of an account's isolated home, in the shared
// vocabulary.
func (s *Store) HomeStatus(id string) string {
	home := s.HomeDir(id)
	if !dirExists(home) {
		return SessionNone
	}
	b, err := readAuthAt(home)
	switch {
	case errors.Is(err, ErrNoLive):
		return SessionNone
	case err != nil:
		return SessionUnknown
	}
	if HasTokens(b) {
		return SessionOK
	}
	return SessionNoTokens
}

// HomeIdentity reports who an account's isolated home currently authenticates
// as. Empty values mean "not recorded", never "matches".
func (s *Store) HomeIdentity(id string) (Identity, error) {
	b, err := readAuthAt(s.HomeDir(id))
	if err != nil {
		return Identity{}, err
	}
	return IdentityOf(b), nil
}

// StoredStatus is one account's health, as `list --json`, `doctor` and the TUI
// all report it.
type StoredStatus struct {
	Account
	Active           bool   `json:"active"`
	CredentialTokens bool   `json:"credentialTokens"`
	Home             string `json:"home"`
}

// StoredStatuses reports every account's health, in List order.
func (s *Store) StoredStatuses() ([]StoredStatus, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	active, _ := s.Active()
	out := make([]StoredStatus, 0, len(all))
	for _, a := range all {
		out = append(out, StoredStatus{
			Account:          a,
			Active:           a.ID == active,
			CredentialTokens: s.CredentialHealthy(a.ID),
			Home:             s.HomeStatus(a.ID),
		})
	}
	return out, nil
}

func homeAuthenticates(home string) (bool, error) {
	b, err := readAuthAt(home)
	if errors.Is(err, ErrNoLive) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return HasTokens(b), nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// linkOrCopy points dst at src, preferring a symlink and falling back to a full
// recursive copy where symlinks need a privilege the machine will not give
// (Windows without Developer Mode).
//
// An existing REAL file or directory at dst is left alone: it is the account's
// own, and replacing it with a link to the machine's would delete whatever the
// account had. An existing symlink is replaced, since that is ours.
func linkOrCopy(src, dst string) error {
	if _, err := os.Lstat(src); err != nil {
		return nil // nothing to share
	}
	if st, err := os.Lstat(dst); err == nil {
		if st.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	return copyAll(src, dst)
}

func copyAll(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		_ = os.Remove(dst)
		return os.Symlink(target, dst)
	case st.IsDir():
		if err := os.MkdirAll(dst, st.Mode().Perm()); err != nil {
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
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}
