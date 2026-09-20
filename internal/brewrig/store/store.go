// Package store is the private git repo holding one file per machine. It is the
// whole transport: clone, pull, write this machine's file, commit, push.
//
// Sharding by machine is what keeps this simple. A machine only ever writes
// machines/<its own name>.json, so two machines syncing at the same moment
// touch disjoint paths and the ordinary case has no conflict to resolve.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	m, err := inventory.Unmarshal(b)
	if err != nil {
		return nil, false, err
	}
	return m, true, nil
}

// Write saves this machine's inventory into the clone. It does not commit.
//
// The destination comes out of a repository other machines write to, and
// os.WriteFile follows symlinks. Another clone could commit
// machines/<this-machine>.json as a link to somewhere outside the staging repo
// and have the next sync write through it, so every component is checked first
// and the file is opened without following a final link.
func (s *Store) Write(m *inventory.Machine) error {
	b, err := inventory.Marshal(m)
	if err != nil {
		return err
	}
	rel := inventory.FileName(m.Name)
	p := filepath.Join(s.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := refuseSymlinks(s.dir, rel); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscallNoFollow, 0o644)
	if err != nil {
		return fmt.Errorf("writing %s: %w", rel, err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// refuseSymlinks rejects a path any of whose components inside the clone is a
// symbolic link.
func refuseSymlinks(root, rel string) error {
	cur := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return fmt.Errorf("refusing to write through %q: it climbs out of the staging repo", rel)
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil // nothing left to follow
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through %q: %s is a symbolic link, and the "+
				"repository it came from is written by other machines", rel, part)
		}
	}
	return nil
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
	return os.WriteFile(p, []byte(body), 0o644)
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
