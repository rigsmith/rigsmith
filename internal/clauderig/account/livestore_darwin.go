//go:build darwin

package account

import (
	"bytes"
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

// ReadLive returns the live credential. On macOS the Keychain takes precedence
// over any ~/.claude/.credentials.json — matching Claude Code itself — so we read
// the Keychain first and fall back to a file only when there's no entry. The
// read returns the secret on stdout (a pipe), not argv.
func ReadLive() ([]byte, error) {
	out, err := exec.Command(securityBin, "find-generic-password",
		"-a", accountName(), "-w", "-s", keychainService).Output()
	if err == nil {
		return bytes.TrimRight(out, "\n"), nil
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != errSecItemNotFound {
		return nil, fmt.Errorf("read keychain: %w", err)
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
//
// The secret is passed as hex via `-X` (no escaping needed) through `security -i`
// stdin, so it never appears in process argv. Only a payload large enough to
// overflow security -i's stdin line buffer falls back to argv — and even then as
// hex, not plaintext.
func WriteLive(raw []byte) error {
	hexVal := hex.EncodeToString(raw)
	acct, svc := accountName(), keychainService
	line := fmt.Sprintf("add-generic-password -U -a %s -s %s -X %s\n",
		quoteForSecurity(acct), quoteForSecurity(svc), hexVal)

	var cmd *exec.Cmd
	if len(line) <= securityStdinLineLimit {
		cmd = exec.Command(securityBin, "-i")
		cmd.Stdin = strings.NewReader(line)
	} else {
		cmd = exec.Command(securityBin, "add-generic-password", "-U",
			"-a", acct, "-s", svc, "-X", hexVal)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write keychain: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
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
