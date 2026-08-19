package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Desktop session sidecars and the transcripts they name.
//
// A Desktop sidecar (`claude-code-sessions/<acct>/<org>/local_<id>.json`, and the
// same shape under `local-agent-mode-sessions`) is metadata — title, cwd, model —
// for a session whose actual content is a CLI transcript at
// `projects/<slug>/<cliSessionId>.jsonl`. The sidecar is what gives a transcript a
// human title in `search`; on its own it describes nothing.
//
// The two trees were pruned on separate clocks: transcripts by mtime, sidecars
// never. So they drifted apart monotonically and staging accumulated titles for
// transcripts that no longer existed.
//
// The obvious fix — age sidecars out on their own mtime, like transcripts — is
// wrong, and measurably so. A sidecar's mtime tracks when Desktop last rewrote its
// metadata, NOT when the session was last used: on a real machine, sidecars 32 and
// 48 days old named transcripts written 0.9 and 2.1 days ago, a gap of up to 46
// days. Pruning on that clock would delete the metadata for actively-used
// sessions.
//
// What the sidecar's lifetime actually depends on is its transcript's. So the rule
// here is referential, not temporal: a staged sidecar survives iff the transcript
// it names is still staged. Transcript retention then drives both trees on one
// clock, and they age out as a unit.

// sidecarTree is the staged Desktop directory this pass governs.
//
// `local-agent-mode-sessions` is deliberately NOT included, even though its
// sidecars have the same shape and also carry a cliSessionId. A Cowork session's
// transcript lives inside its own sandbox directory, which is excluded from sync
// outright — so its cliSessionId can never resolve against the CLI projects tree,
// and judging those sidecars by this rule would prune every one of them, always,
// for a reason that has nothing to do with retention drift.
const sidecarTree = "claude-code-sessions"

// sidecarRef is the only field of a sidecar this pass needs.
type sidecarRef struct {
	CLISessionID string `json:"cliSessionId"`
}

// stagedTranscriptIDs indexes the session ids present under a staged projects dir
// — the basename of every `<sessionId>.jsonl`. ok=false means there is no usable
// index (no projects dir, or it holds no transcripts at all), which must be
// treated as "unknown", never as "nothing survives".
func stagedTranscriptIDs(projectsDir string) (ids map[string]bool, ok bool) {
	ids = map[string]bool{}
	err := filepath.WalkDir(projectsDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil
		}
		if name := d.Name(); strings.HasSuffix(name, ".jsonl") {
			ids[strings.TrimSuffix(name, ".jsonl")] = true
		}
		return nil
	})
	if err != nil || len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

// pruneOrphanedSidecars removes staged Desktop sidecars whose transcript is no
// longer staged, and returns how many it removed.
//
// It is deliberately fail-open. When the transcript index is unavailable — the CLI
// root isn't synced on this machine, or staging holds no transcripts yet — it
// removes nothing: a Desktop-only sync must not read "no transcripts" as "every
// sidecar is an orphan" and delete the lot. For the same reason a sidecar with no
// `cliSessionId` is always kept; there is nothing to resolve, so there is no
// evidence it is orphaned.
//
// Running as a post-pass, after transcript retention, is what keeps this
// consistent. The alternative — deciding per sidecar during the copy loop — would
// have the copy stage and the staging prune disagree about sidecars whose
// transcript is absent for a reason other than age (an oversized transcript the
// size cap dropped, say), so each sync would re-add what the last one removed.
func pruneOrphanedSidecars(stagingDir string) (int, error) {
	ids, ok := stagedTranscriptIDs(filepath.Join(stagingDir, "cli", "projects"))
	if !ok {
		return 0, nil
	}
	root := filepath.Join(stagingDir, "desktop", sidecarTree)
	if !dirExists(root) {
		return 0, nil
	}
	pruned := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "local_") || !strings.HasSuffix(name, ".json") {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		var ref sidecarRef
		if json.Unmarshal(data, &ref) != nil || ref.CLISessionID == "" {
			return nil // unreadable or unresolvable — keep it
		}
		if !ids[ref.CLISessionID] {
			if os.Remove(p) == nil {
				pruned++
			}
		}
		return nil
	})
	if err != nil {
		return pruned, err
	}
	removeEmptyDirs(root)
	return pruned, nil
}
