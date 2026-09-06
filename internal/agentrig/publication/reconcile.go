package publication

import (
	"context"
	"fmt"

	"github.com/rigsmith/rigsmith/core/gitrepo"
)

// ReconcileRequest makes remote, branch and permission to block on mergetool explicit.
type ReconcileRequest struct {
	Repo           *gitrepo.Repo
	Remote, Branch string
	AllowMergeTool bool
}

// Reconcile repairs a pending merge or merges the remote through supplied policy.
func (w Workflow) Reconcile(ctx context.Context, req ReconcileRequest) error {
	if err := w.Policy.check(); err != nil {
		return err
	}
	repo, remote, branch := req.Repo, req.Remote, req.Branch
	if !repo.InMerge(ctx) {
		conflicted, err := repo.FetchMergeUncommitted(ctx, remote, branch)
		if err != nil {
			return fmt.Errorf("reconcile: %w", err)
		}
		if !conflicted {
			return w.FinishMerge(ctx, repo)
		}
	} else {
		w.emit(MergePending{})
	}

	unresolved, err := w.Policy.Resolve(ctx, repo)
	if err != nil {
		return fmt.Errorf("resolve conflicts: %w", err)
	}
	if n := len(unresolved); n > 0 {
		if !req.AllowMergeTool {
			_ = repo.AbortMerge(ctx)
			return w.Policy.HumanRequired(unresolved)
		}
		w.emit(MergeToolStarting{Count: n})
		if err := repo.RunMergeTool(ctx); err != nil {
			_ = repo.AbortMerge(ctx)
			return fmt.Errorf("mergetool: %w", err)
		}
	}
	return w.FinishMerge(ctx, repo)
}

// FinishMerge audits a pending merge before committing. Refused merges remain
// resumable. Working files must match the index so cleaning only a working copy
// cannot hide a credential still staged for commit.
func (w Workflow) FinishMerge(ctx context.Context, repo *gitrepo.Repo) error {
	if err := w.Policy.check(); err != nil {
		return err
	}
	merging := repo.InMerge(ctx)
	if merging {
		dirty, err := repo.HasUnstagedChanges(ctx)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("merge has unstaged changes; stage the intended resolutions before retrying")
		}
	}
	root, err := repo.Toplevel(ctx)
	if err != nil {
		return err
	}
	if merging {
		if err := w.Policy.Prepare(ctx, root); err != nil {
			return err
		}
	}
	if err = w.Policy.Audit(root); err != nil {
		return err
	}
	if !merging {
		return nil
	}
	return repo.CommitMerge(ctx)
}
