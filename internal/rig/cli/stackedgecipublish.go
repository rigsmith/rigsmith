package cli

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/plugin"
)

func stackEdgeCIPublish(ctx context.Context, out io.Writer, cwd, tmp string, m *stackManifest, base string, r stackEdgeCIReport, key string, o stackEdgeCIOptions) error {
	e := m.Edge[o.Member]
	label := e.Label
	if label == "" {
		label = "edge"
	}
	if r.Schema != 1 || r.SeedBase != base || r.Member != o.Member || r.Feed != e.Feed || r.Label != label {
		return fmt.Errorf("edge report selections differ from trusted seed")
	}
	if r.Status != "packed" || r.Pack.Status != "packed" || r.Number == 0 || r.Pack.Here || !r.Pack.Publishable {
		return fmt.Errorf("only packed CI reports with a nonzero number are publishable")
	}
	if r.Number != o.Number {
		return fmt.Errorf("report number differs from trusted --number")
	}
	if r.Tip && !o.AllowTip {
		return fmt.Errorf("tip report requires --allow-tip-publish")
	}
	if r.Pack.Member != o.Member {
		return fmt.Errorf("edge report member differs")
	}
	root, err := stackEdgeCISnapshot(ctx, cwd, tmp, m, r, o.Member)
	if err != nil {
		return err
	}
	want, err := stackEdgeCIExpectations(ctx, root, m, r, o)
	if err != nil {
		return err
	}
	packages, err := stackEdgeVerify(o.Out, want)
	if err != nil {
		return err
	}
	hashes, err := stackEdgeCIHashes(o.Out, packages)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(hashes, r.SHA256) {
		return fmt.Errorf("package sha256 differs from report")
	}
	if !reflect.DeepEqual(packages, r.Pack.Artifacts) {
		return fmt.Errorf("package inventory differs from report")
	}
	if e.PushKeySecret == "" || key == "" {
		return fmt.Errorf("publish needs the credential named by edge.pushKeySecret")
	}
	feed, err := stackEdgeCIOpenFeed(ctx, o.Client, e.Feed, key)
	if err != nil {
		return err
	}
	// Check every id before the first write; a reset must not partially publish.
	for _, pkg := range packages {
		if err = feed.monotonic(ctx, pkg.ID, want.Version); err != nil {
			return err
		}
	}
	if o.DryRun {
		_, err = fmt.Fprintln(out, "Would push independently verified edge packages")
		return err
	}
	for _, pkg := range packages {
		if err = feed.push(ctx, filepath.Join(o.Out, filepath.FromSlash(pkg.File)), want.Version); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "Published %d verified edge package(s)\n", len(packages))
	return err
}

// Fetches only trusted remotes into private bare repositories. No checkout,
// filters, hooks, adapter registry or project evaluation is involved.
func stackEdgeCISnapshot(ctx context.Context, cwd, tmp string, m *stackManifest, r stackEdgeCIReport, member string) (string, error) {
	root := filepath.Join(tmp, "source")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	if len(r.Pack.Provenance) != len(m.Repos) {
		return "", fmt.Errorf("report provenance differs from seed members")
	}
	for _, name := range m.names() {
		repo := m.Repos[name]
		p, ok := r.Pack.Provenance[name]
		if !ok {
			return "", fmt.Errorf("missing member provenance")
		}
		bare := filepath.Join(tmp, "objects-"+name)
		if _, err := stackCIGit(ctx, cwd, "init", "--bare", "-q", bare); err != nil {
			return "", err
		}
		fetch := func(remote, ref string) error {
			if _, err := stackCIGit(ctx, bare, "-c", "core.hooksPath=", "fetch", "--no-tags", "--", stackRemoteURL(remote), ref); err != nil {
				return fmt.Errorf("cannot read trusted member ref")
			}
			return nil
		}
		ref := m.cursor(name)
		if name == member {
			if !stackIsSHA(p.TrackCommit) || p.TrackCommit != r.Pack.Commit || !stackIsSHA(p.TrackParent) {
				return "", fmt.Errorf("invalid track provenance")
			}
			if err := fetch(repo.Fork, "refs/heads/"+repo.TrackBranch); err != nil {
				return "", err
			}
			tip, err := stackCIGit(ctx, bare, "rev-parse", "FETCH_HEAD")
			if err != nil {
				return "", fmt.Errorf("cannot read fork trackBranch")
			}
			// A fork-network SHA may be fetchable without being on this branch.
			// Fetching it is not evidence of membership; ancestry is mandatory.
			if err = fetch(repo.Fork, p.TrackCommit); err != nil {
				return "", err
			}
			if _, err := stackCIGit(ctx, bare, "merge-base", "--is-ancestor", p.TrackCommit, strings.TrimSpace(string(tip))); err != nil {
				return "", fmt.Errorf("report TrackCommit is not on the fork's current trackBranch")
			}
			parents, err := stackCIGit(ctx, bare, "rev-list", "--parents", "-n", "1", p.TrackCommit)
			if err != nil || strings.Join(strings.Fields(string(parents)), " ") != p.TrackCommit+" "+p.TrackParent {
				return "", fmt.Errorf("report track parent differs from fork")
			}
			ref = p.TrackCommit
			if r.Tip {
				if !stackIsSHA(p.UpstreamTip) || p.UpstreamCommit != p.UpstreamTip {
					return "", fmt.Errorf("invalid tip provenance")
				}
				if err = fetch(repo.Upstream, "refs/heads/"+m.branch(name)); err != nil {
					return "", err
				}
				if _, err = stackCIGit(ctx, bare, "merge-base", "--is-ancestor", p.UpstreamTip, "FETCH_HEAD"); err != nil {
					return "", fmt.Errorf("report upstream tip is not on upstream")
				}
				tree, err := stackCIGit(ctx, bare, "merge-tree", "--write-tree", "--merge-base="+p.TrackParent, p.UpstreamTip, p.TrackCommit)
				if err != nil {
					return "", fmt.Errorf("tip reconstruction is inconclusive")
				}
				ref = strings.TrimSpace(string(tree))
			} else if p.UpstreamTip != "" || p.UpstreamCommit != p.TrackParent {
				return "", fmt.Errorf("tip provenance requires a tip report")
			}
		} else {
			if !stackIsSHA(ref) || p != (stackEdgeProvenance{UpstreamCommit: ref}) {
				return "", fmt.Errorf("sibling provenance differs from seed cursor")
			}
			if err := fetch(repo.Upstream, ref); err != nil {
				return "", err
			}
		}
		entries, err := stackCIGit(ctx, bare, "ls-tree", "-r", ref)
		if err != nil {
			return "", fmt.Errorf("cannot read member tree")
		}
		for _, line := range strings.Split(string(entries), "\n") {
			if strings.HasPrefix(line, "120000 ") {
				return "", fmt.Errorf("edge publisher refuses member symlinks")
			}
		}
		dest := filepath.Join(root, name)
		if err = os.MkdirAll(dest, 0700); err != nil {
			return "", err
		}
		if err = stackCIMaterializeRef(ctx, bare, dest, ref); err != nil {
			return "", err
		}
	}
	return root, nil
}

func stackEdgeCIExpectations(ctx context.Context, root string, m *stackManifest, r stackEdgeCIReport, o stackEdgeCIOptions) (stackEdgeVerifyRequest, error) {
	var want stackEdgeVerifyRequest
	eco := dotnet.New()
	discovered, err := eco.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: ".", IncludeUnversioned: true, IncludeRegistrySiblings: true})
	if err != nil {
		return want, err
	}
	var pkgs []stackPackPackage
	for _, p := range discovered.Packages {
		if !stackPackUnder(o.Member, p.Dir) {
			want.OtherMemberIDs = append(want.OtherMemberIDs, p.Name)
			for _, name := range m.names() {
				if stackPackUnder(name, p.Dir) {
					if alias, ok := stackRepublishedID(m.publishing()[name], p.Name); ok {
						want.OtherMemberIDs = append(want.OtherMemberIDs, alias)
					}
				}
			}
			continue
		}
		packable, err := stackEdgeCIIsPackable(root, p.ManifestPath)
		if err != nil {
			return want, err
		}
		if packable {
			pkgs = append(pkgs, stackPackPackage{eco: eco, info: eco.Info(), pkg: p})
		}
	}
	slices.SortFunc(pkgs, func(a, b stackPackPackage) int { return strings.Compare(a.pkg.Name, b.pkg.Name) })
	var mapped []stackPackPackage
	for _, p := range pkgs {
		if _, ok := stackRepublishedID(m.publishing()[o.Member], p.pkg.Name); ok {
			mapped = append(mapped, p)
		}
	}
	want.MemberIDs, err = stackEdgeResolveIDs(mapped, m.publishing()[o.Member], m.Edge[o.Member].Exclude)
	if err != nil {
		return want, err
	}
	for _, p := range pkgs {
		if _, ok := want.MemberIDs[p.pkg.Name]; !ok {
			want.MemberIDs[p.pkg.Name] = ""
		}
	}
	for _, alias := range want.MemberIDs {
		if alias != "" && slices.ContainsFunc(pkgs, func(p stackPackPackage) bool { return strings.EqualFold(alias, p.pkg.Name) }) {
			return want, fmt.Errorf("edge publishing rule must not publish an original id")
		}
	}
	want.MaxSize, err = m.Edge[o.Member].effectiveMaxSize()
	if err != nil {
		return want, err
	}
	if want.MaxSize != r.Pack.Expected.MaxSize {
		return want, fmt.Errorf("report size limit differs from trusted seed")
	}
	if len(r.Pack.Expected.Packages) == 0 {
		return want, fmt.Errorf("report must pack a non-empty subset of allowed republished ids")
	}
	selected := map[string]bool{}
	for _, claim := range r.Pack.Expected.Packages {
		if want.MemberIDs[claim.OriginalID] == "" || want.MemberIDs[claim.OriginalID] != claim.ID || selected[claim.OriginalID] {
			return want, fmt.Errorf("report claims an untrusted or duplicate republished id")
		}
		selected[claim.OriginalID] = true
		// Dependencies and assembly names remain build-job evaluation claims.
		// Ownership is checked against the publisher's entire text-only set,
		// including allowed projects that the build job did not pack.
		want.Packages = append(want.Packages, claim)
	}
	for _, p := range pkgs {
		if !selected[p.pkg.Name] {
			continue
		}
		released, err := stackEdgePublicVersion(ctx, o.Client, o.PublicFeed, p.pkg.Name)
		if err != nil {
			return want, err
		}
		v, err := stackEdgeComputeVersion(released, p.pkg.Version, r.Label, r.Number)
		if err != nil {
			return want, err
		}
		a, _ := stackParseNugetVersion(v.Version)
		b, _ := stackParseNugetVersion(want.Version)
		if want.Version == "" || stackCompareNuget(a, b) > 0 {
			want.Version = v.Version
		}
	}
	if want.Version == "" || r.Pack.Version != want.Version || r.Pack.Expected.Version != want.Version {
		return want, fmt.Errorf("report edge version differs from independently computed version")
	}
	if r.Pack.RepositoryURL != "https://"+m.Repos[o.Member].Fork {
		return want, fmt.Errorf("report repository differs from seed fork")
	}
	return want, nil
}

// This is a text-only allowed set, not an MSBuild packability prediction.
// Unknown SDK/dynamic metadata can leave extra allowed ids; the packed ids
// must be a non-empty subset, and no report can expand this authority.
func stackEdgeCIIsPackable(root, manifest string) (bool, error) {
	path := filepath.Join(root, manifest)
	var files []string
	for dir := filepath.Dir(path); insideRoot(root, dir); dir = filepath.Dir(dir) {
		props := filepath.Join(dir, "Directory.Build.props")
		if _, err := os.Stat(props); err == nil {
			files = append(files, props)
			break // MSBuild automatically imports only the nearest props file.
		} else if !os.IsNotExist(err) {
			return false, err
		}
		if dir == root {
			break
		}
	}
	files = append(files, path)
	for _, path := range files {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		d := xml.NewDecoder(strings.NewReader(string(b)))
		for {
			tok, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return false, err
			}
			el, ok := tok.(xml.StartElement)
			if !ok {
				continue
			}
			if el.Name.Local == "IsPackable" || el.Name.Local == "IsTestProject" {
				var value string
				if err = d.DecodeElement(&value, &el); err != nil {
					return false, err
				}
				value = strings.TrimSpace(value)
				if el.Name.Local == "IsPackable" && strings.EqualFold(value, "false") || el.Name.Local == "IsTestProject" && strings.EqualFold(value, "true") {
					return false, nil
				}
			}
		}
	}
	return true, nil
}
