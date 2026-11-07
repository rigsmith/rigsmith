package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

type stackEdgeStatusItem struct {
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Version       string `json:"version,omitempty"`
	MergeCommit   string `json:"mergeCommit,omitempty"`
	ReleaseCommit string `json:"releaseCommit,omitempty"`
	Note          string `json:"note,omitempty"`
}

type stackEdgeStatusReport struct {
	Member      string                `json:"member"`
	Package     string                `json:"package,omitempty"`
	Items       []stackEdgeStatusItem `json:"items"`
	Local       string                `json:"local"`
	Upstream    string                `json:"upstream"`
	TrackCommit string                `json:"trackCommit,omitempty"`
	TrackBase   string                `json:"trackBase,omitempty"`
	UpstreamTip string                `json:"upstreamTip,omitempty"`
	NewestEdge  string                `json:"newestEdge"`
	Retirable   bool                  `json:"retirable"`
	Version     string                `json:"version,omitempty"`
	Retirement  string                `json:"retirement"`
}

func newStackEdgeStatusCmd() *cobra.Command {
	var pkg, version, feed string
	var asJSON bool
	c := &cobra.Command{
		Use:   "status [member]",
		Short: "Show carried fixes, integration freshness and edge retirement evidence",
		Long: "Reads each recorded proposal and adopted PR using public NuGet repository commit evidence.\n" +
			"Shows unproposed content, local integration receipt freshness, upstream base freshness,\n" +
			"and the newest edge version (authenticated feeds report can't check; no feed credentials are used).\n" +
			"Retirement requires every item released in one public version and a three-way content merge\n" +
			"that leaves that release unchanged. Git checks run in a temporary bare repository.\n" +
			"--package selects the original evidence package; otherwise uses the first sorted non-excluded\n" +
			"NuGet id discovered in the member. --feed is the public flat-container base, not the edge feed.\n" +
			"Read-only, including with --dry-run. No manifest, stackspace ref, fork or feed is changed.",
		Args: cobra.MaximumNArgs(1),
	}
	c.Flags().BoolVar(&asJSON, "json", false, "Print the status report as JSON")
	c.Flags().StringVar(&pkg, "package", "", "Original NuGet package for public release evidence (one member only)")
	c.Flags().StringVar(&version, "version", "", "Public release to check (otherwise checks 20 newest stable versions)")
	c.Flags().StringVar(&feed, "feed", stackNugetFeed, "Public NuGet flat-container base URL")
	c.ValidArgsFunction = func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		root, err := stackRoot(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		m, _, err := loadStackManifest(root)
		if err != nil || m == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, name := range m.names() {
			if m.Edge[name] != nil {
				names = append(names, name)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
	for _, flag := range []string{"package", "version", "feed"} {
		_ = c.RegisterFlagCompletionFunc(flag, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		})
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		root, err := stackRoot(cmd.Context())
		if err != nil {
			return err
		}
		m, _, err := loadStackManifest(root)
		if err != nil {
			return err
		}
		if m == nil {
			return fmt.Errorf("no stack manifest")
		}
		var names []string
		for _, name := range m.names() {
			if m.Edge[name] != nil {
				names = append(names, name)
			}
		}
		if len(args) != 0 {
			if m.Edge[args[0]] == nil {
				return fmt.Errorf("%s is not an edge member", args[0])
			}
			names = args
		}
		if pkg != "" && len(names) != 1 {
			return fmt.Errorf("--package requires one edge member")
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()
		api := stackDepsClient()
		reports := make([]stackEdgeStatusReport, 0, len(names))
		for _, name := range names {
			reports = append(reports, stackEdgeMemberStatus(ctx, api, root, m, name, pkg, version, feed))
		}
		if asJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(reports)
		}
		for _, r := range reports {
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", r.Member, r.Package)
			for _, item := range r.Items {
				state := item.Status
				if state == "released in V" {
					state = "released in " + item.Version
				}
				if item.Note != "" {
					state += " — " + item.Note
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %-30s %s\n", strings.TrimSpace(item.Kind+" "+item.Name), state)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  trackBranch locally: %s\n  trackBranch upstream: %s\n  newest edge version: %s\n  retirement: %s\n", r.Local, r.Upstream, r.NewestEdge, r.Retirement)
		}
		return nil
	}
	return c
}

func stackEdgeEvidencePackage(ctx context.Context, root, member string, edge *stackEdge) (string, error) {
	resp, err := dotnet.New().Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: member, IncludeUnversioned: true})
	if err != nil {
		return "", err
	}
	var ids []string
	for _, p := range resp.Packages {
		if !slices.Contains(edge.Exclude, p.Name) {
			ids = append(ids, p.Name)
		}
	}
	slices.Sort(ids)
	if len(ids) == 0 {
		return "", fmt.Errorf("can't check: no original NuGet package discovered; select --package")
	}
	return ids[0], nil
}

func stackEdgeMemberStatus(ctx context.Context, api stackDepsAPI, root string, m *stackManifest, name, pkg, version, feed string) stackEdgeStatusReport {
	r := stackEdgeStatusReport{Member: name, Package: pkg, Items: []stackEdgeStatusItem{}, Local: "can't check", Upstream: "can't check", NewestEdge: "can't check", Retirement: "not retirable: can't check content"}
	edge, member := m.Edge[name], m.Repos[name]
	var packageErr error
	if pkg == "" {
		pkg, packageErr = stackEdgeEvidencePackage(ctx, root, name, edge)
		r.Package = pkg
	}
	if packageErr == nil {
		as, ok := stackRepublishedID(m.publishing()[name], pkg)
		if ok {
			newest, err := stackEdgeNewestVersion(ctx, api.client, edge.Feed, as, edge.Label)
			if err != nil {
				r.NewestEdge = "can't check — " + err.Error()
			} else {
				r.NewestEdge = newest
			}
		}
	}
	refresh := fmt.Sprintf("run `rig stack propose %s --track-only`", name)
	receipt, receiptErr := stackCIGit(ctx, root, "rev-parse", "--verify", "refs/rigsmith/integration/"+name+"^{tree}")
	head, headErr := stackCIGit(ctx, root, "rev-parse", "--verify", "HEAD:"+name)
	if receiptErr == nil && headErr == nil {
		r.Local = "fresh"
		if string(receipt) != string(head) {
			r.Local = "stale — " + refresh
		}
	} else {
		r.Local = "can't check (missing integration receipt or HEAD prefix) — " + refresh
	}

	dir, err := os.MkdirTemp("", "rig-edge-status-")
	if err == nil {
		defer os.RemoveAll(dir)
	}
	var history *gitrepo.Repo
	if err == nil {
		if _, err = stackCIGit(ctx, dir, "init", "--bare", "-q"); err == nil {
			history, err = gitrepo.Open(ctx, dir)
		}
	}
	var historyErr error
	if err != nil {
		historyErr = err
	} else {
		r.TrackCommit, historyErr = history.FetchRef(ctx, stackRemoteURL(member.Fork), "refs/heads/"+member.TrackBranch, stackAuthFor(ctx, member.Fork))
		if historyErr == nil {
			parents, e := stackCIGit(ctx, dir, "rev-parse", r.TrackCommit+"^@")
			if e != nil || len(strings.Fields(string(parents))) != 1 {
				historyErr = fmt.Errorf("trackBranch must have exactly one upstream parent")
			} else {
				r.TrackBase = strings.TrimSpace(string(parents))
			}
		}
		if historyErr == nil {
			r.UpstreamTip, err = history.FetchRef(ctx, stackRemoteURL(member.Upstream), "refs/heads/"+m.branch(name), stackAuthFor(ctx, member.Upstream))
			if err == nil {
				r.Upstream = "fresh"
				if r.TrackBase != r.UpstreamTip {
					r.Upstream = "stale (upstream moved) — run `rig stack pull " + name + "`, then " + refresh
				}
			}
		}
	}

	var inputs []stackReleaseItem
	var labels []stackEdgeStatusItem
	for _, topic := range slices.Sorted(maps.Keys(m.Proposals[name])) {
		inputs = append(inputs, stackReleaseItem{Proposal: m.Proposals[name][topic]})
		labels = append(labels, stackEdgeStatusItem{Kind: "proposal", Name: topic})
	}
	for _, adopted := range edge.Adopted {
		parts := strings.Split(strings.TrimSuffix(adopted.PR, "/"), "/")
		n, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil {
			n = -1 // the manifest accepts uint64; the API helper requires an int
		}
		inputs = append(inputs, stackReleaseItem{PR: n})
		labels = append(labels, stackEdgeStatusItem{Kind: "adopted", Name: adopted.PR})
	}
	accounted := r.TrackBase
	accountErr := historyErr
	allReleased := packageErr == nil
	var releaseCommit, releaseVersion string
	var releasedPRs []stackDepsPR
	releasesDiffer := false
	for i, input := range inputs {
		item := labels[i]
		var proof stackReleaseProof
		proofErr := packageErr
		if proofErr == nil {
			proof, proofErr = stackReleaseProposalProof(ctx, api, m, name, input, pkg, version, feed)
		}
		item.Status, item.Version, item.MergeCommit, item.ReleaseCommit = proof.Status, proof.Version, proof.MergeCommit, proof.ReleaseCommit
		if proofErr != nil {
			item.Note = proofErr.Error()
		}
		if item.Status == "" {
			item.Status = "can't check"
		}
		r.Items = append(r.Items, item)
		if proofErr != nil || proof.Status != "released in V" {
			allReleased = false
		} else {
			merged := "proven merged"
			releasedPRs = append(releasedPRs, stackDepsPR{MergedAt: &merged, MergeSHA: proof.MergeCommit})
			if releaseCommit == "" {
				releaseCommit, releaseVersion = proof.ReleaseCommit, proof.Version
			} else if releaseCommit != proof.ReleaseCommit || releaseVersion != proof.Version {
				releasesDiffer = true
			}
		}
		if accountErr == nil {
			var commit string
			if input.PR != 0 {
				commit = edge.Adopted[i-len(m.Proposals[name])].Commit
				accountErr = history.FetchObjects(ctx, stackRemoteURL(member.Upstream), commit, stackAuthFor(ctx, member.Upstream))
			} else {
				var pr stackDepsPR
				pr, accountErr = api.proposal(ctx, member, input.Proposal)
				if accountErr == nil {
					commit = pr.Head.SHA
					if !stackIsSHA(commit) {
						accountErr = fmt.Errorf("can't check: PR head commit unavailable")
					} else {
						accountErr = history.FetchObjects(ctx, stackRemoteURL(member.Upstream), commit, stackAuthFor(ctx, member.Upstream))
					}
				}
			}
			if accountErr == nil {
				var tree string
				tree, accountErr = stackEdgeMergeTree(ctx, dir, accounted, commit)
				if accountErr == nil {
					b, e := stackCIGit(ctx, dir, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", accounted, "-p", commit, "-m", "edge status: accounted PR content")
					accounted, accountErr = strings.TrimSpace(string(b)), e
				}
			}
		}
	}
	if accountErr != nil {
		r.Items = append(r.Items, stackEdgeStatusItem{Kind: "unproposed content", Status: "can't check", Note: "could not reconstruct recorded and adopted PR content"})
	} else {
		contained, e := stackEdgeContentContained(ctx, dir, r.TrackCommit, accounted)
		if e != nil {
			r.Items = append(r.Items, stackEdgeStatusItem{Kind: "unproposed content", Status: "can't check", Note: "content merge failed"})
		} else if !contained {
			r.Items = append(r.Items, stackEdgeStatusItem{Kind: "unproposed content", Status: "carried"})
		}
	}
	if !allReleased {
		r.Retirement = "not retirable: not every carried PR is proven released in one public version"
		if packageErr != nil {
			r.Retirement = "not retirable: " + packageErr.Error()
		}
		return r
	}
	if releaseCommit == "" || releasesDiffer {
		releaseVersion, releaseCommit, err = stackReleaseFindVersion(ctx, api, member, releasedPRs, "", pkg, version, feed)
		if err != nil {
			r.Retirement = "not retirable: " + err.Error()
			return r
		}
	}
	if historyErr != nil {
		return r
	}
	if err := history.FetchObjects(ctx, stackRemoteURL(member.Upstream), releaseCommit, stackAuthFor(ctx, member.Upstream)); err != nil {
		return r
	}
	contained, err := stackEdgeContentContained(ctx, dir, r.TrackCommit, releaseCommit)
	if err != nil {
		r.Retirement = "not retirable: content not contained (merge conflicts or ancestry unavailable)"
	} else if !contained {
		r.Retirement = "not retirable: content not contained in public " + releaseVersion
	} else {
		r.Retirable, r.Version = true, releaseVersion
		r.Retirement = "retirable at " + releaseVersion + " — run `rig stack released` to switch consumers back"
	}
	return r
}

// Both commits are unfiltered member history in a disposable bare repository.
// merge-tree selects their merge base; comparing trees also catches reworked
// fixes whose PR ancestry alone would incorrectly permit retirement.
func stackEdgeMergeTree(ctx context.Context, dir, target, carried string) (string, error) {
	b, err := stackCIGit(ctx, dir, "merge-tree", "--write-tree", target, carried)
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(b))
	if !stackIsSHA(tree) {
		return "", fmt.Errorf("can't check: merge-tree returned no unique tree")
	}
	return tree, nil
}

func stackEdgeContentContained(ctx context.Context, dir, carried, target string) (bool, error) {
	tree, err := stackEdgeMergeTree(ctx, dir, target, carried)
	if err != nil {
		return false, err
	}
	b, err := stackCIGit(ctx, dir, "rev-parse", "--verify", target+"^{tree}")
	return tree == strings.TrimSpace(string(b)), err
}

// Feed reads intentionally use the unauthenticated released transport. Edge
// feeds may be flat-container bases or NuGet v3 service indexes.
func stackEdgeNewestVersion(ctx context.Context, client *http.Client, feed, pkg, label string) (string, error) {
	base := strings.TrimRight(feed, "/")
	if strings.HasSuffix(base, "/index.json") {
		var index struct {
			Resources []struct {
				ID   string `json:"@id"`
				Type string `json:"@type"`
			} `json:"resources"`
		}
		if err := stackReleaseGet(ctx, client, base, &index, false); err != nil {
			return "", err
		}
		base = ""
		for _, r := range index.Resources {
			if strings.HasPrefix(r.Type, "PackageBaseAddress/") {
				base = strings.TrimRight(r.ID, "/")
				break
			}
		}
		if base == "" {
			return "", fmt.Errorf("feed has no NuGet flat-container resource")
		}
	}
	var index struct {
		Versions []string `json:"versions"`
	}
	if err := stackReleaseGet(ctx, client, base+"/"+url.PathEscape(strings.ToLower(pkg))+"/index.json", &index, false); err != nil {
		return "", err
	}
	if label == "" {
		label = "edge"
	}
	newest := ""
	var best stackNugetVersion
	for _, raw := range index.Versions {
		v, ok := stackParseNugetVersion(raw)
		if !ok {
			return "", fmt.Errorf("unsupported feed version")
		}
		// A T that is itself a prerelease yields rc.1.edge.N, for example.
		i := strings.LastIndex(v.release, strings.ToLower(label)+".")
		if i < 0 || i > 0 && v.release[i-1] != '.' {
			continue
		}
		number := v.release[i+len(label)+1:]
		if _, err := strconv.ParseUint(number, 10, 31); err != nil {
			continue
		}
		if newest == "" || stackCompareNuget(v, best) > 0 {
			best, newest = v, v.normalized()
		}
	}
	if newest == "" {
		return "none published", nil
	}
	return newest, nil
}
