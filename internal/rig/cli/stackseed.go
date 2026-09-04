package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

// newStackSeedCmd exports what is the stackspace's own — everything at the
// root outside every prefix: the manifest with its cursors, build overlays,
// packaging, whatever was deliberately kept out of the members because it
// would otherwise leave in a pull request — as a small repository that
// `rig stack init` can rebuild the whole stackspace from.
//
// Without it the only thing that reproduces a stackspace is the fused repo
// itself, ~100 MB carrying every upstream's history, and a derived artifact
// under version control drifts. The seed is a few kilobytes and derives
// nothing: the manifest already records which commit each prefix held.
func newStackSeedCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "seed <dir>",
		Short: "Export the root files as a small repo that `rig stack init` rebuilds the stackspace from",
		Long: "Writes everything at the stackspace root that is not a member — the\n" +
			"manifest with its cursors, build overlays, packaging, the files kept out of\n" +
			"every prefix on purpose — into <dir> as a fresh git repository with one\n" +
			"commit. Push that wherever you like; it is a few kilobytes and holds no\n" +
			"upstream history.\n\n" +
			"On another machine, clone it and run `rig stack init`: each member's\n" +
			"directory is missing but its cursor is recorded, so init rebuilds it at\n" +
			"that commit — or, for a repo with `trackBranch` set or one last proposed\n" +
			"to a branch still on its fork, from that branch, so work that has left as\n" +
			"a proposal and not yet merged comes back too.\n\n" +
			"A member holding commits that have not left the stackspace is refused:\n" +
			"a rebuild holds its cursor or its proposed branch, and those commits would\n" +
			"be in neither. `rig stack propose` them first, or --force to seed anyway.\n\n" +
			"  rig stack seed ../my-stack-seed\n" +
			"  git -C ../my-stack-seed remote add origin <url> && git -C ../my-stack-seed push -u origin main",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			m, _, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			dest := args[0]
			written, err := stackSeed(ctx, repo, m, dest, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ seeded %s with %d root file(s) and one commit\n", dest, written)
			fmt.Fprintf(out, "  push it anywhere; on another machine, clone it and run `rig stack init` to rebuild the members\n")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "seed even where a member holds commits that have not left the stackspace")
	return cmd
}

// newStackSeedMenuCmd is seed for the menu, where there is no argument to
// type: it asks where, then runs the real verb.
func newStackSeedMenuCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "seed",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dest := "../stack-seed"
			if err := huh.NewInput().
				Title("Write the seed repository where?").
				Description("a new or empty directory; it gets one commit holding the root files").
				Value(&dest).Run(); err != nil {
				return err
			}
			if strings.TrimSpace(dest) == "" {
				return nil
			}
			sub := newStackSeedCmd()
			sub.SetContext(cmd.Context())
			sub.SetOut(cmd.OutOrStdout())
			sub.SetErr(cmd.ErrOrStderr())
			return sub.RunE(sub, []string{dest})
		},
	}
}

// stackSeed materialises HEAD's root entries that are not member prefixes into
// dest as a fresh repository with one commit, and reports how many entries
// were written. dest must not exist or must be empty. force seeds past a
// member whose commits have not left the stackspace.
func stackSeed(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, dest string, force bool) (int, error) {
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return 0, fmt.Errorf("%s exists and is not empty — a seed needs a new or empty directory", dest)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	// The manifest in hand came from the working tree; the root files come
	// from HEAD. An uncommitted edit to either — a member removed from the
	// manifest but not yet committed, a root file changed — would make the
	// seed something no revision ever was, so both have to agree first.
	if dirty, derr := repo.Dirty(ctx); derr != nil {
		return 0, derr
	} else if dirty {
		return 0, fmt.Errorf("stackspace has uncommitted changes — commit them first, so the seed is a revision that exists")
	}
	// A seed carries no member, so a rebuilt one holds its cursor or the
	// branch it was last proposed to — and commits that reached neither are
	// in no seed. The same refusal rm makes, for the same reason: that work
	// exists nowhere else, and a seed that looks complete and is not would
	// be found out only on the other machine.
	if !force {
		for _, name := range m.names() {
			if !stackPrefixPresent(ctx, repo, name) {
				continue
			}
			u := stackUnsentWork(ctx, repo, name, nil)
			switch {
			case u.Commits:
				return 0, fmt.Errorf("%s has commits that have not left the stackspace — a rebuilt member holds its cursor or proposed branch, not these; `rig stack propose %s <branch>` first, or --force", name, name)
			case !u.Known:
				return 0, fmt.Errorf("cannot tell whether %s holds unsent commits (no import commit in this history) — check with `git log -- %s/`, then --force", name, name)
			}
		}
	}
	names, err := repo.TopLevelNames(ctx, "HEAD")
	if err != nil {
		return 0, fmt.Errorf("reading the stackspace's root: %w", err)
	}
	members := map[string]bool{}
	for _, n := range m.names() {
		members[n] = true
	}
	var keep []string
	for _, n := range names {
		if !members[n] {
			keep = append(keep, n)
		}
	}
	if len(keep) == 0 {
		return 0, fmt.Errorf("nothing at the root outside the members — a seed needs at least the manifest committed")
	}
	data, err := repo.ArchiveTar(ctx, "HEAD", keep)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return 0, err
	}
	if err := untar(bytes.NewReader(data), dest); err != nil {
		return 0, fmt.Errorf("writing %s: %w", dest, err)
	}
	seed, err := gitrepo.Init(ctx, dest)
	if err != nil {
		return 0, err
	}
	if _, err := seed.Commit(ctx, "stack: seed of "+filepath.Base(repo.Dir)); err != nil {
		return 0, err
	}
	return len(keep), nil
}

// untar extracts a tar stream under dest, refusing any entry that would land
// outside it. Regular files, directories and symlinks are written — the three
// things a git tree holds — and a symlink whose target would leave dest is
// refused rather than written pointing anywhere. Anything else in the stream
// is an error, so the seed is never silently incomplete.
//
// Containment is judged through the links already written, not on the path
// alone: once `a -> .` exists, `a/../x` is x's parent's sibling, not x, and a
// check that collapsed the `..` lexically would pass an entry the filesystem
// then puts outside dest.
func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	root := filepath.Clean(dest)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(hdr.Name))
		if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q escapes %s", hdr.Name, dest)
		}
		if !insideRoot(root, filepath.Dir(target)) {
			return fmt.Errorf("archive entry %q escapes %s through a link", hdr.Name, dest)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(hdr.Mode)&0o777|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Resolved against the link's own directory, through whatever
			// links are already there, and refused if that leaves dest: a link
			// out of the tree is not the stackspace's own. Not cleaned first —
			// the `..` has to be walked, not collapsed.
			resolved := filepath.Dir(target) + string(filepath.Separator) + filepath.FromSlash(hdr.Linkname)
			if filepath.IsAbs(hdr.Linkname) || !insideRoot(root, resolved) {
				return fmt.Errorf("symlink %q points outside the seed (%s)", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(filepath.FromSlash(hdr.Linkname), target); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			// pax metadata git archive writes ahead of the entries; not content.
		default:
			return fmt.Errorf("archive entry %q has a type the seed cannot carry", hdr.Name)
		}
	}
}

// insideRoot reports whether path, once every symlink already on disk along
// it is followed, is root or under it. Components that do not exist yet are
// taken as written, after the existing prefix has been resolved — so a `..`
// past a link is judged against where the link goes, not where it sits.
func insideRoot(root, path string) bool {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolved := realPath(path)
	return resolved == real || strings.HasPrefix(resolved, real+string(filepath.Separator))
}

// realPath walks path one component at a time, following each symlink that
// already exists on disk and taking what does not exist yet as written. A
// `..` is applied to the resolved prefix, never to the spelling — which is
// the whole difference from filepath.Clean.
func realPath(path string) string {
	vol := filepath.VolumeName(path)
	cur := vol + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(path, vol), string(filepath.Separator)) {
		switch part {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, part)
		if r, err := filepath.EvalSymlinks(next); err == nil {
			next = r
		}
		cur = next
	}
	return cur
}

// stackImportFromFork decides whether a member is imported from a branch of
// its fork rather than from upstream, and resolves that branch: an explicit
// trackBranch always; otherwise, when rebuilding a prefix the manifest
// remembers, the branch it was last proposed to, if it still exists — that is
// where work that has left as a proposal and not yet merged lives. nil means
// upstream, as ever.
func stackImportFromFork(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, name string, rebuilding bool) (*stackForkRef, error) {
	return stackForkRefFor(m, name, rebuilding, func(branch string) (string, bool, error) {
		// A ref that is absent and a fork that could not be asked are
		// different answers: the first means the work has moved on, the
		// second must not quietly become "rebuild without it".
		ref := "refs/heads/" + branch
		found, err := repo.LsRemoteRefs(ctx, stackRemoteURL(m.Repos[name].Fork), ref)
		if err != nil {
			return "", false, err
		}
		sha, ok := found[ref]
		return sha, ok, nil
	})
}

// stackForkRefFor is stackImportFromFork with the remote lookup passed in, so
// the decision is testable without a forge. An explicit trackBranch that does
// not exist is an error: importing from upstream instead would quietly hand
// back a stackspace without the work the branch was named to carry. So is a
// fork that cannot be reached at all, for the same reason.
func stackForkRefFor(m *stackManifest, name string, rebuilding bool, resolve func(branch string) (sha string, found bool, err error)) (*stackForkRef, error) {
	r := m.Repos[name]
	if r == nil {
		return nil, nil
	}
	if r.TrackBranch != "" {
		sha, ok, err := resolve(r.TrackBranch)
		if err != nil {
			return nil, fmt.Errorf("%s: looking up trackBranch %q on %s: %w", name, r.TrackBranch, r.Fork, err)
		}
		if !ok {
			return nil, fmt.Errorf("%s: trackBranch %q is not on %s — push it there, or drop the key to import from %s", name, r.TrackBranch, r.Fork, r.Upstream)
		}
		return &stackForkRef{Branch: r.TrackBranch, Commit: sha}, nil
	}
	if !rebuilding || m.LastPropose[name] == "" {
		return nil, nil
	}
	// The proposed branch may have merged and been deleted since; upstream
	// then already carries it, and the cursor is the right place to rebuild.
	//
	// The record is the branch as pushed, looked up as it is. A manifest from
	// before it was recorded that way holds the bare name, so the current
	// prefix is tried after — which finds the branch unless the prefix has
	// changed since, the case recording the full name exists to cover.
	last := m.LastPropose[name]
	candidates := []string{last}
	if full := m.sendBranch(name, last); full != last {
		candidates = append(candidates, full)
	}
	for _, branch := range candidates {
		sha, ok, err := resolve(branch)
		if err != nil {
			return nil, fmt.Errorf("%s: checking whether %s still carries %s: %w\nthat branch may hold work the rebuilt %s/ would otherwise lack", name, r.Fork, branch, err, name)
		}
		if ok {
			return &stackForkRef{Branch: branch, Commit: sha}, nil
		}
	}
	return nil, nil
}
