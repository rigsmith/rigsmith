package commands

import (
	"context"
	"os"
	"path/filepath"
)

// settingsPath is the user-scope settings file (~/.claude/settings.json), used by
// the status dashboard and init's sync-hook install. Scoped install/uninstall
// goes through the scope commands (see scope.go).
func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// repoRootBestEffort returns the current repo's top-level, or "" when not in one.
func repoRootBestEffort(ctx context.Context) string {
	if _, root, err := openRepo(ctx); err == nil {
		return root
	}
	return ""
}
