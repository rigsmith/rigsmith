package desktop

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Sharing session history between profiles.
//
// Each profile is a separate Claude Desktop installation as far as the app is
// concerned, so a Claude Code session started from the `work` window does not
// appear in `personal` — and clauderig's sync only watches the DEFAULT Desktop
// root, so profile history is not backed up at all.
//
// Sharing points every profile's session directory at one shared tree. Two facts
// make that safe, and both were established by looking at real data rather than
// assumed:
//
//   - The trees are ALREADY partitioned by account uuid: `claude-code-sessions`
//     contains one directory per account (`456fc32e-…`, `03d1c0c9-…`). Two
//     profiles signed into different accounts therefore write to different
//     subdirectories and cannot collide. Two profiles signed into the SAME
//     account would share one subdirectory — which is the intent, not a bug.
//   - The shared tree is the default Desktop root's own directory, which
//     clauderig's sync allowlist already covers (`inc("claude-code-sessions")`).
//     Sharing therefore brings profile history into the existing backup with no
//     new sync rules — the reason to pick that location over a neutral one.
//
// Nothing is deleted: migration copies into the shared tree and never overwrites
// an existing file, and `unshare` leaves the shared history in place.

// linkDirFn is indirected so a test can force the one failure that matters —
// the link refusing to be created — without arranging a privilege error.
var linkDirFn = linkDir

// errTestLinkFailed is used only by tests, through linkDirFn.
var errTestLinkFailed = errors.New("link creation failed")

// SharedDirs are the session trees `share` links. `claude-code-sessions` is the
// Claude Code history shown in Desktop's Code tab — small, and the thing worth
// sharing. The Cowork tree is opt-in (see CoworkDir): it is two orders of
// magnitude larger and holds sandbox working directories, so moving it is a
// heavier operation than most callers want by default.
var SharedDirs = []string{"claude-code-sessions"}

// CoworkDir is the Cowork/agent-mode session tree, shared only on request.
const CoworkDir = "local-agent-mode-sessions"

// ShareState describes one profile's sharing status.
type ShareState struct {
	// Linked names the directories currently pointing at the shared tree.
	Linked []string
	// Own names the directories that are still the profile's own.
	Own []string
}

// Shared reports whether every directory in want is linked to the shared tree.
func (s ShareState) Shared(want []string) bool {
	if len(s.Linked) == 0 {
		return false
	}
	for _, w := range want {
		found := false
		for _, l := range s.Linked {
			if l == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ShareStatus inspects a profile's session directories against sharedRoot.
func ShareStatus(p Profile, sharedRoot string, dirs []string) ShareState {
	var st ShareState
	for _, d := range dirs {
		link := filepath.Join(p.DataDir(), d)
		target := filepath.Join(sharedRoot, d)
		if linkPointsAt(link, target) {
			st.Linked = append(st.Linked, d)
			continue
		}
		if _, err := os.Lstat(link); err == nil {
			st.Own = append(st.Own, d)
		}
	}
	return st
}

// linkPointsAt reports whether path is a link (or junction) resolving to target.
func linkPointsAt(path, target string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	// A Windows junction is a reparse point: Go reports it as a symlink on
	// modern versions, and EvalSymlinks resolves it either way.
	if fi.Mode()&os.ModeSymlink == 0 && !fi.IsDir() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	wanted, err := filepath.EvalSymlinks(target)
	if err != nil {
		wanted = filepath.Clean(target)
	}
	return sameDir(resolved, wanted) && fi.Mode()&os.ModeSymlink != 0
}

func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// ErrProfileOpen means the profile's window is running, so its session
// directories must not be repointed underneath it.
var ErrProfileOpen = errors.New("the profile's Claude Desktop window is open")

// ShareResult reports what a share did, for a caller to narrate.
type ShareResult struct {
	Dir      string
	Migrated int // files copied into the shared tree
	Skipped  int // files already present there, left untouched
}

// Share points a profile's session directories at the shared tree, migrating
// what was already there.
//
// The profile's window MUST be closed: Electron holds these paths open and will
// keep writing through a directory handle it opened before the swap, so a live
// relink silently loses whatever it writes next. The caller checks that (it owns
// the App); this function only refuses to act on a directory it cannot replace
// safely.
func Share(p Profile, sharedRoot string, dirs []string) ([]ShareResult, error) {
	var out []ShareResult
	for _, d := range dirs {
		link := filepath.Join(p.DataDir(), d)
		target := filepath.Join(sharedRoot, d)
		if err := os.MkdirAll(target, 0o700); err != nil {
			return out, fmt.Errorf("prepare shared %s: %w", d, err)
		}
		if linkPointsAt(link, target) {
			out = append(out, ShareResult{Dir: d}) // already shared; nothing to do
			continue
		}
		res := ShareResult{Dir: d}

		// Move the existing directory ASIDE rather than deleting it, and put it
		// back if the link cannot be created. Deleting first means a failure
		// here — a Windows junction refused for want of privilege is the
		// realistic case — leaves the profile with NO session directory at all,
		// and Claude Desktop would then quietly build a fresh empty tree in its
		// place. The stash is removed only once the profile points at the shared
		// tree.
		stash := link + ".clauderig-stash"
		_ = os.RemoveAll(stash) // a stash from an interrupted earlier run
		stashed := false

		fi, lerr := os.Lstat(link)
		switch {
		case lerr == nil && fi.Mode()&os.ModeSymlink != 0:
			// A link pointing somewhere else: nothing of the profile's own to
			// migrate, but keep it until the replacement exists.
			if rerr := os.Rename(link, stash); rerr != nil {
				return out, rerr
			}
			stashed = true
		case lerr == nil && fi.IsDir():
			// A real directory: its contents are this profile's own history and
			// must reach the shared tree before the directory goes anywhere.
			migrated, skipped, merr := mergeTree(link, target)
			if merr != nil {
				return out, fmt.Errorf("migrate %s into the shared tree: %w", d, merr)
			}
			res.Migrated, res.Skipped = migrated, skipped
			if rerr := os.Rename(link, stash); rerr != nil {
				return out, rerr
			}
			stashed = true
		case lerr == nil:
			return out, fmt.Errorf("%s exists and is not a directory", link)
		}

		if err := linkDirFn(target, link); err != nil {
			if stashed {
				// Restore, so the failure leaves the profile exactly as it was.
				// The migrated copies stay in the shared tree; they are additions
				// there, and mergeTree never overwrites, so a later retry is a
				// no-op rather than a duplication.
				if rerr := os.Rename(stash, link); rerr != nil {
					return out, fmt.Errorf(
						"link %s to the shared tree: %w; AND the original could not be restored: %v.\n"+
							"The profile's history is at %s — move it back to %s by hand",
						d, err, rerr, stash, link)
				}
			}
			return out, fmt.Errorf("link %s to the shared tree: %w", d, err)
		}
		if stashed {
			if rerr := os.RemoveAll(stash); rerr != nil {
				return out, fmt.Errorf("linked %s, but the old copy at %s could not be removed: %w",
					d, stash, rerr)
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// Unshare replaces the links with the profile's own empty directories.
//
// Deliberately non-destructive: the shared history stays where it is. Working
// out which sessions "belong" to this profile and copying them back would be
// guesswork, and getting it wrong would either duplicate or delete history.
// Stopping the sharing is reversible; deleting is not.
func Unshare(p Profile, sharedRoot string, dirs []string) error {
	for _, d := range dirs {
		link := filepath.Join(p.DataDir(), d)
		target := filepath.Join(sharedRoot, d)
		if !linkPointsAt(link, target) {
			continue // not ours to undo
		}
		// Build the replacement BEFORE removing the link, then swap, so a
		// failure never leaves the profile without a session directory.
		fresh := link + ".clauderig-new"
		_ = os.RemoveAll(fresh)
		if err := os.MkdirAll(fresh, 0o700); err != nil {
			return err
		}
		if err := os.Remove(link); err != nil {
			_ = os.RemoveAll(fresh)
			return err
		}
		if err := os.Rename(fresh, link); err != nil {
			// Put the link back rather than leave nothing behind.
			if lerr := linkDirFn(target, link); lerr != nil {
				return fmt.Errorf("unshare %s: %w; AND the shared link could not be restored: %v", d, err, lerr)
			}
			return fmt.Errorf("unshare %s: %w", d, err)
		}
	}
	return nil
}

// mergeTree copies src into dst without ever overwriting: a file already present
// in the shared tree is left exactly as it is.
//
// Never overwriting is what makes this safe to run against a tree the default
// Desktop profile may also have written. The account-uuid partitioning means
// collisions are rare to begin with — they require the same session id recorded
// under the same account in two profiles — and when one happens, keeping the
// shared copy is the conservative choice.
func mergeTree(src, dst string) (migrated, skipped int, err error) {
	err = filepath.Walk(src, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		targetPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(targetPath, 0o700)
		}
		if !info.Mode().IsRegular() {
			return nil // skip links/sockets: session trees hold plain files
		}
		if _, serr := os.Lstat(targetPath); serr == nil {
			skipped++
			return nil
		}
		if cerr := copyFile(path, targetPath, info.Mode()); cerr != nil {
			return cerr
		}
		migrated++
		return nil
	})
	return migrated, skipped, err
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	// O_EXCL: the "never overwrite" promise is enforced by the filesystem, not
	// just by the check above, so a concurrent writer cannot slip in between.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	if _, cerr := io.Copy(out, in); cerr != nil {
		out.Close()
		_ = os.Remove(dst)
		return cerr
	}
	return out.Close()
}

// DescribeShared renders the shared directory list for help and messages.
func DescribeShared(dirs []string) string { return strings.Join(dirs, ", ") }
