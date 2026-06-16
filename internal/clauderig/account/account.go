// Package account manages multiple Claude Code logins from one machine.
//
// The model is built around what live testing proved about Claude Code:
//
//   - Refresh tokens ROTATE on every refresh, so a credential can't be a stable
//     identity and a captured snapshot of an actively-used account goes stale.
//     Accounts are therefore keyed by a stable user-assigned label, and which
//     account is live is tracked by an explicit pointer — never inferred from a
//     rotating token.
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
// lives next to it on disk, never in this struct. ID is a stable slug derived
// from the label — not from any token.
type Account struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	Email            string `json:"email,omitempty"`
	SubscriptionType string `json:"subscriptionType,omitempty"`
	OrganizationUUID string `json:"organizationUuid,omitempty"`
	AddedAt          string `json:"addedAt,omitempty"` // RFC3339
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

func (s *Store) credPath(id string) string  { return filepath.Join(s.acctDir(id), "credential.json") }
func (s *Store) metaPath(id string) string  { return filepath.Join(s.acctDir(id), "meta.json") }
func (s *Store) stalePath(id string) string { return filepath.Join(s.ConfigDir(id), ".rig-stale") }

// CaptureLive builds (or updates) an account from the current live credential.
// The label is required for a new account; passing an existing label/id updates
// it. Returns the account and whether an existing one was updated.
func (s *Store) CaptureLive(raw []byte, label string) (Account, bool, error) {
	sub, org, err := metaFromBlob(raw)
	if err != nil {
		return Account{}, false, err
	}
	id := Slugify(label)
	if id == "" {
		id = s.nextAutoID()
		label = id
	}
	_, existed := s.read(id)
	a := Account{
		ID:               id,
		Label:            label,
		SubscriptionType: sub,
		OrganizationUUID: org,
		AddedAt:          time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.save(a, raw); err != nil {
		return Account{}, false, err
	}
	return a, existed, nil
}

// nextAutoID returns the first free "account-N" id.
func (s *Store) nextAutoID() string {
	for n := 1; ; n++ {
		id := fmt.Sprintf("account-%d", n)
		if _, ok := s.read(id); !ok {
			return id
		}
	}
}

// save writes an account's metadata and credential (0600), marking any existing
// session profile stale so the next `run` re-seeds from the fresh credential.
func (s *Store) save(a Account, raw []byte) error {
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
	if err := os.WriteFile(s.credPath(a.ID), raw, 0o600); err != nil {
		return err
	}
	if hadConfig {
		_ = os.WriteFile(s.stalePath(a.ID), []byte("credential updated\n"), 0o600)
	}
	return nil
}

// List returns all tracked accounts, sorted by label then id.
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].ID < out[j].ID
	})
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

// Resolve finds an account by exact id, exact label, or unambiguous id prefix.
func (s *Store) Resolve(ref string) (Account, error) {
	all, err := s.List()
	if err != nil {
		return Account{}, err
	}
	if len(all) == 0 {
		return Account{}, errors.New("no accounts yet — run `clauderig account add` while logged in")
	}
	var prefix []Account
	for _, a := range all {
		if a.ID == ref || a.Label == ref || Slugify(ref) == a.ID {
			return a, nil
		}
		if ref != "" && strings.HasPrefix(a.ID, ref) {
			prefix = append(prefix, a)
		}
	}
	switch len(prefix) {
	case 1:
		return prefix[0], nil
	case 0:
		return Account{}, fmt.Errorf("no account matches %q", ref)
	default:
		return Account{}, fmt.Errorf("%q is ambiguous (matches %d accounts)", ref, len(prefix))
	}
}

// Credential reads a stored account's raw credential blob.
func (s *Store) Credential(id string) ([]byte, error) {
	return os.ReadFile(s.credPath(id))
}

// SaveCredential overwrites a stored account's credential (used by `switch` to
// round-trip the displaced account's fresh credential back into its store).
func (s *Store) SaveCredential(id string, raw []byte) error {
	if _, ok := s.read(id); !ok {
		return fmt.Errorf("no account %q to update", id)
	}
	a, _ := s.read(id)
	return s.save(a, raw)
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
// returns it. The credential is (re)seeded only when the profile is new or marked
// stale — otherwise the session's own self-refreshed token is left intact (it
// rotates independently of the store). When share is true, SharedEntries from
// claudeHome are linked in (idempotent).
func (s *Store) EnsureSession(a Account, share bool, claudeHome string) (string, error) {
	dir := s.ConfigDir(a.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	credFile := filepath.Join(dir, ".credentials.json")
	_, haveCred := statOK(credFile)
	stale := fileExists(s.stalePath(a.ID))
	if !haveCred || stale {
		raw, err := s.Credential(a.ID)
		if err != nil {
			return "", fmt.Errorf("read stored credential: %w", err)
		}
		if err := os.WriteFile(credFile, raw, 0o600); err != nil {
			return "", err
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
