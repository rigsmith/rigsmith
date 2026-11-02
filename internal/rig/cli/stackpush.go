package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

func stackDeliveryTree(ctx context.Context, repo *gitrepo.Repo, commit string) (string, error) {
	return repo.RevParse(ctx, commit+"^{tree}")
}

// Both delivery verbs use this decision: commit IDs cannot identify changes
// already delivered through a squash or rebase merge.
func stackDeliveryUnsent(ctx context.Context, repo *gitrepo.Repo, tip, localTree string) (bool, error) {
	upstream, err := stackDeliveryTree(ctx, repo, tip)
	return upstream != localTree, err
}

func stackRequireFastForward(ctx context.Context, repo *gitrepo.Repo, tip, head, name string) error {
	ok, err := repo.IsAncestor(ctx, tip, head)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: refusing to push %s: it does not descend from upstream %s; run `rig stack pull %s` and reconcile the member's history before retrying", name, head, tip, name)
	}
	return nil
}

// Keep ordinary filtered history byte-for-byte. After a squash/rebase, an exact
// tree boundary is required: guessing which patches landed can lose local work.
func stackPushHead(ctx context.Context, repo *gitrepo.Repo, tip, head string) (string, error) {
	upstream, err := stackDeliveryTree(ctx, repo, tip)
	if err != nil {
		return "", err
	}
	local, err := stackDeliveryTree(ctx, repo, head)
	if err != nil {
		return "", err
	}
	unsent, err := stackDeliveryUnsent(ctx, repo, tip, local)
	if err != nil {
		return "", err
	}
	if !unsent {
		return tip, nil
	}
	if ok, err := repo.IsAncestor(ctx, tip, head); err != nil {
		return "", err
	} else if ok {
		entries, err := repo.LogRange(ctx, tip, head)
		if err != nil {
			return "", err
		}
		administrative := false
		for _, c := range entries {
			if strings.HasPrefix(c.Subject, "stack: remove ") || strings.HasPrefix(c.Subject, "stack: cursor ") {
				administrative = true
			}
		}
		if !administrative {
			return head, nil
		}
	}
	commits, err := repo.FirstParentCommits(ctx, head)
	if err != nil {
		return "", err
	}
	boundary := -1
	for i, c := range commits {
		tree, err := stackDeliveryTree(ctx, repo, c.Hash)
		if err != nil {
			return "", err
		}
		if upstream == tree {
			boundary = i
			break
		}
	}
	if boundary < 0 {
		return "", fmt.Errorf("cannot safely fast-forward filtered commit %s onto upstream %s: no exact tree boundary (possibly a partial squash or overlapping upstream changes); run `rig stack pull` and reconcile the member, or use `rig stack propose` to review the remaining diff", head, tip)
	}
	parent := tip
	for i := boundary - 1; i >= 0; i-- {
		c := commits[i]
		if len(c.Parents) != 1 {
			return "", fmt.Errorf("cannot safely replay merge %s onto upstream %s; reconcile the member or use `rig stack propose` to review it", c.Hash, tip)
		}
		tree, err := stackDeliveryTree(ctx, repo, c.Hash)
		if err != nil {
			return "", err
		}
		prior, err := stackDeliveryTree(ctx, repo, parent)
		if err != nil {
			return "", err
		}
		if tree == prior {
			continue
		}
		parent, err = repo.ReparentCommit(ctx, c.Hash, parent)
		if err != nil {
			return "", err
		}
	}
	return parent, nil
}

// Re-read immediately before the write, independent of server receive policy.
func stackPushToUpstream(ctx context.Context, repo *gitrepo.Repo, url, head, branch, name string) error {
	auth := stackAuthForURL(ctx, url)
	current, err := repo.LsRemote(ctx, url, "refs/heads/"+branch, auth)
	if err != nil {
		return err
	}
	if err := repo.FetchObjects(ctx, url, current, auth); err != nil {
		return err
	}
	if err := stackRequireFastForward(ctx, repo, current, head, name); err != nil {
		return err
	}
	return repo.PushRef(ctx, url, head, "refs/heads/"+branch, auth)
}
