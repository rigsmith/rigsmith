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
	return atomicWriteFile(path, []byte(out), mode)
}

// atomicWriteFile replaces path in a single step: write a sibling temp file,
// flush it, then rename over the destination.
//
// os.WriteFile truncates first, so a failure partway through would leave
// ~/.claude.json truncated — and that file holds far more than the identity
// block (project state, history, per-org caches; ~75 KB in practice). A partial
// write is therefore real data loss, and it would also make the caller's
// "credential rolled back, nothing changed" rollback message a lie: the
// credential would be restored while the profile stayed corrupt. With a rename,
// the destination is either the old file or the new one, never a fragment.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	// Write THROUGH a symlink, never over it. A rename onto the link path would
	// replace the link with a regular file, quietly detaching ~/.claude.json from
	// wherever the user actually keeps it (a dotfiles repo, a synced folder) —
	// their edits and ours would diverge from that moment on, with nothing to
	// show for it. Resolving first means the temp file lands in the real target's
	// own directory, which is also what keeps the rename atomic: a rename across
	// filesystems fails outright. Credit: claude-swap #201 hit this first.
	path = resolveLinkTarget(path)
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	// No-op once the rename succeeds; cleans up every failure path before it.
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	// Durability matters here: a crash between rename and flush could otherwise
	// leave a correctly-named file with unwritten contents.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// CreateTemp makes 0600; carry over the destination's real mode.
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// oauthMeta is the identity/display slice of an oauthAccount block.
// AccountUUID and ProfileFetchedAt are read only for diagnostics (see
// diagnose.go): the account uuid distinguishes two logins that share an org, and
// the fetch stamp dates the last time Claude Code rewrote this block — which is
// how a desync gets pinned to a moment in time.
type oauthMeta struct {
	EmailAddress     string `json:"emailAddress"`
	OrganizationUUID string `json:"organizationUuid"`
	AccountUUID      string `json:"accountUuid"`
	SeatTier         string `json:"seatTier"`
	OrganizationName string `json:"organizationName"`
	ProfileFetchedAt int64  `json:"profileFetchedAt"`
}

func parseOAuthMeta(raw []byte) oauthMeta {
	var m oauthMeta
	_ = json.Unmarshal(raw, &m)
	return m
}

// maxLinkHops bounds the walk so a symlink cycle cannot spin forever.
const maxLinkHops = 32

// resolveLinkTarget follows a symlink chain to the path it ultimately names,
// WITHOUT requiring that path to exist.
//
// filepath.EvalSymlinks fails outright on a dangling link — one whose target has
// not been created yet — and treating that failure as "not a symlink" is exactly
// the case that must not fall through: the rename would then replace the link
// with a regular file, detaching it from the location the user actually keeps it
// in. That is the detachment this whole function exists to prevent, so the
// dangling case has to be handled, not skipped.
//
// Relative targets resolve against the directory holding the link, as the OS
// does.
func resolveLinkTarget(path string) string {
	for i := 0; i < maxLinkHops; i++ {
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return path // not a link (or gone): this is the file to write
		}
		target, rerr := os.Readlink(path)
		if rerr != nil {
			return path
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = filepath.Clean(target)
	}
	return path // cycle: write where we ended up rather than loop
}

// GlobalConfigPath is ~/.claude.json — where Claude Code keeps the oauthAccount
// block. Exported for callers that must act on the file itself (the pre-restore
// backup), not just its contents.
func GlobalConfigPath() (string, error) { return globalConfigPath() }

// LiveIdentity reports which account ~/.claude.json currently names: the account
// uuid, its organization uuid, and the email address.
//
// Identity ONLY. Never the credential, the plan, the rate-limit tier, or
// anything else in the block — this is the slice that is safe to write into the
// synced repo, where it becomes the sole record of which account a machine's
// sessions were captured under. All three come back empty when Claude Code has
// never logged in here (file or key absent), which is not an error.
func LiveIdentity() (accountUUID, orgUUID, email string, err error) {
	p, err := globalConfigPath()
	if err != nil {
		return "", "", "", err
	}
	return identityFromFile(p)
}

func identityFromFile(path string) (accountUUID, orgUUID, email string, err error) {
	raw, err := readOAuthAccountFrom(path)
	if err != nil || len(raw) == 0 {
		return "", "", "", err
	}
	m := parseOAuthMeta(raw)
	return m.AccountUUID, m.OrganizationUUID, m.EmailAddress, nil
}
