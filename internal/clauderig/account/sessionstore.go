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
// so a fresh profile is seeded via the file and Claude Code migrates it itself
// (wrote=false when there was no entry to update).
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

// readSessionCredential returns the profile's current credential, preferring
// the Keychain entry (what Claude Code actually reads) over the file stub.
// found=false means the profile has no usable credential anywhere.
func readSessionCredential(configDir string) (raw []byte, found bool) {
	if kc, ok, err := sessionKeychainRead(configDir); err == nil && ok && hasTokens(kc) {
		return kc, true
	}
	if f, err := os.ReadFile(sessionCredFile(configDir)); err == nil && hasTokens(f) {
		return f, true
	}
	return nil, false
}

// sessionCredentialUsable reports whether the profile can authenticate as-is.
func sessionCredentialUsable(configDir string) bool {
	_, found := readSessionCredential(configDir)
	return found
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
	raw, found := readSessionCredential(s.ConfigDir(a.ID))
	if !found {
		return fmt.Errorf("no usable session credential for %s — run `clauderig account run %s` and log in there first", a.Email, a.ID)
	}
	if sub, _, err := metaFromBlob(raw); err == nil && sub != "" {
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
)

// StoredStatus is one account's health as `doctor` reports it: whether the
// stored credential would survive a `switch`, and whether its session profile
// can still authenticate.
type StoredStatus struct {
	Account
	Active           bool   `json:"active"`
	CredentialTokens bool   `json:"credentialTokens"`
	Session          string `json:"session"`
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
		st := StoredStatus{
			Account:          a,
			Active:           a.ID == active,
			CredentialTokens: s.CredentialHealthy(a.ID),
			Session:          SessionNone,
		}
		if dir := s.ConfigDir(a.ID); dirExists(dir) {
			if sessionCredentialUsable(dir) {
				st.Session = SessionOK
			} else {
				st.Session = SessionNoTokens
			}
		}
		out = append(out, st)
	}
	return out, nil
}
