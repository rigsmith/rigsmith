package cli

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/ecosystem/dotnet"
	"github.com/spf13/cobra"
)

const stackNugetFeed = "https://api.nuget.org/v3-flatcontainer"

// A flat-container feed is explicit: failed private feeds never mean unpublished.
func stackReleaseGet(ctx context.Context, client *http.Client, path string, result any, isXML bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("can't check feed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("can't check feed: HTTP %d", resp.StatusCode)
	}
	r := io.LimitReader(resp.Body, 2<<20)
	if isXML {
		return xml.NewDecoder(r).Decode(result)
	}
	return json.NewDecoder(r).Decode(result)
}

func stackReleaseVersion(ctx context.Context, a stackDepsAPI, m *stackManifest, sibling, pkg, version, feed string) (string, error) {
	return stackReleaseVersionProof(ctx, a, m, sibling, pkg, version, feed, nil)
}

func stackReleaseVersionProof(ctx context.Context, a stackDepsAPI, m *stackManifest, sibling, pkg, version, feed string, commit *string) (string, error) {
	r := m.Repos[sibling]
	if r == nil {
		return "", fmt.Errorf("unknown sibling %s", sibling)
	}
	var commits []string
	for _, p := range m.Proposals[sibling] {
		pr, err := a.proposal(ctx, r, p)
		if err != nil {
			return "", fmt.Errorf("can't check proposal: %w", err)
		}
		// Open proposals also block: guards carry no topic identity.
		if pr.MergedAt == nil {
			return "", fmt.Errorf("not yet released: recorded proposal is unmerged")
		}
		if !stackIsSHA(pr.MergeSHA) {
			return "", fmt.Errorf("can't tell: missing upstream merge commit")
		}
		commits = append(commits, pr.MergeSHA)
	}
	if len(commits) == 0 {
		return "", fmt.Errorf("can't tell: no recorded proposals")
	}
	if !stackIsSHA(m.cursor(sibling)) {
		return "", fmt.Errorf("can't tell: missing upstream cursor")
	}
	commits = append(commits, m.cursor(sibling))
	base := strings.TrimRight(feed, "/") + "/" + url.PathEscape(strings.ToLower(pkg))
	versions := []string{version}
	if version == "" {
		var index struct {
			Versions []string `json:"versions"`
		}
		if err := stackReleaseGet(ctx, a.client, base+"/index.json", &index, false); err != nil {
			return "", err
		}
		var err error
		versions, err = stackReleaseCandidates(index.Versions)
		if err != nil {
			return "", err
		}
	}
	unknown := false
	for _, v := range versions {
		parsed, ok := stackParseNugetVersion(v)
		if !ok {
			return "", fmt.Errorf("invalid version")
		}
		v = parsed.normalized()
		var spec struct {
			Metadata struct {
				Repository struct {
					URL    string `xml:"url,attr"`
					Commit string `xml:"commit,attr"`
				} `xml:"repository"`
			} `xml:"metadata"`
		}
		if err := stackReleaseGet(ctx, a.client, base+"/"+url.PathEscape(strings.ToLower(v))+"/"+url.PathEscape(strings.ToLower(pkg))+".nuspec", &spec, true); err != nil {
			return "", err
		}
		repo := spec.Metadata.Repository
		normalized := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(repo.URL, "https://"), "/"), ".git")
		if !stackIsSHA(repo.Commit) || !strings.EqualFold(normalized, r.Upstream) {
			unknown = true
			continue
		}
		_, slug, _ := strings.Cut(r.Upstream, "/")
		included := true
		for _, sha := range commits {
			var comparison struct {
				Status string `json:"status"`
			}
			if err := a.request(ctx, "GET", "/repos/"+slug+"/compare/"+sha+"..."+repo.Commit, nil, &comparison); err != nil {
				return "", fmt.Errorf("can't check release ancestry: %w", err)
			}
			if comparison.Status != "ahead" && comparison.Status != "identical" {
				included = false
				break
			}
		}
		if included {
			if commit != nil {
				*commit = repo.Commit
			}
			return v, nil
		}
	}
	if unknown {
		return "", fmt.Errorf("can't tell: missing repository commit or matching repository URL")
	}
	return "", fmt.Errorf("not yet released in the checked versions (at most 20 stable versions)")
}

func newStackReleasedCmd() *cobra.Command {
	var pkg, version, feed, via, viaVersion, report, publish, seed string
	var apply bool
	var warnings []string
	c := &cobra.Command{
		Use:   "released <member> <sibling>",
		Short: "Check NuGet release ancestry and prepare a return to packages",
		Long:  "Checks all recorded sibling proposals and its upstream cursor against NuGet repository commit metadata.\nMissing metadata reports can't tell. --package selects the sibling package; --version selects a release\n(otherwise checks up to 20 newest stable versions). --feed is a NuGet flat-container base URL.\n--apply requires a clean tree and interactive confirmation. Transitive siblings require a published direct package dependency minimum in every framework group.\n--via-package/--via-version select that delivery release. Complex guards and project conditions\nare listed for manual review. --warning names declarations explicitly attributed to this package. -n previews only.\n--report writes read-only JSON (not yet released exits successfully). --seed reloads a trusted manifest home\nand member default branches. --publish verifies that report independently and proposes a PR, running no repository code.\nPublish requires --seed and trusted --package, --feed, --version, --via-* and --warning selections matching the report.",
		Args: func(cmd *cobra.Command, args []string) error {
			if publish != "" && len(args) == 0 {
				return nil
			}
			return cobra.ExactArgs(2)(cmd, args)
		},
	}
	c.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		root, err := stackRoot(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		m, _, err := loadStackManifest(root)
		if err != nil || m == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return m.names(), cobra.ShellCompDirectiveNoFileComp
	}
	c.Flags().StringVar(&pkg, "package", "", "Sibling NuGet package identity (required)")
	c.Flags().StringVar(&via, "via-package", "", "Direct delivery package for a transitive sibling")
	c.Flags().StringVar(&viaVersion, "via-version", "", "Delivery package release (otherwise checks 20 newest stable versions)")
	c.Flags().StringVar(&version, "version", "", "Published version to check")
	c.Flags().StringVar(&feed, "feed", stackNugetFeed, "NuGet flat-container base URL")
	c.Flags().StringSliceVar(&warnings, "warning", nil, "Explicit warning IDs belonging to this package to remove")
	c.Flags().BoolVar(&apply, "apply", false, "Apply reviewed edits locally (interactive, clean tree only)")
	c.Flags().StringVar(&report, "report", "", "Write a read-only release report")
	c.Flags().StringVar(&publish, "publish", "", "Verify and publish a release report as a review PR")
	c.Flags().StringVar(&seed, "seed", "", "Trusted manifest home (owner/repo@branch) for scheduled reports/publication")
	for _, name := range []string{"package", "via-package", "via-version", "version", "feed", "warning", "seed"} {
		_ = c.RegisterFlagCompletionFunc(name, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		})
	}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if publish != "" || report != "" {
			if apply || publish != "" && report != "" {
				return fmt.Errorf("--report, --publish and --apply are mutually exclusive")
			}
			return stackReleasedReportCommand(cmd, args, stackReleasedOptions{Package: pkg, Version: version, Feed: feed, Via: via, ViaVersion: viaVersion, Warnings: warnings}, seed, report, publish)
		}
		if seed != "" {
			return fmt.Errorf("--seed requires --report or --publish")
		}

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
		if m.Repos[args[0]] == nil || m.Repos[args[1]] == nil || args[0] == args[1] {
			return fmt.Errorf("name distinct manifest members")
		}
		if pkg == "" {
			return fmt.Errorf("--package is required")
		}
		links, err := stackReleaseAllLinks(cmd.Context(), root, m)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
		defer cancel()
		v, err := stackReleaseVersion(ctx, stackDepsClient(), m, args[1], pkg, version, feed)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: released in %s (all recorded proposals and upstream cursor checked)\n", pkg, v)
		symbol := "STACK_" + strings.ToUpper(dotnet.StackSourceName(pkg))
		pin, pinVersion, delivery, err := stackReleaseDelivery(ctx, stackDepsClient(), links, args[0], args[1], pkg, v, via, viaVersion, feed)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), delivery)
		edits, notes, err := stackReleaseMigrationEdits(filepath.Join(root, args[0]), pkg, pin, pinVersion, symbol, warnings...)
		if err != nil {
			return err
		}
		for _, note := range notes {
			fmt.Fprintln(cmd.OutOrStdout(), note)
		}
		for _, edit := range edits {
			fmt.Fprintln(cmd.OutOrStdout(), "edit:", edit.path)
		}
		if !apply || dryRun {
			return nil
		}
		dirty, err := stackCIGit(cmd.Context(), root, "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return err
		}
		if len(dirty) > 0 {
			return fmt.Errorf("--apply refuses a dirty tree")
		}
		if !stdinStdoutTTY() {
			return fmt.Errorf("--apply requires interactive confirmation")
		}
		yes := false
		if err = huh.NewConfirm().Title("Apply these destructive package and guard edits?").Value(&yes).Run(); err != nil {
			return err
		}
		if !yes {
			return nil
		}
		return stackReleaseWrite(edits)
	}
	return c
}
