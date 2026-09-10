package service

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/agentrig/queue"
)

// CheckHookTranscript checks the hook's native CLI path against the runtime's
// configured source spelling. It reads no transcript bytes and does not require
// the file to exist; capture still proves availability and rejects ambiguity.
// Filesystem links and source roots must remain stable, as for other producers.
func (r *QueueRuntime) CheckHookTranscript(path, sessionID string) error {
	fail := fmt.Errorf("hook transcript must name the matching session under the configured CLI projects directory: %w", queue.ErrBinding)
	encoded, _ := json.Marshal(path)
	if !filepath.IsAbs(path) || len(encoded) > 4098 {
		return fail
	}
	roots, err := captureRootsWithPathResolver(r.request, r.profiles, filepath.Abs)
	if err != nil {
		return err
	}
	cli, ok := roots["cli"]
	if !ok {
		return fail
	}
	rel, err := filepath.Rel(cli, filepath.Clean(path))
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if err != nil || len(parts) != 3 || parts[0] != "projects" || parts[1] == "" || parts[2] != sessionID+".jsonl" {
		return fail
	}
	return nil
}
