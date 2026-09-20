// Package store is the private git repo holding one file per machine. It is the
// whole transport: clone, pull, write this machine's file, commit, push.
//
// Sharding by machine is what keeps this simple. A machine only ever writes
// machines/<its own name>.json, so two machines syncing at the same moment
// touch disjoint paths and the ordinary case has no conflict to resolve.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/ghrepo"
	"github.com/rigsmith/rigsmith/internal/brewrig/inventory"
)

// Store is the local clone.
type Store struct {
	repo   *gitrepo.Repo
	dir    string
	remote string
	branch string

	// base records, per machine, a digest of the published file as this
	// process last saw it. Write refuses to replace a destination that no
	// longer matches — see ErrStaleBase.
	mu   sync.Mutex
	base map[string][32]byte
}

// ErrStaleBase is returned by Write when the published file changed after this
// process read it.
//
// The rename itself is atomic, which protects the file from being seen
// half-written and does nothing about a lost update: two brewrig processes on
// one machine can each snapshot, and the one that renames LAST wins even if it
// captured the older state. The published inventory would self-correct on the
// next sync, but a `retired` or `acknowledged` entry written by the loser would
// be gone for good — and a "keep" decision quietly forgotten is exactly what
// this PR spent its time fixing elsewhere.
//
// So the overwrite is refused rather than performed. Retrying automatically
// would be better still; serialising the whole snapshot-write-publish
// transaction better again. Neither is here: this converts a silent loss into a
// legible error, and the rest is honest follow-up work.
var ErrStaleBase = errors.New("the published inventory changed while this run was preparing its own; run brewrig again")

// digestOf is the recorded form of a file's contents, with a distinct value for
// "the file was not there", so a file appearing under us is caught too.
func digestOf(b []byte, exists bool) [32]byte {
	if !exists {
		return [32]byte{}
	}
	d := sha256.Sum256(b)
	if d == ([32]byte{}) { // unreachable in practice; keeps "absent" unambiguous
		d[0] = 1
	}
	return d
}

func (s *Store) recordBase(machine string, b []byte, exists bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base == nil {
		s.base = map[string][32]byte{}
	}
	s.base[machine] = digestOf(b, exists)
}

func (s *Store) expectedBase(machine string) ([32]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.base[machine]
	return d, ok
}

// Dir is the clone's path on disk.
func (s *Store) Dir() string { return s.dir }

// Open prepares the local clone at dir for the given remote, cloning it if it
// is not there yet and repointing it if the configured remote has changed.
func Open(ctx context.Context, dir, remote, branch string) (*Store, error) {
	if branch == "" {
		branch = "main"
	}
	s := &Store{dir: dir, remote: remote, branch: branch}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		r, err := gitrepo.Open(ctx, dir)
		if err != nil {
			return nil, err
		}
		s.repo = r
		if remote != "" {
			if err := r.EnsureRemote(ctx, "origin", remote); err != nil {
				return nil, err
			}
		}
		return s, nil
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if remote == "" {
		return nil, fmt.Errorf("no remote configured — run `brewrig init`")
	}
	r, err := gitrepo.Clone(ctx, remote, dir)
	if err != nil {
		return nil, fmt.Errorf("cloning %s: %w", ghrepo.SafeRemote(remote), err)
	}
	s.repo = r
	return s, nil
}

// Pull brings the clone up to date. A brand-new repo has no commits to fetch,
// which is not an error — it is the first machine to sync.
func (s *Store) Pull(ctx context.Context) error {
	if s.repo.Unborn(ctx) {
		if err := s.repo.Fetch(ctx, "origin", s.branch); err != nil {
			// A brand-new shared repo genuinely has no branch to fetch, and
			// that is not an error — this is the first machine to sync. But it
			// is the ONLY case worth swallowing: returning nil for every
			// failure hides an unreachable host, a bad credential or a
			// cancelled context, and sync then commits locally and reports
			// success while nothing has been shared.
			if !s.remoteLacksBranch(ctx) {
				return fmt.Errorf("fetching %s: %w", s.branch, err)
			}
			return nil
		}
	}
	if err := s.repo.Pull(ctx, "origin", s.branch); err != nil {
		return fmt.Errorf("pulling %s: %w", s.branch, err)
	}
	return nil
}

// remoteLacksBranch reports whether the remote answers but simply has no such
// branch yet, which is what a freshly created empty repo looks like.
func (s *Store) remoteLacksBranch(ctx context.Context) bool {
	refs, err := s.repo.LsRemoteRefs(ctx, "origin", nil, "refs/heads/"+s.branch)
	if err != nil {
		return false // could not ask, so do not assume the benign case
	}
	return len(refs) == 0
}

// Machines reads every published inventory, newest-named first by machine name.
//
// A file that will not parse is reported rather than skipped: silently ignoring
// one machine's inventory would make its packages look uninstalled everywhere
// and, worse, make its retirements vanish.
func (s *Store) Machines(ctx context.Context) ([]*inventory.Machine, error) {
	dir := filepath.Join(s.dir, "machines")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*inventory.Machine
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		m, err := inventory.Unmarshal(b)
		if err != nil {
			return nil, fmt.Errorf("machines/%s: %w", e.Name(), err)
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Load reads one machine's published inventory.
func (s *Store) Load(name string) (*inventory.Machine, bool, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, inventory.FileName(name)))
	if os.IsNotExist(err) {
		s.recordBase(name, nil, false)
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	s.recordBase(name, b, true)
	m, err := inventory.Unmarshal(b)
	if err != nil {
		return nil, false, err
	}
	return m, true, nil
}

// Write saves this machine's inventory into the clone. It does not commit.
//
// The destination comes out of a repository other machines write to, so it is
// not trusted input. Two distinct things can go wrong and both are guarded:
//
//   - A component replaced by a symlink pointing OUT of the clone, which would
//     have the next sync write through it to somewhere else entirely. os.Root
//     confines every lookup below the clone directory and refuses a link that
//     leaves it. It resolves descriptor-relative, so unlike a check followed by
//     an open it cannot be raced by a concurrent pull swapping a directory
//     after the check and before the write.
//   - A machine file that IS a symlink, pointing somewhere inside the clone —
//     another machine's inventory, say. os.Root would follow that quite
//     happily, since it never leaves the root, so it is refused explicitly.
//
// The second check is a check-then-use and could in principle be raced. The
// residual is bounded and recoverable: the worst case is clobbering another
// machine's file inside a git clone, which shows up in the diff and can be
// restored. The unbounded case — writing outside the clone — is the one
// os.Root closes properly.
func (s *Store) Write(m *inventory.Machine) error {
	b, err := inventory.Marshal(m)
	if err != nil {
		return err
	}
	rel := path.Join("machines", m.Name+".json")

	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer root.Close()

	if err := root.MkdirAll("machines", 0o755); err != nil {
		return err
	}
	if fi, lerr := root.Lstat(rel); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write %s: it is a symbolic link, and the repository "+
			"it came from is written by other machines", rel)
	}

	// Written to a temporary file and renamed into place. A truncating write
	// can leave partial JSON behind if it fails, and that file is read back by
	// this machine's own next sync (Snapshot loads it to carry retirements
	// forward) — so a half-written inventory would not just be wrong, it would
	// block the run that was going to repair it. Rename is also why a symlink
	// at the destination cannot be written through: it replaces the link
	// rather than following it. The explicit check above stays anyway, so the
	// refusal is a clear error rather than a silently vanished link.
	// A unique temp path per writer, not a deterministic one. Two brewrig
	// processes can run at once on the same machine — a scheduled sync and a
	// manual one — and with a shared name the second writer's O_EXCL create
	// would delete the first's file out from under it, so one of them renames
	// or cleans up something that is not theirs.
	//
	// The randomness has to be real. A first attempt used the pid and
	// time.Now().UnixNano(), which is unique across processes and NOT within
	// one: concurrent goroutines share the pid and can read the same
	// nanosecond, and the concurrency test caught it with "file exists".
	//
	// Nothing sweeps temp files left by a crash, on purpose: a sweep is the
	// deterministic-name problem again. They are harmless instead — Machines
	// reads only *.json, and .gitignore (written by EnsureReadme) keeps them
	// out of the commit.
	f, tmp, err := createTemp(root)
	if err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	// Compare-and-replace, under a lock so the two are one step. Without it
	// the check is a check-then-act: two Stores can read the same digest, both
	// pass, and the second silently overwrites the first while reporting
	// success — which is the lost update the digest was added to prevent.
	unlock, stillHeld, err := lockMachines(root)
	if err != nil {
		_ = root.Remove(tmp)
		return err
	}
	defer unlock()

	if want, ok := s.expectedBase(m.Name); ok {
		cur, rerr := root.ReadFile(rel)
		switch {
		case rerr != nil && !errors.Is(rerr, fs.ErrNotExist):
			_ = root.Remove(tmp)
			return rerr
		case digestOf(cur, rerr == nil) != want:
			_ = root.Remove(tmp)
			return fmt.Errorf("not replacing %s: %w", rel, ErrStaleBase)
		}
	}

	// A seam for the concurrency test, and only that. The window between the
	// check and the rename is a few microseconds wide, so a test cannot hit it
	// by racing goroutines — it passes with or without the lock, which makes
	// it evidence of nothing. Widening the window here lets the test show that
	// the lock is what closes it, since inside the lock a slow critical
	// section is merely slow.
	afterStaleCheck()

	// Re-check ownership immediately before the rename. If the lock was broken
	// as stale while this run was inside it, another writer is in there now and
	// this write must not land on top of theirs.
	if !stillHeld() {
		_ = root.Remove(tmp)
		return fmt.Errorf("not replacing %s: %w", rel, ErrStaleBase)
	}

	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("replacing %s: %w", rel, err)
	}
	s.recordBase(m.Name, b, true)
	return nil
}

// afterStaleCheck runs between the stale-base check and the rename. It is a
// no-op outside tests.
var afterStaleCheck = func() {}

// lockDir is the mutual-exclusion point for compare-and-replace inside a clone,
// and lockOwner names the token file that says who holds it.
const (
	lockDir   = "machines/.lock"
	lockOwner = "machines/.lock/owner"
)

// lockStale is how old a lock must be before it is broken. The critical section
// is a handful of syscalls, so a lock this old means the holder died rather
// than that it is slow — and leaving a dead holder's lock forever would wedge
// every future sync on the machine.
const lockStale = 2 * time.Minute

// lockMachines takes the clone's write lock, waiting briefly for another
// process to finish. It returns a release that only releases what it took, and
// a predicate for re-checking that the lock is still ours.
//
// mkdir, because it is the one create-or-fail primitive that is atomic on every
// filesystem worth caring about. The token inside is what makes breaking a
// stale lock safe: without it, taking over by age alone means the original
// holder — slow rather than dead — carries on, and its deferred release deletes
// the SUCCESSOR's live lock, handing the directory to a third writer while two
// are already inside. Identity turns that release into a no-op instead.
//
// This serialises writers sharing a clone, which is the case that exists: two
// brewrig processes on one machine. Two machines do not share a clone, and
// their races are settled by git on push.
func lockMachines(root *os.Root) (release func(), held func() bool, err error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, nil, err
	}
	mine := fmt.Sprintf("%x", token)

	owner := func() string {
		b, err := root.ReadFile(lockOwner)
		if err != nil {
			return ""
		}
		return string(b)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		switch mkErr := root.Mkdir(lockDir, 0o755); {
		case mkErr == nil:
			f, cerr := root.OpenFile(lockOwner, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
			if cerr == nil {
				_, cerr = f.WriteString(mine)
				if closeErr := f.Close(); cerr == nil {
					cerr = closeErr
				}
			}
			if cerr != nil {
				_ = root.RemoveAll(lockDir)
				return nil, nil, cerr
			}
			return func() {
				// Only ever remove a lock still marked as ours. If it was
				// broken as stale while we ran, it is someone else's now.
				if owner() == mine {
					_ = root.RemoveAll(lockDir)
				}
			}, func() bool { return owner() == mine }, nil
		case !errors.Is(mkErr, fs.ErrExist):
			return nil, nil, mkErr
		}

		if fi, serr := root.Stat(lockDir); serr == nil && time.Since(fi.ModTime()) > lockStale {
			_ = root.RemoveAll(lockDir)
			continue
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("another brewrig is writing to %s and did not finish within 5s", lockDir)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Publish commits and pushes whatever Write left in the tree. It reports
// whether anything was actually committed, so a no-op sync can say "already up
// to date" instead of claiming a push it never made.
func (s *Store) Publish(ctx context.Context, msg string) (bool, error) {
	changed, err := s.repo.Commit(ctx, msg)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := s.ensureBranch(ctx); err != nil {
		return false, err
	}
	if err := s.repo.Push(ctx, "origin", s.branch); err != nil {
		// Sharding by machine keeps the CONTENT disjoint, which is not the
		// same as keeping the push safe: two machines committing from the same
		// base still race, and the loser is rejected as non-fast-forward. Left
		// there the clone stays diverged and every later ff-only Pull fails
		// too, so the machine silently stops syncing. Reconcile and retry once.
		if merr := s.reconcile(ctx); merr != nil {
			return true, fmt.Errorf("pushing to %s: %w (and reconciling the remote failed: %v)",
				ghrepo.SafeRemote(s.remote), err, merr)
		}
		if err := s.repo.Push(ctx, "origin", s.branch); err != nil {
			return true, fmt.Errorf("pushing to %s after reconciling: %w", ghrepo.SafeRemote(s.remote), err)
		}
	}
	return true, nil
}

// reconcile merges whatever the remote gained since this clone last looked.
// Machine files are disjoint, so this is ordinarily a trivial merge; a genuine
// conflict is surfaced rather than resolved, because it means two machines
// wrote the same file and brewrig cannot know which is right.
func (s *Store) reconcile(ctx context.Context) error {
	conflicted, err := s.repo.FetchMerge(ctx, "origin", s.branch)
	if err != nil {
		return err
	}
	if conflicted {
		return fmt.Errorf("the shared repo and this clone both changed the same file; "+
			"resolve it in %s and run sync again", s.dir)
	}
	return nil
}

// ensureBranch puts the clone on the configured branch. A repo cloned while
// empty starts on whatever git's init.defaultBranch says, which is not
// necessarily the branch we publish to.
func (s *Store) ensureBranch(ctx context.Context) error {
	cur, err := s.repo.CurrentBranch(ctx)
	if err != nil || cur == s.branch {
		return err
	}
	return s.repo.Checkout(ctx, s.branch, true)
}

// createTemp opens a uniquely named temp file inside the clone's machines/
// directory, retrying on the vanishingly unlikely collision rather than
// assuming one cannot happen.
//
// The name is opaque and does not embed the machine's. A machine name is only
// bounded by what config.Load accepts, so `.<machine>.json.tmp<16 hex>` could
// exceed the filesystem's component limit for a name whose own
// `machines/<machine>.json` fits — the write would fail for a file that is
// otherwise perfectly legal.
func createTemp(root *os.Root) (*os.File, string, error) {
	for attempt := 0; attempt < 10; attempt++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, "", err
		}
		name := path.Join("machines", fmt.Sprintf(".tmp%x", b))
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("could not create a temp file in machines/ after 10 attempts")
}

// gitignoreBody keeps in-flight temp files out of the shared repo. A crashed
// write leaves one behind and the next Publish stages everything.
const gitignoreBody = "machines/.tmp*\nmachines/.lock/\n"

// EnsureReadme writes the orientation file on first publish. A private repo
// full of JSON with no explanation is a puzzle when it resurfaces in two years.
func (s *Store) EnsureReadme() error {
	p := filepath.Join(s.dir, "README.md")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	const body = `# brew sync

Homebrew inventories, one file per machine, published by [brewrig](https://rigsmith.dev).

` + "```" + `
machines/<machine>.json   what that machine has deliberately installed
` + "```" + `

Each machine writes only its own file. The set of them is the shared truth —
there is no merged Brewfile, because a single file cannot tell "not installed
there yet" apart from "deliberately removed there".

Run ` + "`brewrig status`" + ` on a machine to see how it differs from the others,
` + "`brewrig apply`" + ` to install what it is missing, and ` + "`brewrig update`" + `
to move it onto current versions.

Edited by hand? That is fine — it is only JSON — but note that a package removed
from a file by hand reads as "never installed", not as "uninstall it elsewhere".
Uninstalling with ` + "`brew uninstall`" + ` and running ` + "`brewrig sync`" + ` is
what records the intent to remove.
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		return err
	}
	return s.ensureGitignore()
}

// ensureGitignore makes sure the temp-file rule is present, adding it to an
// existing file rather than replacing one someone else wrote.
func (s *Store) ensureGitignore() error {
	p := filepath.Join(s.dir, ".gitignore")
	cur, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Whole lines, not a substring: a comment mentioning the pattern, or an
	// unrelated rule containing it, would otherwise count as the rule being
	// present and leave temp files stageable.
	have := map[string]bool{}
	for _, line := range strings.Split(string(cur), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing string
	for _, want := range strings.Split(strings.TrimSpace(gitignoreBody), "\n") {
		if !have[want] {
			missing += want + "\n"
		}
	}
	if missing == "" {
		return nil
	}
	out := string(cur)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return os.WriteFile(p, []byte(out+missing), 0o644)
}

// LastSync reports the most recent commit in the clone.
func (s *Store) LastSync(ctx context.Context) (subject, when string, ok bool) {
	_, subject, when, err := s.repo.LastCommit(ctx)
	if err != nil || subject == "" {
		return "", "", false
	}
	return subject, when, true
}

// Reachable reports whether the remote answers.
func Reachable(ctx context.Context, remote string) bool { return gitrepo.Reachable(ctx, remote) }
