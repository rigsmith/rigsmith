package commitartifact

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// mergeWorkDir keeps disposable Git repositories out of deeply nested intent
// stores on Windows. core.longpaths permits long file paths, but does not lift
// the process working-directory limit when starting Git or its children.
// Only scratch data lives here: bundles and sealed intents still go to their
// caller-owned destinations. Callers remove this directory on ordinary return;
// process-crash cleanup remains separate from durable checkpoint retention.
func (r gitRepo) mergeWorkDir(ctx context.Context, parent, pattern string) (string, error) {
	if runtime.GOOS == "windows" {
		temp, err := filepath.Abs(os.TempDir())
		if err != nil {
			return "", err
		}
		// Relocating scratch must not bypass the planner's canonical checkout,
		// linked-worktree and Git-directory protections via a configured TEMP.
		dest, err := r.planDestination(ctx, filepath.Join(temp, "rig-merge-work"))
		if err != nil {
			return "", err
		}
		parent = filepath.Dir(dest)
		pattern = "rig-merge-" + strings.TrimPrefix(pattern, ".")
	}
	return os.MkdirTemp(parent, pattern)
}
