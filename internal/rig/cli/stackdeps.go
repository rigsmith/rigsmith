package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Dependencies reference proposal members within the same topic.
// Cursor checks use upstream history, never checkout ancestry.
type stackDepsAPI struct {
	base, token string
	client      *http.Client
}

type stackDepsHTTPError struct{ status int }

func (e *stackDepsHTTPError) Error() string {
	return fmt.Sprintf("GitHub API HTTP %d (repository or permission may be unavailable)", e.status)
}

type stackDepsPR struct {
	Mergeable *bool   `json:"mergeable"`
	Number    int     `json:"number"`
	State     string  `json:"state"`
	MergedAt  *string `json:"merged_at"`
	MergeSHA  string  `json:"merge_commit_sha"`
	Head      struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

var stackDepsAuthForURL = stackAuthForURL

func stackDepsClient() stackDepsAPI {
	base := os.Getenv("GITHUB_API_URL")
	if base == "" {
		base = "https://api.github.com"
	}
	return stackDepsAPI{strings.TrimRight(base, "/"), os.Getenv("GITHUB_TOKEN"), &http.Client{Timeout: 15 * time.Second}}
}
func (a stackDepsAPI) request(ctx context.Context, method, path string, body, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, &b)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	token := a.token
	if token == "" {
		// Reuse stack's gh-first, git-helper-second credential lookup, lazily:
		// manifests without dependencies never consult credentials or the network.
		if u, e := url.Parse(a.base); e == nil && u.Scheme == "https" {
			host := u.Host
			if host == "api.github.com" {
				host = "github.com"
			}
			if auth := stackDepsAuthForURL(ctx, "https://"+host+"/"); auth != nil {
				token = auth.Password
			}
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub API unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &stackDepsHTTPError{status: resp.StatusCode}
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(result)
	}
	return nil
}
func (a stackDepsAPI) proposal(ctx context.Context, r *stackRepo, p stackProposal) (stackDepsPR, error) {
	var zero stackDepsPR
	upstream := strings.SplitN(r.Upstream, "/", 2)
	fork := strings.SplitN(r.Fork, "/", 2)
	if len(upstream) != 2 || len(fork) != 2 || upstream[0] != fork[0] {
		return zero, fmt.Errorf("unsupported repository")
	}
	u, err := url.Parse(a.base)
	if err != nil {
		return zero, err
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != upstream[0] && !(upstream[0] == "github.com" && u.Hostname() == "api.github.com") {
		return zero, fmt.Errorf("API host does not match upstream")
	}
	owner := strings.Split(fork[1], "/")[0]
	var matches []stackDepsPR
	for page := 1; page <= 100; page++ {
		var prs []stackDepsPR
		path := fmt.Sprintf("/repos/%s/pulls?state=all&head=%s&per_page=100&page=%d", upstream[1], url.QueryEscape(owner+":"+p.Branch), page)
		if err = a.request(ctx, "GET", path, nil, &prs); err != nil {
			return zero, err
		}
		for _, pr := range prs {
			if pr.Head.Ref == p.Branch && pr.Head.Repo.FullName == fork[1] {
				matches = append(matches, pr)
			}
		}
		if len(matches) > 1 {
			return zero, fmt.Errorf("ambiguous reused proposal branch")
		}
		if len(prs) < 100 {
			if len(matches) == 1 {
				var pr stackDepsPR
				err = a.request(ctx, "GET", fmt.Sprintf("/repos/%s/pulls/%d", upstream[1], matches[0].Number), nil, &pr)
				return pr, err
			}
			break
		}
	}
	return zero, fmt.Errorf("no PR found for proposal branch")
}

func (a stackDepsAPI) pullRequest(ctx context.Context, r *stackRepo, number int) (stackDepsPR, error) {
	var pr stackDepsPR
	host, slug, ok := strings.Cut(r.Upstream, "/")
	u, err := url.Parse(a.base)
	if !ok || err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != host && !(host == "github.com" && u.Hostname() == "api.github.com")) {
		return pr, fmt.Errorf("API host does not match upstream")
	}
	if number <= 0 {
		return pr, fmt.Errorf("invalid PR number")
	}
	err = a.request(ctx, "GET", fmt.Sprintf("/repos/%s/pulls/%d", slug, number), nil, &pr)
	return pr, err
}
func (a stackDepsAPI) dependency(ctx context.Context, r *stackRepo, p stackProposal, cursor string) (string, bool) {
	pr, err := a.proposal(ctx, r, p)
	if err != nil {
		return "could not check: " + err.Error(), false
	}
	slug := strings.SplitN(r.Upstream, "/", 2)[1]
	label := fmt.Sprintf("%s#%d", slug, pr.Number)
	if pr.MergedAt == nil {
		return label + " (" + pr.State + ")", false
	}
	if !stackIsSHA(pr.MergeSHA) || !stackIsSHA(cursor) {
		return label + " (merged; could not check cursor)", false
	}
	var comparison struct {
		Status string `json:"status"`
	}
	err = a.request(ctx, "GET", "/repos/"+slug+"/compare/"+pr.MergeSHA+"..."+cursor, nil, &comparison)
	if err != nil {
		return label + " (merged; could not check cursor)", false
	}
	if comparison.Status != "ahead" && comparison.Status != "identical" {
		return label + " (merged; cursor does not include merge)", false
	}
	return label + " (merged; included in cursor)", true
}
func stackDepsReport(ctx context.Context, m *stackManifest, a stackDepsAPI, out io.Writer, publish bool, releases ...stackReleaseAnnotation) {
	publishRepo := os.Getenv("GITHUB_REPOSITORY")
	if publish {
		matched := false
		for _, r := range m.Repos {
			_, slug, ok := strings.Cut(r.Upstream, "/")
			if ok && publishRepo != "" && strings.EqualFold(slug, publishRepo) {
				matched = true
			}
		}
		if !matched {
			fmt.Fprintln(out, "::warning::Refusing to publish stack dependencies: GITHUB_REPOSITORY must name a member upstream repository.")
			publish = false
		}
	}
	topics := map[string]bool{}
	for _, ps := range m.Proposals {
		for topic := range ps {
			topics[topic] = true
		}
	}
	names := make([]string, 0, len(topics))
	for t := range topics {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, topic := range names {
		for _, member := range m.names() {
			target, ok := m.Proposals[member][topic]
			if !ok {
				continue
			}
			state := "success"
			var lines []string
			for _, dep := range target.DependsOn {
				p, ok := m.Proposals[dep][topic]
				if !ok || m.Repos[dep] == nil {
					state = "pending"
					lines = append(lines, "could not check missing proposal: "+dep)
					continue
				}
				text, ready := a.dependency(ctx, m.Repos[dep], p, m.cursor(dep))
				if ready && len(releases) > 0 {
					text += releases[0](member, dep)
				}
				if !ready {
					state = "pending"
				}
				lines = append(lines, "depends on "+text)
			}
			if len(lines) == 0 {
				continue
			}
			description := strings.Join(lines, "; ")
			fmt.Fprintf(out, "%s / %s: %s\n", topic, member, description)
			_, memberRepo, _ := strings.Cut(m.Repos[member].Upstream, "/")
			if publish && strings.EqualFold(memberRepo, publishRepo) {
				pr, err := a.proposal(ctx, m.Repos[member], target)
				if err == nil && stackIsSHA(pr.Head.SHA) {
					slug := strings.SplitN(m.Repos[member].Upstream, "/", 2)[1]
					short := stackDepsDescription(description)
					err = a.request(ctx, "POST", "/repos/"+slug+"/statuses/"+pr.Head.SHA, map[string]string{"state": state, "context": "stack dependencies", "description": short}, nil)
				} else if err == nil {
					err = fmt.Errorf("missing PR head SHA")
				}
				if err != nil {
					fmt.Fprintln(out, "::warning::Could not publish stack dependencies status; token may be read-only. See job summary.")
					fmt.Fprintf(out, "Status publication unavailable for %s: %v\n", member, err)
				}
			}
		}
	}
}
func stackStatusDependencies(ctx context.Context, m *stackManifest, out io.Writer, releases ...bool) {
	// One deadline for the entire optional report, including credential helpers.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	a := stackDepsClient()
	if len(releases) > 0 && releases[0] {
		annotate := stackReleaseAnnotations(ctx, m, a, stackNugetFeed, func(ctx context.Context) ([]stackLink, error) {
			root, err := stackRoot(ctx)
			if err != nil {
				return nil, err
			}
			return stackReleaseAllLinks(ctx, root, m)
		})
		stackDepsReport(ctx, m, a, out, false, annotate)
	} else {
		stackDepsReport(ctx, m, a, out, false)
	}
}

func stackDepsDescription(s string) string {
	runes := []rune(s)
	if len(runes) > 140 {
		return string(runes[:137]) + "..."
	}
	return s
}

func newStackDepsCmd() *cobra.Command {
	var manifest, seed, allowedHosts string
	var publish bool
	c := &cobra.Command{Use: "deps", Short: "Check topic PR dependencies against upstream cursors", Long: "--dry-run is not supported; this command refuses it before making changes.\n\nReads committed proposal records from --manifest (default: rig.stack.jsonc) or --seed, resolves PRs through the GitHub API, and\nchecks API merge commits against upstream cursors. Uses GITHUB_TOKEN (or local gh/git credentials) and\nGITHUB_API_URL. --publish posts only to GITHUB_REPOSITORY PR head SHAs; denied writes warn.\nPending and unavailable dependencies remain non-green. Rerun after merges\nand committed cursor bumps; no automatic sweep is installed.", Args: cobra.NoArgs}
	c.Flags().StringVar(&manifest, "manifest", "rig.stack.jsonc", "Committed manifest path")
	c.Flags().StringVar(&seed, "seed", "", "Manifest seed; exclusive with --manifest")
	c.Flags().StringVar(&allowedHosts, "allowed-hosts", "github.com", "Comma-separated allowed seed hosts")
	_ = c.RegisterFlagCompletionFunc("seed", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
	c.Flags().BoolVar(&publish, "publish", false, "Publish commit statuses (optional statuses: write permission)")
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if dryRun {
			return fmt.Errorf("--dry-run is not supported for stack deps")
		}
		root, err := stackRoot(cmd.Context())
		if err != nil {
			return err
		}
		if cmd.Flags().Changed("seed") && (seed == "" || cmd.Flags().Changed("manifest")) {
			return fmt.Errorf("--seed requires a reference and is exclusive with --manifest")
		}
		temp, err := os.MkdirTemp("", "rig-stack-deps-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temp)
		var data []byte
		if seed != "" {
			data, _, err = stackCISeed(cmd.Context(), root, filepath.Join(temp, "seed"), seed, allowedHosts)
		} else {
			p := manifest
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			rel, e := filepath.Rel(root, p)
			if e != nil {
				return e
			}
			data, err = stackCIGit(cmd.Context(), root, "show", "HEAD:"+filepath.ToSlash(rel))
		}
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(temp, "rig.stack.jsonc"), data, 0600); err != nil {
			return err
		}
		m, _, err := loadStackManifest(temp)
		if err != nil {
			return err
		}
		if m == nil {
			return fmt.Errorf("no stack manifest")
		}
		var b bytes.Buffer
		stackDepsReport(cmd.Context(), m, stackDepsClient(), &b, publish)
		fmt.Fprint(cmd.OutOrStdout(), b.String())
		if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
			f, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			defer f.Close()
			_, err = fmt.Fprint(f, "\n## Stack dependencies\n\n```text\n", b.String(), "```\n")
		}
		return err
	}
	return c
}
