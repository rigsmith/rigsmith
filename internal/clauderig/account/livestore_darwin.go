//go:build darwin

package account

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

// keychainService is the generic-password service Claude Code stores its OAuth
// blob under on macOS (verified on Claude Code 2.x). The whole JSON document —
// claudeAiOauth + organizationUuid — is the keychain "password".
const keychainService = "Claude Code-credentials"

// securityBin is pinned to Apple's absolute path, not resolved via PATH: this is
// a credential tool, so an attacker-controlled `security` earlier on PATH must
// not be able to intercept secrets. It's present on every macOS.
const securityBin = "/usr/bin/security"

// errSecItemNotFound is security(1)'s exit code for "no such item".
const errSecItemNotFound = 44

// securityStdinLineLimit is security -i's stdin line buffer (BUFSIZ, 4096),
// minus headroom: a longer line is truncated mid-argument and fails to write.
const securityStdinLineLimit = 4096 - 64

// sessionKeychainService is the per-profile service name Claude Code uses when
// CLAUDE_CONFIG_DIR is set: the base name plus the first 8 hex chars of SHA-256
// of the config dir path, hashed exactly as passed in the env var (clauderig
// always passes the clean absolute ConfigDir). Verified live 2026-08-18 against
// two clauderig session profiles.
func sessionKeychainService(configDir string) string {
	sum := sha256.Sum256([]byte(configDir))
	return keychainService + "-" + hex.EncodeToString(sum[:4])
}

// readKeychain reads one generic-password service. found=false (nil error)
// means no such item. The secret returns on stdout (a pipe), not argv.
func readKeychain(service string) (raw []byte, found bool, err error) {
	out, err := exec.Command(securityBin, "find-generic-password",
		"-a", accountName(), "-w", "-s", service).Output()
	if err == nil {
		return bytes.TrimRight(out, "\n"), true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == errSecItemNotFound {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("read keychain: %w", err)
}

// writeKeychain creates or updates one generic-password service. The secret is
// passed as hex via `-X` (no escaping needed) through `security -i` stdin, so
// it never appears in process argv. Only a payload large enough to overflow
// security -i's stdin line buffer falls back to argv — and even then as hex,
// not plaintext.
func writeKeychain(service string, raw []byte) error {
	hexVal := hex.EncodeToString(raw)
	acct := accountName()
	line := fmt.Sprintf("add-generic-password -U -a %s -s %s -X %s\n",
		quoteForSecurity(acct), quoteForSecurity(service), hexVal)

	var cmd *exec.Cmd
	if len(line) <= securityStdinLineLimit {
		cmd = exec.Command(securityBin, "-i")
		cmd.Stdin = strings.NewReader(line)
	} else {
		cmd = exec.Command(securityBin, "add-generic-password", "-U",
			"-a", acct, "-s", service, "-X", hexVal)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write keychain: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// ReadLive returns the live credential. On macOS the Keychain takes precedence
// over any ~/.claude/.credentials.json — matching Claude Code itself — so we read
// the Keychain first and fall back to a file only when there's no entry.
func ReadLive() ([]byte, error) {
	raw, found, err := readKeychain(keychainService)
	if err != nil {
		return nil, err
	}
	if found {
		return raw, nil
	}
	if raw, found, ferr := readLiveFile(); ferr != nil {
		return nil, ferr
	} else if found {
		return raw, nil
	}
	return nil, ErrNoLive
}

// WriteLive overwrites the live Keychain credential — the machine-wide login the
// whole Mac reads. It must use the Keychain, not a file: Claude Code prefers the
// Keychain over ~/.claude/.credentials.json for the default profile.
func WriteLive(raw []byte) error {
	return writeKeychain(keychainService, raw)
}

// platformSessionKeychainRead reads a session profile's per-profile entry —
// where Claude Code keeps the profile's tokens after migrating the seeded
// .credentials.json (which it leaves behind as a token-less stub).
func platformSessionKeychainRead(configDir string) ([]byte, bool, error) {
	return readKeychain(sessionKeychainService(configDir))
}

// platformSessionKeychainWrite updates a session profile's entry only when one
// already exists: a fresh profile is seeded via the file (Claude Code migrates
// it itself). A read failure propagates — reporting it as a no-op would let the
// caller clear the stale marker while the entry still holds the old tokens.
func platformSessionKeychainWrite(configDir string, raw []byte) (bool, error) {
	svc := sessionKeychainService(configDir)
	_, found, err := readKeychain(svc)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return true, writeKeychain(svc, raw)
}

// quoteForSecurity wraps a value for a `security -i` stdin line, which is
// re-parsed shell-style: double-quote it and escape embedded `\` and `"` (the
// service name "Claude Code-credentials" contains a space).
func quoteForSecurity(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(v) + `"`
}

// accountName is the Keychain item's account attribute — the OS username, which
// is what Claude Code uses on macOS.
func accountName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "user"
}
