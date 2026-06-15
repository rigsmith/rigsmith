// Package account manages multiple Claude Code logins from one machine.
//
// Two mechanisms, deliberately separated:
//
//   - Session mode (the safe one): write a file-based credential into a
//     per-account CLAUDE_CONFIG_DIR and exec `claude` against it, so one
//     terminal runs as a chosen account while every other terminal and the VS
//     Code extension stay on the default. Claude Code reads
//     $CLAUDE_CONFIG_DIR/.credentials.json in preference to the OS keychain, so
//     this never touches the live store and can't clobber a working login.
//
//   - Global swap: overwrite the live credential the whole machine reads
//     (macOS Keychain, or ~/.claude/.credentials.json elsewhere). Convenient
//     but machine-wide, so every running instance follows it. The displaced
//     credential is always backed up first.
//
// The idea is a clean-room Go reimplementation of claude-swap by realiti4
// (https://github.com/realiti4/claude-swap, MIT) — credit for the concept and
// the file-fallback session trick goes to that project.
package account

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/copytree"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// ErrNoLive means no live Claude Code credential was found — the machine isn't
// logged in (no Keychain entry / no .credentials.json).
var ErrNoLive = errors.New("no live Claude Code credential found (run `claude` and log in first)")

// SharedEntries are the ~/.claude customizations flowed into a session profile
// when sharing is on (the default). Credentials, history, and global state
// (.credentials.json, projects/, sessions/, .claude.json) are deliberately
// absent so sessions stay isolated where it matters.
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

// Account is the metadata rig tracks for one login. The credential blob itself
// lives next to it on disk, never in this struct.
type Account struct {
	ID               string `json:"id"`
	Label            string `json:"label,omitempty"`
	SubscriptionType string `json:"subscriptionType,omitempty"`
	OrganizationUUID string `json:"organizationUuid,omitempty"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"` // epoch ms
	AddedAt          string `json:"addedAt,omitempty"`   // RFC3339, stamped by caller
}

// blob mirrors the shape Claude Code persists (Keychain blob on macOS,
// .credentials.json elsewhere). Only the fields rig reads are modeled; unknown
// fields survive because rig stores and writes the raw bytes, never a
// re-marshaled struct.
type blob struct {
	ClaudeAiOauth struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
	OrganizationUUID string `json:"organizationUuid"`
}

// parseBlob extracts the metadata rig cares about and the stable fingerprint
// used as the account id.
func parseBlob(raw []byte) (Account, error) {
	var b blob
	if err := json.Unmarshal(raw, &b); err != nil {
		return Account{}, fmt.Errorf("parse credential: %w", err)
	}
	fp := b.ClaudeAiOauth.RefreshToken
	if fp == "" {
		fp = b.ClaudeAiOauth.AccessToken
	}
	if fp == "" {
		return Account{}, errors.New("credential has no OAuth token (is Claude Code logged in?)")
	}
	sum := sha256.Sum256([]byte(fp))
	return Account{
		ID:               hex.EncodeToString(sum[:])[:8],
		SubscriptionType: b.ClaudeAiOauth.SubscriptionType,
		OrganizationUUID: b.OrganizationUUID,
		ExpiresAt:        b.ClaudeAiOauth.ExpiresAt,
	}, nil
}

// AccountFromBlob builds a tracked Account from a raw credential blob, attaching
// a friendly label and stamping the add time (RFC3339).
func AccountFromBlob(raw []byte, label string, now time.Time) (Account, error) {
	a, err := parseBlob(raw)
	if err != nil {
		return Account{}, err
	}
	a.Label = label
	a.AddedAt = now.UTC().Format(time.RFC3339)
	return a, nil
}

// FingerprintOf returns the rig account id for a raw credential blob, or "" if
// it can't be parsed. Used to mark which stored account is currently live.
func FingerprintOf(raw []byte) string {
	a, err := parseBlob(raw)
	if err != nil {
		return ""
	}
	return a.ID
}

// Store is rig's on-disk account registry, rooted at ~/.clauderig.
type Store struct{ Root string }

// DefaultStore roots the registry at clauderig's config dir (~/.clauderig).
func DefaultStore() (*Store, error) {
	d, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return &Store{Root: d}, nil
}

func (s *Store) accountsDir() string { return filepath.Join(s.Root, "accounts") }
func (s *Store) sessionsDir() string { return filepath.Join(s.Root, "sessions") }
func (s *Store) backupsDir() string  { return filepath.Join(s.Root, "cred-backups") }
func (s *Store) acctDir(id string) string {
	return filepath.Join(s.accountsDir(), id)
}

// Save writes (or updates) an account's metadata and credential blob. The blob
// file is 0600; rig never logs or prints it.
func (s *Store) Save(a Account, raw []byte) error {
	dir := s.acctDir(a.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	meta, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "credentials.json"), raw, 0o600)
}

// List returns all tracked accounts, sorted by label then id for stable output.
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
		a, err := s.readMeta(e.Name())
		if err != nil {
			continue // skip half-written / foreign dirs rather than fail the whole list
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *Store) readMeta(id string) (Account, error) {
	raw, err := os.ReadFile(filepath.Join(s.acctDir(id), "meta.json"))
	if err != nil {
		return Account{}, err
	}
	var a Account
	if err := json.Unmarshal(raw, &a); err != nil {
		return Account{}, err
	}
	return a, nil
}

// Resolve finds an account by exact id, exact label, or unambiguous id prefix.
// An ambiguous prefix is an error rather than a silent pick.
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
		if a.ID == ref || (ref != "" && a.Label == ref) {
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
	return os.ReadFile(filepath.Join(s.acctDir(id), "credentials.json"))
}

// Next returns the account after the live one in list order, wrapping around —
// the rotation target for a bare `switch`. liveID may be "" (nothing live or
// untracked), in which case the first account is returned.
func (s *Store) Next(liveID string) (Account, error) {
	all, err := s.List()
	if err != nil {
		return Account{}, err
	}
	if len(all) == 0 {
		return Account{}, errors.New("no accounts to rotate between")
	}
	for i, a := range all {
		if a.ID == liveID {
			return all[(i+1)%len(all)], nil
		}
	}
	return all[0], nil
}

// SessionDir is the per-account profile directory used as CLAUDE_CONFIG_DIR.
func (s *Store) SessionDir(id string) string {
	return filepath.Join(s.sessionsDir(), id)
}

// PrepareSession materializes a session profile for acct and returns the
// directory to use as CLAUDE_CONFIG_DIR. The credential is refreshed from the
// store every call. When share is true, SharedEntries from claudeHome are linked
// in (symlink, falling back to a copy where symlinks aren't permitted).
func (s *Store) PrepareSession(a Account, raw []byte, share bool, claudeHome string) (string, error) {
	dir := s.SessionDir(a.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), raw, 0o600); err != nil {
		return "", err
	}
	if !share {
		return dir, nil
	}
	for _, name := range SharedEntries {
		src := filepath.Join(claudeHome, name)
		if _, err := os.Lstat(src); err != nil {
			continue // not present in the default profile; nothing to share
		}
		if err := linkOrCopy(src, filepath.Join(dir, name)); err != nil {
			return "", fmt.Errorf("share %s: %w", name, err)
		}
	}
	return dir, nil
}

// linkOrCopy points dst at src via symlink, replacing any existing link/file.
// Where symlinks aren't permitted (notably Windows without Developer Mode), it
// falls back to a recursive copy so sharing still works, just statically.
func linkOrCopy(src, dst string) error {
	// Replace an existing symlink (refresh) but never clobber a real file/dir the
	// user may have customized inside the session.
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		if err := os.Remove(dst); err != nil {
			return err
		}
	}
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if si.IsDir() {
		_, err := copytree.Copy(src, dst, false)
		return err
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

// BackupLive saves the current live credential before a global swap overwrites
// it, so a bad swap is recoverable. Returns the backup path. A nil/empty blob
// (no live login) is a no-op returning "".
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

// ClaudeHome is the default Claude Code config dir (~/.claude) that sessions
// share customizations from.
func ClaudeHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude"), nil
}
