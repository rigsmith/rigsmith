package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Session-profile credential access.
//
// A session profile (CLAUDE_CONFIG_DIR) does not keep its tokens where clauderig
// originally seeds them. On macOS, Claude Code migrates the seeded
// .credentials.json into a per-profile Keychain entry — the service name is
// "Claude Code-credentials-" plus the first 8 hex chars of SHA-256 of the config
// dir path (verified live 2026-08-18, Claude Code 2.x) — and leaves the file
// behind as a token-less stub. Off macOS the file keeps the tokens. Everything
// here goes through the two platform hooks below so the Keychain scheme stays in
// one place and tests can stub it.

// sessionKeychainRead returns the profile's Keychain credential
// (found=false when there is no entry; off macOS always found=false).
// sessionKeychainWrite updates an EXISTING entry only — it never creates one,
// so a fresh profile is seeded via the file and Claude Code migrates it itself.
// (false, nil) means specifically "no entry to update"; a Keychain failure is
// an error, never a silent no-op.
// Package vars so tests can stub the Keychain.
var (
	sessionKeychainRead  = platformSessionKeychainRead
	sessionKeychainWrite = platformSessionKeychainWrite
)

func sessionCredFile(configDir string) string {
	return filepath.Join(configDir, ".credentials.json")
}

// hasTokens reports whether a credential blob actually authenticates — the same
// bar metaFromBlob sets, usable where only a yes/no is needed. A blob Claude
// Code has blanked (expired refresh token, logout) parses fine but fails this.
func hasTokens(raw []byte) bool {
	var b blob
	if json.Unmarshal(raw, &b) != nil {
		return false
	}
	return b.ClaudeAiOauth.AccessToken != "" || b.ClaudeAiOauth.RefreshToken != ""
}

// readSessionCredential returns the profile's current credential. An EXISTING
// Keychain entry is authoritative even when its tokens are blanked — Claude
// Code reads it in preference to the file, so once an entry exists, file tokens
// are at best stale and must not resurrect a login the entry says is dead. The
// file is consulted only when there is no entry at all (fresh profile, or off
// macOS). found=false means the profile has no usable credential; a Keychain
// read failure propagates rather than guessing.
func readSessionCredential(configDir string) (raw []byte, found bool, err error) {
	kc, ok, err := sessionKeychainRead(configDir)
	if err != nil {
		return nil, false, err
	}
	if ok {
		if hasTokens(kc) {
			return kc, true, nil
		}
		return nil, false, nil
	}
	if f, ferr := os.ReadFile(sessionCredFile(configDir)); ferr == nil && hasTokens(f) {
		return f, true, nil
	}
	return nil, false, nil
}

// sessionCredentialUsable reports whether the profile can authenticate as-is.
func sessionCredentialUsable(configDir string) (bool, error) {
	_, found, err := readSessionCredential(configDir)
	return found, err
}

// seedSessionCredential writes a credential into a session profile: the file
// always, and the Keychain entry too when one exists — otherwise Claude Code
// would keep authenticating from the old Keychain tokens and the reseed would
// be a silent no-op.
func seedSessionCredential(configDir string, raw []byte) error {
	if err := os.WriteFile(sessionCredFile(configDir), raw, 0o600); err != nil {
		return err
	}
	_, err := sessionKeychainWrite(configDir, raw)
	return err
}

// CaptureFromSession repairs an account's stored credential from its own
// session profile — the recovery path when the stored copy is a token-less stub
// (e.g. an expired live login was round-tripped over it) but the account's
// session still authenticates. The session keeps rotating its refresh token, so
// the captured snapshot can go stale; capture right before you need it.
func (s *Store) CaptureFromSession(a Account) error {
	raw, found, err := readSessionCredential(s.ConfigDir(a.ID))
	if err != nil {
		return fmt.Errorf("read session credential: %w", err)
	}
	if !found {
		return fmt.Errorf("no usable session credential for %s — run `clauderig account run %s` and log in there first", a.Email, a.ID)
	}
	sub, org, merr := metaFromBlob(raw)
	if merr != nil {
		return fmt.Errorf("parse session credential for %s: %w", a.Email, merr)
	}
	// The session may have been re-logged-in as a DIFFERENT account since it was
	// created; storing its credential under this account's label would be the
	// mislabeled pair `switch` exists to refuse. Verify the org before saving.
	if a.OrganizationUUID != "" && org != "" && org != a.OrganizationUUID {
		return fmt.Errorf(
			"the session profile for %s is logged in as a different account (org %s, expected %s) — "+
				"not capturing. Log in as %s inside that session (`clauderig account run %s`) and retry",
			a.Email, org, a.OrganizationUUID, a.Email, a.ID)
	}
	if sub != "" {
		a.SubscriptionType = sub
	}
	if err := s.save(a, raw); err != nil {
		return err
	}
	// The tokens came FROM this session — reseeding them back is pointless, and
	// harmful if the session rotates them in between.
	_ = os.Remove(s.stalePath(a.ID))
	return nil
}

// CredentialHealthy reports whether an account's STORED credential still has
// tokens — i.e. whether `switch` would accept it.
func (s *Store) CredentialHealthy(id string) bool {
	raw, err := s.Credential(id)
	return err == nil && hasTokens(raw)
}

// Session profile states reported by StoredStatus.Session.
const (
	SessionNone     = "none"      // no session profile on disk
	SessionOK       = "ok"        // profile can authenticate (Keychain entry or file)
	SessionNoTokens = "no-tokens" // profile exists but nothing in it authenticates
	SessionUnknown  = "unknown"   // Keychain unreadable — health can't be determined
)

// StoredStatus is one account's health as `doctor` reports it: whether the
// stored credential would survive a `switch`, and whether its session profile
// can still authenticate.
type StoredStatus struct {
	Account
	Active           bool   `json:"active"`
	CredentialTokens bool   `json:"credentialTokens"`
	Session          string `json:"session"`
	// Desktop reports whether a Claude Desktop session is stored for this
	// account — i.e. whether `switch` can move Desktop too or will leave it
	// signed in as whoever it was.
	Desktop bool `json:"desktop"`
}

// StoredStatuses reports every tracked account's health, sorted like List.
func (s *Store) StoredStatuses() ([]StoredStatus, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	active, _ := s.Active()
	out := make([]StoredStatus, 0, len(all))
	for _, a := range all {
		desk, _ := s.Desktop(a.ID)
		st := StoredStatus{
			Account:          a,
			Active:           a.ID == active,
			CredentialTokens: s.CredentialHealthy(a.ID),
			Session:          SessionNone,
			Desktop:          desk.HasSession(),
		}
		if dir := s.ConfigDir(a.ID); dirExists(dir) {
			switch usable, uerr := sessionCredentialUsable(dir); {
			case uerr != nil:
				st.Session = SessionUnknown
			case usable:
				st.Session = SessionOK
			default:
				st.Session = SessionNoTokens
			}
		}
		out = append(out, st)
	}
	return out, nil
}
