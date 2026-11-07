package cli

import (
	"context"
	"fmt"
	"strings"
)

// PR identifies an adopted upstream PR; Proposal identifies a recorded fork
// branch. Exactly one must be set. No fork identity is needed for an adopted PR.
type stackReleaseItem struct {
	Proposal stackProposal
	PR       int
}

type stackReleaseProof struct {
	Status, Version, MergeCommit, ReleaseCommit string
}

func stackReleaseContains(ctx context.Context, a stackDepsAPI, r *stackRepo, merge, tip string) (bool, error) {
	_, slug, _ := strings.Cut(r.Upstream, "/")
	var comparison struct {
		Status string `json:"status"`
	}
	err := a.request(ctx, "GET", "/repos/"+slug+"/compare/"+merge+"..."+tip, nil, &comparison)
	return comparison.Status == "ahead" || comparison.Status == "identical", err
}

// The aggregate release check uses the same per-item verdict, but additionally
// requires one common release to contain the sibling's entire upstream cursor.
func stackReleaseItemProof(ctx context.Context, a stackDepsAPI, r *stackRepo, pr stackDepsPR, cursor, version, release string) (stackReleaseProof, error) {
	proof := stackReleaseProof{Status: "open"}
	if pr.MergedAt == nil {
		return proof, nil
	}
	proof.Status, proof.MergeCommit = "merged", pr.MergeSHA
	if !stackIsSHA(pr.MergeSHA) {
		return proof, fmt.Errorf("can't tell: missing upstream merge commit")
	}
	if cursor != "" {
		if !stackIsSHA(cursor) {
			return proof, fmt.Errorf("can't tell: missing upstream cursor")
		}
		included, err := stackReleaseContains(ctx, a, r, pr.MergeSHA, cursor)
		if err != nil {
			return proof, fmt.Errorf("can't check cursor ancestry: %w", err)
		}
		if included {
			proof.Status = "merged and in cursor"
		}
	}
	if release != "" {
		included, err := stackReleaseContains(ctx, a, r, pr.MergeSHA, release)
		if err != nil {
			return proof, fmt.Errorf("can't check release ancestry: %w", err)
		}
		if included {
			proof.Status, proof.Version, proof.ReleaseCommit = "released in V", version, release
		}
	}
	return proof, nil
}

// stackReleaseProposalProof exposes complete per-item evidence for recorded and
// adopted PRs. An inconclusive check returns an error alongside the facts known
// so far; absence of public release containment preserves the merged verdict.
func stackReleaseProposalProof(ctx context.Context, a stackDepsAPI, m *stackManifest, sibling string, item stackReleaseItem, pkg, version, feed string) (stackReleaseProof, error) {
	var proof stackReleaseProof
	r := m.Repos[sibling]
	if r == nil {
		return proof, fmt.Errorf("unknown sibling %s", sibling)
	}
	if item.PR < 0 || (item.PR > 0) == (item.Proposal.Branch != "") {
		return proof, fmt.Errorf("identify one proposal branch or PR number")
	}
	var pr stackDepsPR
	var err error
	if item.PR == 0 {
		pr, err = a.proposal(ctx, r, item.Proposal)
	} else {
		pr, err = a.pullRequest(ctx, r, item.PR)
	}
	if err != nil {
		return proof, fmt.Errorf("can't check proposal: %w", err)
	}
	proof, err = stackReleaseItemProof(ctx, a, r, pr, m.cursor(sibling), "", "")
	if err != nil || proof.Status == "open" {
		return proof, err
	}
	v, commit, err := stackReleaseFindVersion(ctx, a, r, []stackDepsPR{pr}, "", pkg, version, feed)
	if err != nil {
		if strings.HasPrefix(err.Error(), "not yet released") {
			return proof, nil
		}
		return proof, err
	}
	proof.Status, proof.Version, proof.ReleaseCommit = "released in V", v, commit
	return proof, nil
}
