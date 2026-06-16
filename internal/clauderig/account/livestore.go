package account

import (
	"fmt"
	"os"
	"path/filepath"
)

// liveCredFile is the file-based credential path: ~/.claude/.credentials.json.
// Off macOS this IS the machine-wide live store; on macOS it's only a fallback,
// because Claude Code reads the Keychain in preference to it (see
// livestore_darwin.go). It deliberately ignores CLAUDE_CONFIG_DIR — that's for
// isolated sessions, while `switch` is machine-wide (session isolation is
// handled by PrepareSession writing into its own dir).
func liveCredFile() (string, error) {
	home, err := ClaudeHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".credentials.json"), nil
}

// readLiveFile reads the file-based live credential. found=false with a nil
// error means no file is present (the caller decides whether to fall back).
func readLiveFile() (raw []byte, found bool, err error) {
	p, err := liveCredFile()
	if err != nil {
		return nil, false, err
	}
	raw, err = os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", p, err)
	}
	return raw, true, nil
}

// writeLiveFile writes the file-based live credential (0600), creating ~/.claude
// if needed. It's the live store off macOS.
func writeLiveFile(raw []byte) error {
	p, err := liveCredFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}
