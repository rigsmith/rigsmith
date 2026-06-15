//go:build darwin

package account

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

// keychainService is the generic-password service Claude Code stores its OAuth
// blob under on macOS (verified on Claude Code 2.x). The whole JSON document
// — claudeAiOauth + organizationUuid — is the keychain "password".
const keychainService = "Claude Code-credentials"

// ReadLive returns the live credential blob from the macOS Keychain. A missing
// entry yields ErrNoLive.
func ReadLive() ([]byte, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", keychainService, "-w").Output()
	if err != nil {
		// Exit 44 = SecItemNotFound (no entry); treat as "not logged in". Any
		// other exit code is a real failure (e.g. keychain access denied) and
		// must surface rather than masquerade as ErrNoLive.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 {
			return nil, ErrNoLive
		}
		return nil, fmt.Errorf("read keychain: %w", err)
	}
	return bytes.TrimRight(out, "\n"), nil
}

// WriteLive overwrites the live Keychain credential. It preserves the existing
// account ("acct") attribute so the entry stays the one Claude Code reads,
// defaulting to the current OS user when there's no entry yet.
func WriteLive(raw []byte) error {
	acct := liveAccountName()
	// -U updates the entry in place if it already exists.
	cmd := exec.Command("security", "add-generic-password",
		"-U", "-a", acct, "-s", keychainService, "-w", string(raw))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("write keychain: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// liveAccountName reads the "acct" attribute of the existing entry, falling back
// to the current OS username.
func liveAccountName() string {
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService).Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, `"acct"`) {
				if i := strings.Index(line, `="`); i >= 0 {
					return strings.TrimSuffix(line[i+2:], `"`)
				}
			}
		}
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "claude"
}
