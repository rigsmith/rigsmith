package commands

import (
	"context"
	"io"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/service"
)

func reconcile(ctx context.Context, out io.Writer, repo *gitrepo.Repo, remote, branch string, allowMergeTool bool) error {
	return applicationService(out).Reconcile(ctx, service.ReconcileRequest{
		Repo: repo, Remote: remote, Branch: branch, AllowMergeTool: allowMergeTool && interactive(),
	})
}

func finishAuditedMerge(ctx context.Context, repo *gitrepo.Repo) error {
	return service.FinishMerge(ctx, repo)
}

func repairWedgedMerge(ctx context.Context, out io.Writer, staging string, allowMergeTool bool) bool {
	return applicationService(out).RepairMerge(ctx, staging, allowMergeTool && interactive()).Safe
}
