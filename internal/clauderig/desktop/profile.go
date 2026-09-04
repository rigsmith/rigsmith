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

// DefaultStore is the profile store this machine uses.
//
// Under ~/.clauderig and deliberately NOT under ~/.claude or Desktop's own
// application-support directory: a profile holds a live logged-in session, and
// keeping the store outside every sync root is what stops a root's own walk from
// sweeping the whole tree, credentials included.
func DefaultStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(home, ".clauderig", "desktop")), nil
}

func (s *Store) profileDir(name string) string { return filepath.Join(s.Root, name) }
func (s *Store) metaPath(name string) string {
	return filepath.Join(s.profileDir(name), "profile.json")
}

// CandidateDataDirs returns the data directory of every entry under the store
// root, keyed by directory name — INCLUDING entries List skips because their
// profile.json is missing, corrupt, or unreadable.
//
// List is deliberately forgiving: an unreadable directory should not fail a
// listing meant for display. But a safety scan cannot inherit that. A profile
// whose metadata will not parse can still have a Claude Desktop instance
// running against its data dir, competing for a scheme-routed deep link
// exactly like any other — so anything deciding whether it is safe to send one
// has to see it, name or no name.
func (s *Store) CandidateDataDirs() (map[string]string, error) {
	entries, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		// Shared with List, deliberately. This check lived here alone until a
		// review found List missing symlinked profiles in exactly the way this
		// comment already described — one copy fixed, the other not, which is
		// what having two copies buys you.
		if !isDirFollowingLinks(s.Root, e) {
			continue
		}
		out[e.Name()] = filepath.Join(s.profileDir(e.Name()), "data")
	}
	return out, nil
}

// isDirFollowingLinks reports whether an entry is, or points at, a directory.
// Only a symlink pays for a stat; every other entry is answered from the type
// bits ReadDir already carries. A broken link answers false.
func isDirFollowingLinks(root string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	fi, err := os.Stat(filepath.Join(root, e.Name()))
	return err == nil && fi.IsDir()
}

// List returns every saved profile, ordered by name. A directory under the
// store that does not load as a profile is passed over; ListAll says which.
func (s *Store) List() ([]Profile, error) {
	out, _, err := s.ListAll()
	return out, err
}

// ListAll is List that also names the directories it passed over — ones
// that look like profiles but whose profile.json is missing or unreadable —
// so a verb acting on "every profile" can say what it did not act on rather
// than report success over a profile it never saw.
func (s *Store) ListAll() (profiles []Profile, skipped []string, err error) {
	entries, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []Profile
	for _, e := range entries {
		// os.ReadDir reports the entry itself, not its target, so a profile
		// directory that is a SYMLINK answers IsDir false. Skipping those made
		// List disagree with Get/Resolve, which follow the link and work fine:
		// `desktop list` hid such a profile, `shortcut --all` and `rm` passed
		// over it, and anything asking "are there any profiles" was told no
		// while `desktop open <name>` drove it happily.
		if !isDirFollowingLinks(s.Root, e) {
			continue
		}
		p, lerr := s.Get(e.Name())
		if lerr != nil {
			// Not a profile directory, or an unreadable one: passed over
			// rather than failing the listing, and named for callers that
			// need to know.
			skipped = append(skipped, e.Name())
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.Strings(skipped)
	return out, skipped, nil
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
	// The DIRECTORY is the identity, not the metadata. If profile.json somehow
	// names something else — hand-edited, copied from another profile — then
	// DataDir would point at the directory asked for while Touch and Remove
	// operated on the name inside it, which is a write to (or a deletion of) a
	// different profile. Overwrite rather than trust.
	p.dir = s.profileDir(name)
	p.Name = name
	return p, nil
}

// Resolve finds a profile by name, or by the email label when no profile has
// that name.
//
// A real load error is returned rather than swallowed: a profile whose
// profile.json is corrupt or unreadable must not silently become "no such
// profile", or a later `open` would create the impression it had been deleted.
// Only a genuine miss falls through to the email lookup.
//
// Email labels are not unique — clauderig never verifies them, and two profiles
// may legitimately carry the same one — so an ambiguous email is refused rather
// than resolved to whichever sorted first. `quit` and `rm` act on the result.
func (s *Store) Resolve(ref string) (Profile, error) {
	p, err := s.Get(ref)
	switch {
	case err == nil:
		return p, nil
	case !errors.Is(err, ErrNotFound):
		return Profile{}, err
	}
	all, lerr := s.List()
	if lerr != nil {
		return Profile{}, lerr
	}
	var matches []Profile
	for _, cand := range all {
		if cand.Email != "" && strings.EqualFold(cand.Email, ref) {
			matches = append(matches, cand)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Profile{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return Profile{}, fmt.Errorf("%q labels %d profiles (%s) — name the one you mean",
			ref, len(matches), strings.Join(names, ", "))
	}
}

// Create makes a new, empty profile. The data directory is created but left
// empty — Claude Desktop populates it on first launch, and the user logs in
// there once.
func (s *Store) Create(name, email, accountID string) (Profile, error) {
	if err := ValidName(name); err != nil {
		return Profile{}, err
	}
	// Only a genuine miss means the name is free. A permission or parse error
	// here would otherwise fall through and overwrite an existing profile's
	// metadata — and `add` would open an already-logged-in data directory as
	// though it were new.
	switch _, err := s.Get(name); {
	case err == nil:
		return Profile{}, fmt.Errorf("%w: %s", ErrExists, name)
	case !errors.Is(err, ErrNotFound):
		return Profile{}, fmt.Errorf("cannot tell whether %q already exists: %w", name, err)
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

// save writes profile.json atomically. Touch calls it after every launch and
// discards the error, so a torn write — an interrupt, a full disk — would leave
// truncated JSON that List silently skips, and the profile would appear to have
// vanished. A rename leaves either the old file or the new one, never a
// fragment.
func (s *Store) save(p Profile) error {
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')

	final := s.metaPath(p.Name)
	f, err := os.CreateTemp(p.dir, "profile.json.tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op once the rename succeeds
	if _, werr := f.Write(body); werr != nil {
		_ = f.Close()
		return werr
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		return serr
	}
	if cerr := f.Close(); cerr != nil {
		return cerr
	}
	if cerr := os.Chmod(tmp, 0o600); cerr != nil {
		return cerr
	}
	return os.Rename(tmp, final)
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
