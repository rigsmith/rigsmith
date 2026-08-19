// Package desktop runs several Claude Desktop accounts side by side, each in its
// own permanent Electron profile.
//
// The model, and why it is the opposite of what clauderig tried first:
//
// Claude Desktop holds ONE login at a time, so the obvious idea is to snapshot
// the signed-in session and restore it later. clauderig shipped that and
// withdrew it the same day (see docs/CLAUDERIG-ACCOUNTS.md). It cannot be made
// reliable: Desktop signs in twice at moments a capture cannot see, Electron
// rewrites its config and holds its cookie database open so writes underneath a
// running app are silently lost, and reading the session at all means driving a
// private Chromium sqlite schema.
//
// The model here moves no sessions at all. Each account gets its own permanent
// `--user-data-dir`, and Claude Desktop is launched against it. Logging into one
// profile cannot disturb another, because they share nothing: no snapshot, no
// restore, no cookie database, no credential ever read by us. Several windows
// can be open at once. The app owns every byte inside a profile; clauderig only
// decides which directory to launch against.
//
// Credit: this model — and the `open -n --args --user-data-dir=…` mechanism on
// macOS — is from guise by siddhjagani (https://github.com/siddhjagani/guise).
// This is an independent Go implementation that adds Windows support and wires
// the profiles into clauderig's account store and sync.
package desktop

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
)

// Profile is one saved Desktop account: a directory Claude Desktop owns, plus
// the small amount of metadata clauderig keeps beside it.
type Profile struct {
	// Name is the handle typed on the command line. Also the directory name.
	Name string `json:"name"`
	// Email is a label only — clauderig never reads Desktop's login, so this is
	// whatever the user told us, not something we verified.
	Email string `json:"email,omitempty"`
	// AccountID links this profile to a `clauderig account` entry when the names
	// line up. Purely informational: the two logins remain independent.
	AccountID  string `json:"accountId,omitempty"`
	CreatedAt  string `json:"createdAt"`
	LastOpened string `json:"lastOpened,omitempty"`

	// dir is the profile root; not serialized (it IS the location).
	dir string
}

// DataDir is the directory handed to Electron as --user-data-dir. Claude Desktop
// owns everything inside it: config, cookies, cache, its own sessions. clauderig
// creates it and then never looks in.
func (p Profile) DataDir() string { return filepath.Join(p.dir, "data") }

// Dir is the profile root (metadata + data/).
func (p Profile) Dir() string { return p.dir }

// Label renders the profile for a listing.
func (p Profile) Label() string {
	if p.Email != "" {
		return fmt.Sprintf("%s · %s", p.Name, p.Email)
	}
	return p.Name
}

// nameRe keeps names usable as directory names and as command-line handles.
var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// ValidName reports whether a profile name is safe to use as a directory name.
// Rejecting `.`/`..`/separators here is what keeps a name from escaping the
// store root — the name is concatenated into a path.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use letters, digits, dot, dash or underscore (max 64, must start alphanumeric)", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid profile name %q", name)
	}
	return nil
}

// Store is the on-disk set of Desktop profiles.
type Store struct{ Root string }

// ErrNotFound means no profile by that name is saved.
var ErrNotFound = errors.New("no such Desktop profile")

// ErrExists means a profile by that name is already saved.
var ErrExists = errors.New("Desktop profile already exists")

// NewStore roots the profile set at dir (…/.clauderig/desktop).
func NewStore(dir string) *Store { return &Store{Root: dir} }

func (s *Store) profileDir(name string) string { return filepath.Join(s.Root, name) }
func (s *Store) metaPath(name string) string {
	return filepath.Join(s.profileDir(name), "profile.json")
}

// List returns every saved profile, ordered by name.
func (s *Store) List() ([]Profile, error) {
	entries, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, lerr := s.Get(e.Name())
		if lerr != nil {
			continue // not a profile directory (or unreadable); skip rather than fail the listing
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get loads one profile by name.
//
// The name is validated here as well as in Create, because it arrives from the
// command line on every path — `open`, `quit`, `rm` — and is concatenated into a
// filesystem path. Validating only at creation would leave `rm ../something`
// reading, and then deleting, outside the store root.
func (s *Store) Get(name string) (Profile, error) {
	if err := ValidName(name); err != nil {
		return Profile{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	raw, err := os.ReadFile(s.metaPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return Profile{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if uerr := json.Unmarshal(raw, &p); uerr != nil {
		return Profile{}, fmt.Errorf("parse %s: %w", s.metaPath(name), uerr)
	}
	p.dir = s.profileDir(name)
	if p.Name == "" {
		p.Name = name
	}
	return p, nil
}

// Resolve finds a profile by name or by the email label, so either works
// wherever a profile is named.
func (s *Store) Resolve(ref string) (Profile, error) {
	if p, err := s.Get(ref); err == nil {
		return p, nil
	}
	all, err := s.List()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range all {
		if strings.EqualFold(p.Email, ref) {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
}

// Create makes a new, empty profile. The data directory is created but left
// empty — Claude Desktop populates it on first launch, and the user logs in
// there once.
func (s *Store) Create(name, email, accountID string) (Profile, error) {
	if err := ValidName(name); err != nil {
		return Profile{}, err
	}
	if _, err := s.Get(name); err == nil {
		return Profile{}, fmt.Errorf("%w: %s", ErrExists, name)
	}
	p := Profile{
		Name:      name,
		Email:     email,
		AccountID: accountID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		dir:       s.profileDir(name),
	}
	// 0700: a Desktop profile holds that account's whole logged-in session.
	if err := os.MkdirAll(p.DataDir(), 0o700); err != nil {
		return Profile{}, err
	}
	if err := s.save(p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Touch records that a profile was opened.
func (s *Store) Touch(p Profile) error {
	p.LastOpened = time.Now().UTC().Format(time.RFC3339)
	return s.save(p)
}

func (s *Store) save(p Profile) error {
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(p.Name), append(body, '\n'), 0o600)
}

// Remove deletes a profile and everything in it. The caller must ensure the
// profile's window is closed first: deleting a live Electron profile out from
// under the app leaves it writing into unlinked files.
//
// This logs that account out of Desktop for good — the session lived only here.
func (s *Store) Remove(name string) error {
	if _, err := s.Get(name); err != nil {
		return err
	}
	return os.RemoveAll(s.profileDir(name))
}
