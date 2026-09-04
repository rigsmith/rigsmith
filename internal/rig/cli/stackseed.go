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
			written, err := stackSeed(ctx, repo, m, dest)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ seeded %s with %d root file(s) and one commit\n", dest, written)
			fmt.Fprintf(out, "  push it anywhere; on another machine, clone it and run `rig stack init` to rebuild the members\n")
			return nil
		},
	}
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
// were written. dest must not exist or must be empty.
func stackSeed(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, dest string) (int, error) {
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		return 0, fmt.Errorf("%s exists and is not empty — a seed needs a new or empty directory", dest)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
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
// outside it. Only regular files and directories are written; git archive
// emits nothing else for a tree of ordinary files.
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
		}
	}
}

// stackImportFromFork decides whether a member is imported from a branch of
// its fork rather than from upstream, and resolves that branch: an explicit
// trackBranch always; otherwise, when rebuilding a prefix the manifest
// remembers, the branch it was last proposed to, if it still exists — that is
// where work that has left as a proposal and not yet merged lives. nil means
// upstream, as ever.
func stackImportFromFork(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, name string, rebuilding bool) (*stackForkRef, error) {
	return stackForkRefFor(m, name, rebuilding, func(branch string) (string, bool) {
		sha, err := repo.LsRemote(ctx, stackRemoteURL(m.Repos[name].Fork), "refs/heads/"+branch)
		return sha, err == nil
	})
}

// stackForkRefFor is stackImportFromFork with the remote lookup passed in, so
// the decision is testable without a forge. An explicit trackBranch that does
// not exist is an error: importing from upstream instead would quietly hand
// back a stackspace without the work the branch was named to carry.
func stackForkRefFor(m *stackManifest, name string, rebuilding bool, resolve func(branch string) (string, bool)) (*stackForkRef, error) {
	r := m.Repos[name]
	if r == nil {
		return nil, nil
	}
	if r.TrackBranch != "" {
		sha, ok := resolve(r.TrackBranch)
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
	branch := m.sendBranch(name, m.LastPropose[name])
	if sha, ok := resolve(branch); ok {
		return &stackForkRef{Branch: branch, Commit: sha}, nil
	}
	return nil, nil
}
