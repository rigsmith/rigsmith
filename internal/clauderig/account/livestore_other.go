//go:build !darwin

package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// liveCredPath is where Claude Code keeps its file-based credential off macOS:
// $CLAUDE_CONFIG_DIR/.credentials.json when set, else ~/.claude/.credentials.json.
func liveCredPath() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".credentials.json"), nil
	}
	home, err := ClaudeHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".credentials.json"), nil
}

// ReadLive returns the live file-based credential blob. A missing file yields
// ErrNoLive.
func ReadLive() ([]byte, error) {
	p, err := liveCredPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoLive
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	return raw, nil
}

// WriteLive overwrites the live file-based credential (0600), creating the
// parent dir if needed.
func WriteLive(raw []byte) error {
	p, err := liveCredPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}
