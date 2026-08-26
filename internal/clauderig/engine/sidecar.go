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

// cliSynced reports whether the CLI root was resolved and walked on this run —
// the precondition for judging any sidecar, since its transcripts are the
// evidence the prune deletes on the absence of.
func cliSynced(rep *Report) bool {
	for _, r := range rep.Roots {
		if r.ID == "cli" {
			return !r.Skipped
		}
	}
	return false
}

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
		if werr != nil {
			// A vanished entry is ordinary churn and simply isn't in the index. But
			// an unreadable DIRECTORY would silently yield a partial index, and this
			// pass deletes based on absence — so every sidecar pointing into that
			// subtree would be destroyed on incomplete evidence. Propagate anything
			// that isn't a plain "gone" so the caller falls back to doing nothing.
			if os.IsNotExist(werr) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
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
// Applied to every staged Desktop tree — the machine-wide install and each
// profile — because they hold the same sidecars pointing into the same shared
// CLI transcripts, and one clock has to govern all of them.
func pruneOrphanedSidecars(stagingDir string, trees []string) (int, error) {
	ids, ok := stagedTranscriptIDs(filepath.Join(stagingDir, "cli", "projects"))
	if !ok {
		return 0, nil
	}
	total := 0
	for _, tree := range trees {
		n, err := pruneSidecarTree(filepath.Join(stagingDir, tree, sidecarTree), ids)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func pruneSidecarTree(root string, ids map[string]bool) (int, error) {
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

// desktopSessionAccounts maps a CLI session id to the accountUuid that owns it,
// read from every staged Desktop sidecar tree. A session claimed by more than
// one account maps to "" — see readSidecarAccounts.
//
// The account is the PATH, not a field in the file:
// <root>/claude-code-sessions/<accountUuid>/<organizationUuid>/local_<id>.json.
// Desktop files each session under the account that opened it, which makes this
// ground truth about ownership — unlike the syncing machine's current login,
// which is only ever an inference about someone else's past.
//
// Every staged tree is read, including roots this run did not walk. That is the
// opposite of pruneOrphanedSidecars' fail-open rule, and deliberately so: that
// pass DELETES on absence, so a partial view is dangerous, while this one only
// ever adds an attribution, so a partial view merely attributes less.
func desktopSessionAccounts(stagingDir string) (accounts map[string]string, conflicted map[string]bool) {
	out := map[string]string{}
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return out, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Both layouts: a machine-wide root holds the tree directly, a profile
		// root nests it under data/ (see desktopTreesIn).
		for _, tree := range []string{
			filepath.Join(stagingDir, e.Name(), sidecarTree),
			filepath.Join(stagingDir, e.Name(), "data", sidecarTree),
		} {
			readSidecarAccounts(tree, out)
		}
	}
	// Ambiguity reported APART from absence. Collapsing them let a session that
	// later became contested keep the attribution recorded before the conflict:
	// callers saw "" as "no sidecar", kept the existing higher-ranked desktop
	// answer, and went on filtering it under an account that is now disputed.
	conflicts := map[string]bool{}
	for id, acct := range out {
		if acct == "" {
			conflicts[id] = true
			delete(out, id)
		}
	}
	return out, conflicts
}

// readSidecarAccounts adds one tree's <accountUuid>/<org>/local_*.json rows to
// out. A sidecar that cannot be read or names no session is skipped: attribution
// is optional, so an unreadable file costs one session's label and nothing else.
func readSidecarAccounts(root string, out map[string]string) {
	if !dirExists(root) {
		return
	}
	accounts, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, acct := range accounts {
		if !acct.IsDir() {
			continue
		}
		orgs, err := os.ReadDir(filepath.Join(root, acct.Name()))
		if err != nil {
			continue
		}
		for _, org := range orgs {
			if !org.IsDir() {
				continue
			}
			dir := filepath.Join(root, acct.Name(), org.Name())
			files, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, f := range files {
				name := f.Name()
				if !strings.HasPrefix(name, "local_") || !strings.HasSuffix(name, ".json") {
					continue
				}
				data, rerr := os.ReadFile(filepath.Join(dir, name))
				if rerr != nil {
					continue
				}
				var ref sidecarRef
				if json.Unmarshal(data, &ref) != nil || ref.CLISessionID == "" {
					continue
				}
				if prev, seen := out[ref.CLISessionID]; seen && prev != acct.Name() {
					// The same session claimed by two accounts. Whichever
					// directory happened to be read last would otherwise decide,
					// and this is supposed to be GROUND TRUTH — an arbitrary
					// answer here is worse than none, because it outranks the
					// inference that would have been used instead and is sticky
					// once stored. Mark it unusable and let the session stay
					// unattributed.
					out[ref.CLISessionID] = ""
					continue
				}
				out[ref.CLISessionID] = acct.Name()
			}
		}
	}
}
