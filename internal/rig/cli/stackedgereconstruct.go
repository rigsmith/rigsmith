package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

type stackEdgeReconstructOptions struct {
	// Checkout is the stackspace root (or a git directory from which to load a
	// seed). Manifest and Seed are exclusive; neither selects the checkout's
	// own committed manifest.
	Checkout     string
	Manifest     string
	Seed         string
	Members      []string
	AllowedHosts string
	UpstreamTip  bool
}

type stackEdgeProvenance struct {
	TrackCommit    string
	TrackParent    string
	UpstreamCommit string
	UpstreamTip    string
}

type stackEdgeReconstruction struct {
	Root       string
	Provenance map[string]stackEdgeProvenance
}

// stackEdgeInconclusiveError distinguishes an untested conflicting combination
// from a build failure. A caller must never publish it as a successful rebuild.
type stackEdgeInconclusiveError struct {
	Member     string
	Provenance stackEdgeProvenance
	err        error
}

func (e *stackEdgeInconclusiveError) Error() string {
	return fmt.Sprintf("%s: upstream-tip reconstruction is inconclusive: %s", e.Member, e.Remedy())
}

func (e *stackEdgeInconclusiveError) Remedy() string {
	return fmt.Sprintf("pull, resolve on the integration line, `rig stack propose %s --track-only`", e.Member)
}

func (e *stackEdgeInconclusiveError) Unwrap() error { return e.err }

// stackEdgeReconstruct returns a remote-free fused repository and the exact
// commits it built. It never reads member working trees or publishes a home.
// The caller owns cleanup on success; failures clean up before returning.
func stackEdgeReconstruct(ctx context.Context, opts stackEdgeReconstructOptions) (_ *stackEdgeReconstruction, cleanup func(), err error) {
	temp, err := os.MkdirTemp("", "rig-stack-edge-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(temp) }
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	data, _, err := stackReconstructManifest(ctx, opts.Checkout, temp, opts.Manifest, opts.Seed, opts.AllowedHosts)
	if err != nil {
		return nil, cleanup, err
	}
	root := filepath.Join(temp, "stack")
	if err = os.Mkdir(root, 0700); err != nil {
		return nil, cleanup, err
	}
	// NuGet matches project references by absolute path. A symlinked temp
	// root (notably /var on macOS) can make restore and pack disagree and
	// silently omit project dependencies from the nuspec.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, cleanup, err
	}
	m, err := stackReconstructRepository(ctx, root, data, opts.AllowedHosts)
	if err != nil {
		return nil, cleanup, err
	}
	edge := map[string]bool{}
	if len(opts.Members) == 0 {
		return nil, cleanup, fmt.Errorf("at least one edge member is required")
	}
	for _, name := range opts.Members {
		r := m.Repos[name]
		if r == nil {
			return nil, cleanup, fmt.Errorf("unknown edge member %s", name)
		}
		if r.TrackBranch == "" {
			return nil, cleanup, fmt.Errorf("edge member %s needs a trackBranch", name)
		}
		edge[name] = true
	}
	// Tip mode only advances edge members. Siblings always use recorded cursors.
	if err = stackReconstructCursors(m, "", false, edge); err != nil {
		return nil, cleanup, err
	}
	if err = stackReconstructWrite(root, m); err != nil {
		return nil, cleanup, err
	}
	if err = stackCIGuardPush(ctx, root); err != nil {
		return nil, cleanup, err
	}
	// stackPullOne amends import commits, so root the fused history on the
	// manifest just as init does, with a local identity for unattended runs.
	for _, setting := range [][2]string{{"user.name", "rig CI"}, {"user.email", "ci@localhost"}, {"commit.gpgsign", "false"}} {
		if _, err = stackCIGit(ctx, root, "config", setting[0], setting[1]); err != nil {
			return nil, cleanup, err
		}
	}
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		return nil, cleanup, err
	}
	if _, err = repo.Commit(ctx, "stack: stackspace manifest"); err != nil {
		return nil, cleanup, err
	}
	m, src, err := loadStackManifest(root)
	if err != nil {
		return nil, cleanup, err
	}
	forks := map[string]*stackForkRef{}
	for _, name := range m.names() {
		fork, e := stackImportFromFork(ctx, repo, m, name, true)
		if e != nil {
			return nil, cleanup, e
		}
		if edge[name] && fork == nil {
			return nil, cleanup, fmt.Errorf("edge member %s needs a trackBranch", name)
		}
		forks[name] = fork
	}
	bin, err := ensureJoshProxy(ctx, m.joshVersion(), io.Discard)
	if err != nil {
		return nil, cleanup, err
	}
	result := &stackEdgeReconstruction{Root: root, Provenance: map[string]stackEdgeProvenance{}}
	for _, name := range m.names() {
		fork := forks[name]
		if err = stackPullOne(ctx, io.Discard, repo, bin, src, m, name, stackPullOpts{initial: true, at: m.cursor(name), fork: fork}); err != nil {
			return nil, cleanup, fmt.Errorf("importing %s: %w", name, err)
		}
		p := stackEdgeProvenance{UpstreamCommit: m.cursor(name)}
		if edge[name] {
			p.TrackCommit = fork.Commit
			parents, e := repo.RevParse(ctx, fork.Commit+"^@")
			if e != nil || len(strings.Fields(parents)) != 1 {
				return nil, cleanup, fmt.Errorf("%s: trackBranch must have exactly one upstream parent", name)
			}
			p.TrackParent = strings.TrimSpace(parents)
			p.UpstreamCommit = p.TrackParent
			if opts.UpstreamTip {
				p.UpstreamTip, err = stackResolveUpstream(ctx, repo, stackRemoteURL(m.Repos[name].Upstream), stackPin{Kind: "branch", Value: m.branch(name)})
				if err != nil {
					return nil, cleanup, err
				}
				if err = repo.FetchObjects(ctx, stackRemoteURL(m.Repos[name].Upstream), p.UpstreamTip, stackAuthFor(ctx, m.Repos[name].Upstream)); err != nil {
					return nil, cleanup, err
				}
				tree, e := stackCIGit(ctx, root, "merge-tree", "--write-tree", "--merge-base="+p.TrackParent, p.UpstreamTip, p.TrackCommit)
				if e != nil {
					var exit *exec.ExitError
					if errors.As(e, &exit) && exit.ExitCode() == 1 {
						return nil, cleanup, &stackEdgeInconclusiveError{Member: name, Provenance: p, err: e}
					}
					return nil, cleanup, e
				}
				commit, e := repo.CommitTree(ctx, strings.TrimSpace(string(tree)), p.UpstreamTip, "stack edge: upstream tip "+name)
				if e != nil {
					return nil, cleanup, e
				}
				if _, err = stackCIReplaceRef(ctx, root, root, name, commit); err != nil {
					return nil, cleanup, err
				}
				if err = stackSetCursor(src, m, name, p.UpstreamTip); err != nil {
					return nil, cleanup, err
				}
				if _, err = repo.CommitPaths(ctx, "stack edge: upstream tip cursor "+name, "rig.stack.jsonc"); err != nil {
					return nil, cleanup, err
				}
				p.UpstreamCommit = p.UpstreamTip
			}
		}
		result.Provenance[name] = p
	}
	if err = stackCIGuardPush(ctx, root); err != nil {
		return nil, cleanup, err
	}
	return result, cleanup, nil
}
