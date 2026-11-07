package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

func newStackEdgePackCmd() *cobra.Command {
	var out, manifest, seed, allowedHosts string
	var here, dryRun, upstreamTip bool
	var number uint64
	cmd := &cobra.Command{
		Use: "pack <member>", Short: "Rename, version, pack and verify a member's edge packages",
		Long: "Reconstructs the committed stackspace manifest (or --manifest / --seed),\n" +
			"then packs the member's fork trackBranch under its publishing ids.\n" +
			"Checks the observed NuGet packages and preserves assembly names.\n" +
			"--upstream-tip merges onto current upstream; conflicts report inconclusive\n" +
			"with commits and a remedy, replace --out with only edge-pack.json, and skip packing.\n" +
			"--here uses the working tree instead;\n" +
			"its output can never be published, even with a nonzero --number.\n" +
			"Writes verified packages and edge-pack.json to --out; never pushes a feed.\n" +
			"--dry-run discovers ids and versions without building, writing or deleting output.",
		Args: cobra.ExactArgs(1), ValidArgsFunction: stackEdgeMemberCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if here && (seed != "" || manifest != "" || upstreamTip) {
				return fmt.Errorf("--here cannot be combined with --manifest, --seed or --upstream-tip")
			}
			if manifest != "" && seed != "" {
				return fmt.Errorf("--manifest and --seed are mutually exclusive")
			}
			ctx := cmd.Context()
			checkout, err := stackRoot(ctx)
			if err != nil {
				return err
			}
			dir := out
			if dir == "" {
				dir = filepath.Join(checkout, stackPackDefaultOut)
			}
			dir, err = filepath.Abs(dir)
			if err != nil {
				return err
			}
			root := checkout
			opts := stackEdgePackOptions{OutputDir: dir, Number: number, DryRun: dryRun, Here: here}
			if here {
				_, _, repo, err := stackspace(ctx)
				if err != nil {
					return err
				}
				opts.Commit, err = repo.Head(ctx)
				if err != nil {
					return err
				}
			} else {
				rebuilt, cleanup, err := stackEdgeReconstruct(ctx, stackEdgeReconstructOptions{
					Checkout: checkout, Manifest: manifest, Seed: seed, Members: args,
					AllowedHosts: allowedHosts, UpstreamTip: upstreamTip,
				})
				if err != nil {
					var inconclusive *stackEdgeInconclusiveError
					if errors.As(err, &inconclusive) {
						result := stackEdgePackResult{
							Status: "inconclusive", Member: inconclusive.Member, Remedy: inconclusive.Remedy(),
							Commit:     inconclusive.Provenance.TrackCommit,
							Provenance: map[string]stackEdgeProvenance{inconclusive.Member: inconclusive.Provenance},
						}
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (track: %s; upstream tip: %s): %s\n", result.Member, result.Status, result.Commit, inconclusive.Provenance.UpstreamTip, result.Remedy)
						if dryRun {
							return nil
						}
						if err := stackEdgeClearOutput(ctx, checkout, dir); err != nil {
							return err
						}
						return stackEdgeWritePackReport(dir, result)
					}
					return err
				}
				defer cleanup()
				root, opts.Provenance = rebuilt.Root, rebuilt.Provenance
				opts.Commit = rebuilt.Provenance[args[0]].TrackCommit
			}
			m, _, err := loadStackManifest(root)
			if err != nil {
				return err
			}
			if m.Repos[args[0]] == nil {
				return fmt.Errorf("unknown stack member %q", args[0])
			}
			_, err = stackEdgePackTree(ctx, cmd.OutOrStdout(), m, root, args[0], opts)
			return err
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "output directory (default dist/ at the stackspace root)")
	cmd.Flags().Uint64Var(&number, "number", 0, "edge run number (0 is unpublishable)")
	cmd.Flags().BoolVar(&here, "here", false, "pack the working tree; output is never publishable")
	cmd.Flags().StringVar(&manifest, "manifest", "", "committed manifest path (default: the stackspace's own manifest)")
	cmd.Flags().StringVar(&seed, "seed", "", "manifest seed (owner/repo@ref, URL or local path); exclusive with --manifest")
	cmd.Flags().StringVar(&allowedHosts, "allowed-hosts", "github.com", "comma-separated allowed fork/upstream and seed hosts")
	cmd.Flags().BoolVar(&upstreamTip, "upstream-tip", false, "merge trackBranch onto upstream tip; conflicts are inconclusive")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show ids and versions without building")
	for _, flag := range []string{"seed", "allowed-hosts", "number"} {
		_ = cmd.RegisterFlagCompletionFunc(flag, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		})
	}
	return cmd
}

// stackEdgePackTree takes a supplied fused tree. Reconstruction and CI can call
// it with their temporary root and the actual fork commit, without involving
// the working checkout or changing the packaging and verification rules.
type stackEdgePackOptions struct {
	OutputDir  string
	Number     uint64
	Commit     string
	Here       bool
	DryRun     bool
	Provenance map[string]stackEdgeProvenance
	// PublicFeed and Client allow hermetic feed fixtures. CLI uses nuget.org.
	PublicFeed string
	Client     *http.Client
}

type stackEdgePackResult struct {
	Status        string `json:"status"` // planned, packed, or inconclusive
	Remedy        string `json:"remedy,omitempty"`
	Member        string
	Version       string
	RepositoryURL string
	Commit        string
	Here          bool
	Publishable   bool
	Provenance    map[string]stackEdgeProvenance `json:",omitempty"`
	Expected      stackEdgeVerifyRequest
	Artifacts     []stackEdgeVerifiedPackage
}

var stackEdgePackageID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func stackEdgeResolveIDs(pkgs []stackPackPackage, pub stackPublishing, exclude []string) (map[string]string, error) {
	ids := map[string]string{}
	used := map[string]string{}
	originals := map[string]bool{}
	for _, p := range pkgs {
		originals[strings.ToLower(p.pkg.Name)] = true
	}
	for _, p := range pkgs {
		id := p.pkg.Name
		if slices.ContainsFunc(exclude, func(e string) bool { return strings.EqualFold(e, id) }) {
			continue
		}
		as, ok := stackRepublishedID(pub, id)
		if !ok || !stackEdgePackageID.MatchString(id) || !stackEdgePackageID.MatchString(as) || originals[strings.ToLower(as)] {
			return nil, fmt.Errorf("%s needs a distinct republished id via publishPrefix or publishesAs; edge never publishes an original id", id)
		}
		key := strings.ToLower(as)
		if prev := used[key]; prev != "" {
			return nil, fmt.Errorf("%s and %s resolve to the same republished id %s", prev, id, as)
		}
		used[key], ids[id] = id, as
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no non-excluded packages to pack")
	}
	return ids, nil
}

func stackEdgePublicVersion(ctx context.Context, client *http.Client, feed, id string) (string, error) {
	var index struct {
		Versions []string `json:"versions"`
	}
	path := strings.TrimRight(feed, "/") + "/" + url.PathEscape(strings.ToLower(id)) + "/index.json"
	if err := stackReleaseGet(ctx, client, path, &index, false); err != nil {
		var status *stackReleaseHTTPError
		if errors.As(err, &status) && status.StatusCode == http.StatusNotFound {
			return "", nil // the public flat-container index has no such package
		}
		return "", fmt.Errorf("public version of %s: %w", id, err)
	}
	versions, err := stackReleaseCandidates(index.Versions)
	if err != nil {
		return "", fmt.Errorf("public version of %s: %w", id, err)
	}
	if len(versions) == 0 {
		return "", nil
	}
	return versions[0], nil
}

func stackEdgePackTree(ctx context.Context, out io.Writer, m *stackManifest, root, member string, opts stackEdgePackOptions) (stackEdgePackResult, error) {
	result := stackEdgePackResult{Member: member, Commit: opts.Commit, Here: opts.Here, Provenance: opts.Provenance}
	// The working checkout can be symlinked too. Keep discovery, evaluated
	// project paths and NuGet's restore/pack inputs on the same absolute root.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return result, err
	}
	e, r := m.Edge[member], m.Repos[member]
	if e == nil || r == nil {
		return result, fmt.Errorf("%s needs a configured member and edge block", member)
	}
	if r.Fork == "" || !stackIsSHA(opts.Commit) {
		return result, fmt.Errorf("edge pack needs the member's PR fork and the full commit being built")
	}
	result.RepositoryURL = "https://" + r.Fork
	maxSize, err := e.effectiveMaxSize()
	if err != nil {
		return result, err
	}
	// Registry discovery is necessary for overlay swaps, including unversioned
	// projects. A partial scan is never evidence that no dependency crosses.
	scans := stackScan(ctx, root, stackScanOptions{Registry: true, IncludeOverlays: true})
	var all []stackPackPackage
	for _, scan := range scans {
		if scan.Err != nil {
			return result, scan.Err
		}
		for _, pkg := range scan.Packages {
			all = append(all, stackPackPackage{eco: scan.Eco, info: scan.Eco.Info(), pkg: pkg})
		}
	}
	all = stackPackReconcileOverlays(all)
	pkgs := slices.DeleteFunc(stackPackFor(all, member), func(p stackPackPackage) bool { return p.pkg.Private })
	slices.SortFunc(pkgs, func(a, b stackPackPackage) int { return strings.Compare(a.pkg.Name, b.pkg.Name) })
	metadata := map[string]dotnet.PackMetadata{}
	// Static discovery includes test projects and cannot resolve conditional or
	// central PackageReference versions. Evaluate the unmodified member first.
	// Dry-run stays toolchain-free and reports the static discovery plan.
	if !opts.DryRun {
		for _, p := range stackPackFor(all, member) {
			if p.info.ID != "dotnet" {
				continue
			}
			meta, err := dotnet.EvaluatePackMetadata(ctx, root, p.pkg.ManifestPath, nil)
			if err != nil {
				return result, fmt.Errorf("discovering pack metadata for %s: %w", p.pkg.Name, err)
			}
			metadata[filepath.Join(root, p.pkg.ManifestPath)] = meta
		}
		pkgs = slices.DeleteFunc(pkgs, func(p stackPackPackage) bool {
			return p.info.ID == "dotnet" && !metadata[filepath.Join(root, p.pkg.ManifestPath)].Packable
		})
	}
	var produced []string
	for _, p := range pkgs {
		produced = append(produced, p.pkg.Name)
	}
	for _, note := range e.excludeNotes(member, produced) {
		fmt.Fprintln(out, note)
	}
	ids, err := stackEdgeResolveIDs(pkgs, m.publishing()[member], e.Exclude)
	if err != nil {
		return result, err
	}
	links, _, _, failed := stackRedirectsFromScan(scans, m.names(), m.publishing())
	if len(failed) != 0 {
		return result, fmt.Errorf("cannot discover overlay dependencies")
	}
	for _, links := range links {
		for _, link := range links {
			if link.To != member && slices.Contains(link.From, member) {
				return result, fmt.Errorf("%s depends through an overlay swap on %s from member %s; cross-member edge dependencies are unsupported", member, link.Package, link.To)
			}
		}
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if opts.PublicFeed == "" {
		opts.PublicFeed = stackNugetFeed
	}
	// A member shares one version. Take the highest R/T result so it is above
	// every public release, including members whose projects have different T.
	for _, p := range pkgs {
		if ids[p.pkg.Name] == "" {
			continue
		}
		if p.info.ID != "dotnet" || !slices.Contains(p.info.Capabilities, plugin.CapabilityArtifactsProperties) {
			return result, fmt.Errorf("%s adapter must support %s and .NET pack verification", p.info.ID, plugin.CapabilityArtifactsProperties)
		}
		released, err := stackEdgePublicVersion(ctx, opts.Client, opts.PublicFeed, p.pkg.Name)
		if err != nil {
			return result, err
		}
		version, err := stackEdgeComputeVersion(released, p.pkg.Version, e.Label, opts.Number)
		if err != nil {
			return result, err
		}
		if result.Version == "" {
			result.Version = version.Version
		} else {
			a, _ := stackParseNugetVersion(result.Version)
			b, _ := stackParseNugetVersion(version.Version)
			if stackCompareNuget(b, a) > 0 {
				result.Version = version.Version
			}
		}
	}
	result.Publishable = opts.Number != 0 && !opts.Here
	fmt.Fprintf(out, "%s: edge %s (publishable: %t; here: %t)\n", member, result.Version, result.Publishable, opts.Here)
	if opts.DryRun {
		result.Status = "planned"
		for _, p := range pkgs {
			if as := ids[p.pkg.Name]; as != "" {
				fmt.Fprintf(out, "would pack %s as %s@%s\n", p.pkg.Name, as, result.Version)
			}
		}
		return result, nil
	}
	properties := map[string]string{"RepositoryUrl": result.RepositoryURL, "RepositoryCommit": opts.Commit, "Version": result.Version}
	paths := map[string]string{}
	for _, p := range all {
		paths[filepath.Clean(filepath.Join(root, p.pkg.ManifestPath))] = p.pkg.Name
	}
	hook, cleanup, err := stackEdgeWriteHook(ids, metadata)
	if err != nil {
		return result, err
	}
	defer cleanup()
	properties["CustomAfterMicrosoftCommonTargets"] = hook
	request := stackEdgeVerifyRequest{Version: result.Version, MemberIDs: map[string]string{}, MaxSize: maxSize}
	for _, p := range pkgs {
		request.MemberIDs[p.pkg.Name] = ids[p.pkg.Name]
	}
	for _, name := range m.names() {
		if name == member {
			continue
		}
		for _, p := range stackPackFor(all, name) {
			request.OtherMemberIDs = append(request.OtherMemberIDs, p.pkg.Name)
			if as, ok := stackRepublishedID(m.publishing()[name], p.pkg.Name); ok {
				request.OtherMemberIDs = append(request.OtherMemberIDs, as)
			}
		}
	}
	stage, err := os.MkdirTemp("", "rig-stack-edge-pack-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(stage)
	for _, p := range pkgs {
		as := ids[p.pkg.Name]
		if as == "" {
			continue
		}
		discovered := metadata[filepath.Join(root, p.pkg.ManifestPath)]
		before, err := dotnet.EvaluatePackMetadata(ctx, root, p.pkg.ManifestPath, map[string]string{
			"RepositoryUrl": result.RepositoryURL, "RepositoryCommit": opts.Commit, "Version": result.Version,
		})
		if err != nil {
			return result, err
		}
		after, err := dotnet.EvaluatePackMetadata(ctx, root, p.pkg.ManifestPath, properties)
		if err != nil {
			return result, err
		}
		expected := stackEdgeExpectedPackage{OriginalID: p.pkg.Name, ID: as, AssemblyBefore: before.AssemblyName, AssemblyAfter: after.AssemblyName}
		if before.AssemblyName != after.AssemblyName {
			return result, fmt.Errorf("%s: AssemblyName changed from %q to %q", as, before.AssemblyName, after.AssemblyName)
		}
		for _, dep := range discovered.Dependencies {
			id, version := dep.ID, dep.Version
			if dep.ProjectPath != "" {
				id = paths[dep.ProjectPath]
				if id == "" {
					return result, fmt.Errorf("%s: cannot discover dependency %s", as, dep.ProjectPath)
				}
			}
			if slices.ContainsFunc(request.OtherMemberIDs, func(other string) bool { return strings.EqualFold(id, other) }) {
				return result, fmt.Errorf("%s depends on another member's package %s", as, id)
			}
			for original, alias := range ids {
				if strings.EqualFold(id, original) || strings.EqualFold(id, alias) {
					id, version = alias, result.Version
					break
				}
			}
			if version == "" {
				return result, fmt.Errorf("%s: no discovered version for dependency %s", as, id)
			}
			expected.Dependencies = append(expected.Dependencies, stackEdgeDependency{ID: id, Version: version, Framework: dep.Framework})
		}
		request.Packages = append(request.Packages, expected)
		pkg := p.pkg
		pkg.Version = result.Version
		packProperties := map[string]string{"CustomAfterMicrosoftCommonTargets": hook, "RepositoryUrl": result.RepositoryURL, "RepositoryCommit": opts.Commit}
		resp, err := p.eco.Artifacts(ctx, plugin.ArtifactsRequest{RepoRoot: root, Package: pkg, OutputDir: stage, Properties: packProperties})
		if err != nil {
			return result, err
		}
		if !resp.Built || resp.Skipped {
			return result, fmt.Errorf("%s was not packed: %s", as, resp.Message)
		}
	}
	result.Expected = request
	result.Artifacts, err = stackEdgeVerify(stage, request)
	for _, artifact := range result.Artifacts {
		fmt.Fprintf(out, "%s: %d bytes\n", artifact.File, artifact.Size)
	}
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return result, err
	}
	for _, artifact := range result.Artifacts {
		destination := filepath.Join(opts.OutputDir, filepath.FromSlash(artifact.File))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return result, err
		}
		data, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(artifact.File)))
		if err != nil {
			return result, err
		}
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			return result, err
		}
	}
	result.Status = "packed"
	return result, stackEdgeWritePackReport(opts.OutputDir, result)
}

func stackEdgeWritePackReport(dir string, result stackEdgePackResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "edge-pack.json"), append(data, '\n'), 0o644)
}

// An inconclusive run must not leave earlier artifacts beside its report.
// Refuse source directories before clearing a user-selected output path.
func stackEdgeClearOutput(ctx context.Context, checkout, dir string) error {
	resolved, err := filepath.EvalSymlinks(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, part := range strings.Split(filepath.ToSlash(resolved), "/") {
		if strings.EqualFold(part, ".git") {
			return fmt.Errorf("refusing to clear --out inside git metadata")
		}
	}
	root, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(resolved, root); err != nil || rel == "." || filepath.IsLocal(rel) {
		return fmt.Errorf("refusing to clear --out containing the stackspace root")
	}
	if rel, err := filepath.Rel(root, resolved); err == nil && filepath.IsLocal(rel) {
		files, err := stackCIGit(ctx, root, "ls-files", "-z", "--", filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if len(files) != 0 {
			return fmt.Errorf("refusing to clear --out containing tracked source files")
		}
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(resolved, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
