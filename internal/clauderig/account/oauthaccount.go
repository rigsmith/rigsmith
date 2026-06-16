package account

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/jsonc"
)

// The account's identity (email, org) and PLAN (seatTier / rateLimitTier) live
// in ~/.claude.json under "oauthAccount" — state separate from the OAuth
// credential. Claude Code's UI reads the plan from here, so a `switch` that only
// swaps the credential leaves the plan display stale (shows the previous
// account's tier until a login refresh). A correct swap must move this block too.

func globalConfigPath() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude.json"), nil
}

// ReadOAuthAccount returns the raw `oauthAccount` object from ~/.claude.json, or
// (nil, nil) when the file or key is absent.
func ReadOAuthAccount() ([]byte, error) {
	p, err := globalConfigPath()
	if err != nil {
		return nil, err
	}
	return readOAuthAccountFrom(p)
}

func readOAuthAccountFrom(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	raw, ok := m["oauthAccount"]
	if !ok {
		return nil, nil
	}
	return raw, nil
}

// WriteOAuthAccount surgically replaces `oauthAccount` in ~/.claude.json,
// preserving the rest of the (large) file and its mode. No-op for an empty value
// or a missing file (nothing to merge into).
func WriteOAuthAccount(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	p, err := globalConfigPath()
	if err != nil {
		return err
	}
	return writeOAuthAccountTo(p, raw)
}

func writeOAuthAccountTo(path string, raw []byte) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // no global config to merge into; the credential swap still stands
	}
	if err != nil {
		return err
	}
	out, ok := jsonc.Set(string(b), []string{"oauthAccount"}, string(raw))
	if !ok {
		return errors.New("could not set oauthAccount in ~/.claude.json")
	}
	mode := os.FileMode(0o644)
	if fi, serr := os.Stat(path); serr == nil {
		mode = fi.Mode().Perm()
	}
	return os.WriteFile(path, []byte(out), mode)
}

// oauthMeta is the identity/display slice of an oauthAccount block.
type oauthMeta struct {
	EmailAddress     string `json:"emailAddress"`
	OrganizationUUID string `json:"organizationUuid"`
	SeatTier         string `json:"seatTier"`
	OrganizationName string `json:"organizationName"`
}

func parseOAuthMeta(raw []byte) oauthMeta {
	var m oauthMeta
	_ = json.Unmarshal(raw, &m)
	return m
}
