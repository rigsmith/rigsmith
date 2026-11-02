package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Resolve the whole set before fetching any executable source. Detail responses
// are validated again: a branch may have moved since the list request.
func stackTopicHeads(ctx context.Context, m *stackManifest, topic string, api stackDepsAPI) (map[string]stackDepsPR, error) {
	if os.Getenv("GITHUB_EVENT_NAME") == "pull_request_target" {
		return nil, fmt.Errorf("topic CI refuses pull_request_target; use pull_request without secrets and with contents: read")
	}
	heads := map[string]stackDepsPR{}
	for _, n := range m.names() {
		p, ok := m.Proposals[n][topic]
		if !ok {
			continue
		}
		for _, dep := range p.DependsOn {
			if _, ok := m.Proposals[dep][topic]; !ok {
				return nil, fmt.Errorf("topic %q member %s: missing proposal %s", topic, n, dep)
			}
		}
		pr, err := api.proposal(ctx, m.Repos[n], p)
		if err != nil {
			return nil, fmt.Errorf("topic %q member %s: %w", topic, n, err)
		}
		if pr.State != "open" || pr.MergedAt != nil {
			return nil, fmt.Errorf("topic %q member %s PR %d is closed or merged", topic, n, pr.Number)
		}
		if pr.Mergeable != nil && !*pr.Mergeable {
			return nil, fmt.Errorf("topic %q member %s PR %d conflicts with its base", topic, n, pr.Number)
		}
		if !stackIsSHA(pr.Head.SHA) || pr.Head.Ref != p.Branch {
			return nil, fmt.Errorf("topic %q member %s: invalid PR head", topic, n)
		}
		host, _ := stackSplitHost(m.Repos[n].Upstream)
		spec := host + "/" + pr.Head.Repo.FullName
		if !strings.EqualFold(spec, m.Repos[n].Fork) && !strings.EqualFold(spec, m.Repos[n].Upstream) {
			return nil, fmt.Errorf("topic %q member %s: untrusted PR head repository", topic, n)
		}
		heads[n] = pr
	}
	if len(heads) == 0 {
		return nil, fmt.Errorf("topic %q has no proposals", topic)
	}
	return heads, nil
}

func stackTopicReplace(ctx context.Context, root, member string, r *stackRepo, pr stackDepsPR) (string, error) {
	temp, err := os.MkdirTemp("", "rig-stack-topic-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	if _, err = stackCIGit(ctx, temp, "init", "--bare", "-q"); err != nil {
		return "", err
	}
	host, _ := stackSplitHost(r.Upstream)
	// Never use an API-provided clone URL or ref as a Git argument.
	remote := stackRemoteURL(host + "/" + pr.Head.Repo.FullName)
	if _, err = stackCIGit(ctx, temp, "fetch", "--no-tags", "--depth=1", remote, pr.Head.SHA); err != nil {
		return "", fmt.Errorf("member %s: fetching topic head failed", member)
	}
	if _, err = stackCIGit(ctx, temp, "update-ref", "HEAD", pr.Head.SHA); err != nil {
		return "", err
	}
	sha, err := stackCIReplace(ctx, temp, root, member)
	if err != nil {
		return "", fmt.Errorf("member %s topic head: %w", member, err)
	}
	return sha, nil
}

// Kept separate from command wiring so topic mode cannot accidentally fall back
// to the triggering checkout (which is often a synthetic merge commit).
func stackTopicApply(ctx context.Context, root string, m *stackManifest, heads map[string]stackDepsPR, resolved map[string]string) error {
	for _, n := range m.names() {
		pr, ok := heads[n]
		if !ok {
			continue
		}
		sha, err := stackTopicReplace(ctx, root, n, m.Repos[n], pr)
		if err != nil {
			return err
		}
		resolved[n] = sha
	}
	return nil
}
