package desktop

import (
	"bytes"
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

// linkPointsAt reports whether path is a link (or junction) naming target.
//
// Checked on the RAW link destination first, before any resolution: a link
// whose target has been deleted or moved is still OUR link, and still the thing
// standing between the profile and a usable session directory. Requiring
// EvalSymlinks to succeed meant `unshare` silently skipped a dangling link and
// reported success, leaving the profile pointing at nothing.
func linkPointsAt(path, target string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	if raw, rerr := os.Readlink(path); rerr == nil {
		if !filepath.IsAbs(raw) {
			raw = filepath.Join(filepath.Dir(path), raw)
		}
		if filepath.Clean(raw) == filepath.Clean(target) {
			return true
		}
	}
	// Fall back to resolution for the cases a raw comparison cannot settle —
	// a Windows junction, or a target reached by a different but equivalent
	// spelling (a symlinked parent).
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	wanted, err := filepath.EvalSymlinks(target)
	if err != nil {
		wanted = filepath.Clean(target)
	}
	return sameDir(resolved, wanted)
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
	Skipped  int // identical copies already present there, safely dropped
	// Conflicts are files that existed in BOTH trees with DIFFERENT contents.
	// The shared copy is kept and the profile's version is preserved under
	// ConflictDir rather than discarded — see mergeTree.
	Conflicts   int
	ConflictDir string
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
			// Conflicts are preserved beside the profile, NOT inside data/,
			// so Claude Desktop never sees them.
			conflictDir := filepath.Join(p.Dir(), "conflicts", d)
			migrated, skipped, conflicts, unsupported, merr := mergeTree(link, target, conflictDir)
			if merr != nil {
				return out, fmt.Errorf("migrate %s into the shared tree: %w", d, merr)
			}
			// Refuse while the source is still intact: these entries cannot be
			// copied, and the next step deletes the directory holding them.
			if len(unsupported) > 0 {
				// Not "nothing was changed": the walk copies as it goes, so some
				// files may already be in the shared tree. What matters — and
				// what is true — is that the profile's own directory is intact
				// and a retry cannot duplicate anything, since migration never
				// overwrites.
				return out, fmt.Errorf(
					"%s holds %d entry/entries that cannot be migrated (first: %s).\n"+
						"The profile's own copy is untouched and nothing was linked; anything already "+
						"copied into the shared tree is additive, so a retry is safe. Move or remove "+
						"them, then share again",
					d, len(unsupported), unsupported[0])
			}
			res.Migrated, res.Skipped, res.Conflicts = migrated, skipped, conflicts
			if conflicts > 0 {
				res.ConflictDir = conflictDir
			}
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

// mergeTree copies src into dst without ever overwriting, and without ever
// discarding a file that differs.
//
// A collision has two very different meanings, and conflating them loses data:
//
//   - Identical contents: the same session recorded in both trees. The source
//     copy is redundant and can simply be dropped.
//   - DIFFERENT contents: two genuinely different files claiming the same path.
//     Keeping the shared copy is the conservative choice for the tree the app
//     will read, but the profile's version is still the only copy of itself, so
//     it is preserved under conflictDir instead of deleted.
//
// conflictDir sits outside the profile's data directory, so preserved files are
// never seen by Claude Desktop — a stray file inside `claude-code-sessions`
// would be the app's problem to interpret.
//
// Never overwriting is what makes this safe to run against a tree the default
// Desktop profile may also have written; never discarding is what makes the
// "nothing is destroyed" promise true rather than nearly true.
func mergeTree(src, dst, conflictDir string) (migrated, skipped, conflicts int, unsupported []string, err error) {
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
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		// Anything that is neither a regular file nor a symlink — a socket,
		// device or fifo — cannot be copied. Recording it lets Share refuse
		// before deleting the source, the only way to keep the promise for
		// something that cannot be moved.
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			unsupported = append(unsupported, path)
			return nil
		}

		// Attempt first, compare second. Checking whether the destination
		// exists and THEN writing leaves a race in which another writer lands
		// between the two — and a create that loses that race says only that
		// something is there, never that it is the same thing.
		created, cerr := placeEntry(path, filepath.Join(dst, rel), info)
		if cerr != nil {
			return cerr
		}
		if created {
			migrated++
			return nil
		}
		same, eerr := entriesEquivalent(path, filepath.Join(dst, rel))
		if eerr != nil {
			return eerr
		}
		if same {
			skipped++
			return nil
		}
		// Different: the shared copy stands (it is what the app reads), and
		// ours is preserved rather than dropped.
		if perr := preserveConflict(path, filepath.Join(conflictDir, rel), info); perr != nil {
			return perr
		}
		conflicts++
		return nil
	})
	return migrated, skipped, conflicts, unsupported, err
}

// placeEntry writes src to dst without overwriting, whichever kind it is.
func placeEntry(src, dst string, info os.FileInfo) (created bool, err error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return copyLink(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

// entriesEquivalent reports whether two paths hold the same thing — the same
// bytes, or the same link target.
//
// Mismatched kinds are simply "not equivalent", never an error: a source
// symlink colliding with a regular file is a genuine conflict to preserve, and
// reading it as a failure would abort the migration instead.
func entriesEquivalent(a, b string) (bool, error) {
	fa, err := os.Lstat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Lstat(b)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	aLink := fa.Mode()&os.ModeSymlink != 0
	bLink := fb.Mode()&os.ModeSymlink != 0
	if aLink != bLink {
		return false, nil
	}
	if aLink {
		return sameLink(a, b)
	}
	if !fa.Mode().IsRegular() || !fb.Mode().IsRegular() {
		return false, nil
	}
	return sameContents(a, b)
}

// preserveConflict copies src somewhere under the conflict directory, and
// GUARANTEES it lands: if the natural path is taken by something that is not
// equivalent, a numbered sibling is used.
//
// Share deletes the source once migration reports success, so "preserved" has
// to mean preserved. Writing to an occupied path and reporting success — which
// is what ignoring the created flag amounted to — would destroy the profile's
// only copy while claiming to have saved it.
func preserveConflict(src, want string, info os.FileInfo) error {
	for i := 0; i < 1000; i++ {
		candidate := want
		if i > 0 {
			candidate = fmt.Sprintf("%s.%d", want, i)
		}
		created, err := placeEntry(src, candidate, info)
		if err != nil {
			return err
		}
		if created {
			return nil
		}
		// Occupied: an identical backup from an earlier run is as good as ours.
		same, eerr := entriesEquivalent(src, candidate)
		if eerr != nil {
			return eerr
		}
		if same {
			return nil
		}
	}
	return fmt.Errorf("could not preserve %s: too many conflicting copies alongside %s", src, want)
}

// sameContents reports whether two files hold identical bytes. Size is checked
// first because it settles almost every case without reading anything.
func sameContents(a, b string) (bool, error) {
	fa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if fa.Size() != fb.Size() {
		return false, nil
	}
	ba, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ba, bb), nil
}

// copyFile copies src to dst without ever overwriting. created reports whether
// this call made the file, so a caller can tell a real migration from a file
// that was already there — including the O_EXCL race, where another writer won
// and ours was NOT the copy that landed.
func copyFile(src, dst string, mode os.FileMode) (created bool, err error) {
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	// O_EXCL: the "never overwrite" promise is enforced by the filesystem, not
	// just by the check above, so a concurrent writer cannot slip in between.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	if _, cerr := io.Copy(out, in); cerr != nil {
		out.Close()
		_ = os.Remove(dst)
		return false, cerr
	}
	// A delayed write error surfaces at Close. Leaving the partial file behind
	// would make it a collision on the next run — and mergeTree would then treat
	// the truncated copy as the canonical one.
	if cerr := out.Close(); cerr != nil {
		_ = os.Remove(dst)
		return false, cerr
	}
	return true, nil
}

// copyLink recreates a symlink at dst pointing wherever src points. created is
// false when something is already there.
//
// Session trees are meant to hold plain files, but the Cowork tree carries
// sandbox working directories that can contain anything — and Share deletes the
// source once migration reports success, so a link that is merely "skipped"
// would be destroyed rather than moved.
func copyLink(src, dst string) (created bool, err error) {
	target, err := os.Readlink(src)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	if err := os.Symlink(target, dst); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// sameLink reports whether two symlinks name the same target.
func sameLink(a, b string) (bool, error) {
	ta, err := os.Readlink(a)
	if err != nil {
		return false, err
	}
	tb, err := os.Readlink(b)
	if err != nil {
		return false, err
	}
	return ta == tb, nil
}

// DescribeShared renders the shared directory list for help and messages.
func DescribeShared(dirs []string) string { return strings.Join(dirs, ", ") }
