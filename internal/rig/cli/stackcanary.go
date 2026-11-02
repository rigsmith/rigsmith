package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type stackCanaryChange struct {
	Member, From, To string
	Commits          []string
}
type stackCanaryResult struct {
	Changes  []stackCanaryChange
	Green    bool
	Batch    bool
	Failures []string
}
type stackCanaryReport struct {
	Base    string
	Results []stackCanaryResult
}

func newStackCanaryCmd() *cobra.Command {
	var seed, members, build, test, report, publish, allowed, mode string
	var isolate bool
	c := &cobra.Command{Use: "canary", Short: "Test upstream tips and propose manifest cursor updates", Long: "Test moving upstream branches in an isolated stackspace and write a JSON report.\nPins and fork-tracking members stay fixed. --mode separate tests one update\nper PR; batch (default) tests all updates together. --isolate adds individual\nbuilds to attribute batch failures. No bisect is run. In a fresh trusted job,\n--publish reads the report and updates stable manifest-only PRs or issues.\nUse --seed owner/repo@branch. -n previews publication without writes.\nNever provide write credentials to the build job; use separate clean runners.", Args: cobra.NoArgs}
	f := c.Flags()
	f.StringVar(&seed, "seed", "", "Manifest home (owner/repo@branch, URL or local path)")
	f.StringVar(&members, "members", "", "Comma-separated members to follow (default: all eligible)")
	f.StringVar(&mode, "mode", "batch", "batch or separate cursor PRs")
	f.BoolVar(&isolate, "isolate", false, "Also test each changed member to attribute batch failures")
	f.StringVar(&build, "build", "rig build", "Build command from stack root (bash)")
	f.StringVar(&test, "test", "rig test", "Test command from stack root (bash)")
	f.StringVar(&report, "report", "canary.json", "Output report for a separate trusted job")
	f.StringVar(&publish, "publish", "", "Publish this report; runs no project commands")
	f.StringVar(&allowed, "allowed-hosts", "github.com", "Allowed upstream and seed hosts")
	for _, name := range []string{"seed", "members", "mode", "allowed-hosts"} {
		_ = c.RegisterFlagCompletionFunc(name, func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		})
	}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if seed == "" {
			return fmt.Errorf("--seed is required")
		}
		if mode != "batch" && mode != "separate" {
			return fmt.Errorf("--mode must be batch or separate")
		}
		ctx := cmd.Context()
		tmp, e := os.MkdirTemp("", "rig-canary-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		cwd, e := os.Getwd()
		if e != nil {
			return e
		}
		data, base, e := stackCISeed(ctx, cwd, filepath.Join(tmp, "home"), seed, allowed)
		if e != nil {
			return e
		}
		home := filepath.Join(tmp, "home")
		if e = os.WriteFile(filepath.Join(home, "rig.stack.jsonc"), data, 0600); e != nil {
			return e
		}
		m, _, e := loadStackManifest(home)
		if e != nil {
			return e
		}
		if m == nil {
			return fmt.Errorf("missing manifest")
		}
		if e = stackCheckHome(ctx, home, m); e != nil {
			return e
		}
		if e = stackCIValidate(m, allowed); e != nil {
			return e
		}
		if publish != "" {
			b, e := os.ReadFile(publish)
			if e != nil {
				return e
			}
			if len(b) > 1<<20 {
				return fmt.Errorf("report too large")
			}
			var r stackCanaryReport
			if e = json.Unmarshal(b, &r); e != nil {
				return e
			}
			if r.Base != base {
				return fmt.Errorf("seed moved; rerun canary")
			}
			// Reports cross an untrusted boundary: only valid cursor deltas are applied.
			for _, result := range r.Results {
				if len(result.Changes) == 0 {
					return fmt.Errorf("empty result")
				}
				seen := map[string]bool{}
				for _, ch := range result.Changes {
					repo := m.Repos[ch.Member]
					if repo == nil || seen[ch.Member] || m.pin(ch.Member).pinned() || repo.TrackBranch != "" || ch.From != m.cursor(ch.Member) || !stackIsSHA(ch.To) || ch.To == ch.From {
						return fmt.Errorf("invalid cursor change")
					}
					seen[ch.Member] = true
				}
			}
			for _, result := range r.Results {
				for _, n := range result.Failures {
					found := false
					for _, ch := range result.Changes {
						if n == ch.Member {
							found = true
						}
					}
					if !found {
						return fmt.Errorf("invalid attribution member")
					}
				}
			}
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "Would reconcile %d canary PR/issue result(s)\n", len(r.Results))
				return nil
			}
			if len(r.Results) == 0 {
				return nil
			}
			api := stackDepsClient()
			if api.token == "" {
				return stackCanarySummary(cmd.OutOrStdout(), "Canary report available; publication skipped (no GITHUB_TOKEN).")
			}
			remote, branch, e := stackSeedReference(seed)
			if e != nil {
				return e
			}
			spec := stackNormalizeSpec(remote)
			parts := strings.Split(spec, "/")
			if len(parts) != 3 || parts[0] != "github.com" || branch == "" {
				return fmt.Errorf("publication requires a GitHub owner/repo@branch home")
			}
			available := make([]bool, len(r.Results))
			var summary strings.Builder
			for i := range r.Results {
				available[i], e = stackCanaryValidateResult(ctx, api, m, &r.Results[i])
				if e != nil {
					return e
				}
				if !available[i] {
					fmt.Fprintf(&summary, "Canary %s: publication skipped; upstream comparison unavailable. Check publish token read access to every upstream.\n", stackCanaryMembers(r.Results[i]))
				}
			}
			// Rebuild each manifest from the original bytes; no artifact-supplied file is copied.
			for i, result := range r.Results {
				if !available[i] {
					continue
				}
				if e = os.WriteFile(filepath.Join(home, "rig.stack.jsonc"), data, 0600); e != nil {
					return e
				}
				m, src, err := loadStackManifest(home)
				if err != nil {
					return err
				}
				for _, ch := range result.Changes {
					m.LastSync[ch.Member] = ch.To
					if e = stackWriteManifestMap(src, "lastSync", m.LastSync); e != nil {
						return e
					}
				}
				updated, e := os.ReadFile(filepath.Join(home, "rig.stack.jsonc"))
				if e != nil {
					return e
				}
				if e = stackCanaryPublish(ctx, api, parts[1]+"/"+parts[2], branch, base, updated, result); e != nil {
					fmt.Fprintf(&summary, "Canary %s: publication unavailable; check home contents/issues/PR permissions. If PR creation failed, enable \"Allow GitHub Actions to create and approve pull requests\" or use a token with PR write access. A rig-canary/ branch may remain; the next run reuses it.\n", stackCanaryMembers(result))
					continue
				}
				fmt.Fprintf(&summary, "Canary %s: publication reconciled.\n", stackCanaryMembers(result))
			}
			return stackCanarySummary(cmd.OutOrStdout(), strings.TrimSpace(summary.String()))
		}
		if dryRun {
			fmt.Fprintln(cmd.OutOrStdout(), "Would resolve upstream tips, build, test and write a canary report")
			return nil
		}
		selected := map[string]bool{}
		if members != "" {
			for _, n := range strings.Split(members, ",") {
				if m.Repos[n] == nil {
					return fmt.Errorf("unknown member %q", n)
				}
				selected[n] = true
			}
		}
		var changes []stackCanaryChange
		for _, n := range m.names() {
			repo := m.Repos[n]
			if (members != "" && !selected[n]) || m.pin(n).pinned() || repo.TrackBranch != "" {
				continue
			}
			if !stackIsSHA(m.cursor(n)) {
				return fmt.Errorf("member %s needs a full cursor", n)
			}
			dir := filepath.Join(tmp, "upstream-"+n)
			if _, e = stackCIGit(ctx, cwd, "clone", "--bare", "--", stackRemoteURL(repo.Upstream), dir); e != nil {
				return fmt.Errorf("cannot fetch upstream member %s", n)
			}
			b, e := stackCIGit(ctx, dir, "rev-parse", "refs/heads/"+m.branch(n))
			if e != nil {
				return fmt.Errorf("cannot resolve member %s", n)
			}
			tip := strings.TrimSpace(string(b))
			if tip == m.cursor(n) {
				continue
			}
			if _, e = stackCIGit(ctx, dir, "merge-base", "--is-ancestor", m.cursor(n), tip); e != nil {
				return fmt.Errorf("member %s cursor is not an ancestor of tip", n)
			}
			b, e = stackCIGit(ctx, dir, "log", "--format=%H", m.cursor(n)+".."+tip)
			if e != nil {
				return e
			}
			changes = append(changes, stackCanaryChange{n, m.cursor(n), tip, strings.Fields(string(b))})
		}
		r := stackCanaryReport{Base: base}
		run := func(ch []stackCanaryChange) (bool, error) {
			return stackCanaryBuild(ctx, tmp, home, data, ch, build, test)
		}
		if len(changes) > 0 {
			if mode == "separate" {
				for _, ch := range changes {
					single := []stackCanaryChange{ch}
					green, err := run(single)
					if err != nil {
						return err
					}
					r.Results = append(r.Results, stackCanaryResult{Changes: single, Green: green})
				}
			} else {
				green, err := run(changes)
				if err != nil {
					return err
				}
				var failures []string
				if !green && isolate && len(changes) > 1 {
					for _, ch := range changes {
						single := []stackCanaryChange{ch}
						singleGreen, err := run(single)
						if err != nil {
							return err
						}
						if !singleGreen {
							failures = append(failures, ch.Member)
						}
					}
				}
				r.Results = append(r.Results, stackCanaryResult{Changes: changes, Green: green, Batch: true, Failures: failures})
			}
		}
		b, e := json.MarshalIndent(r, "", "  ")
		if e != nil {
			return e
		}
		if e = os.WriteFile(report, b, 0600); e != nil {
			return e
		}
		return stackCanarySummary(cmd.OutOrStdout(), fmt.Sprintf("Canary tested %d moving member(s); report written.", len(changes)))
	}
	return c
}

func stackCanarySummary(out io.Writer, s string) error {
	fmt.Fprintln(out, s)
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		f, e := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer f.Close()
		_, e = fmt.Fprintln(f, s)
		return e
	}
	return nil
}

func stackCanaryEnv() []string {
	var env []string
	for _, s := range os.Environ() {
		k, _, _ := strings.Cut(s, "=")
		k = strings.ToUpper(k)
		if !strings.HasSuffix(k, "_TOKEN") && !strings.HasSuffix(k, "_PAT") {
			env = append(env, s)
		}
	}
	return env
}

func stackCanaryBuild(ctx context.Context, tmp, home string, data []byte, changes []stackCanaryChange, build, test string) (bool, error) {
	root, e := os.MkdirTemp(tmp, "build-")
	if e != nil {
		return false, e
	}
	if e = stackCIMaterialize(ctx, home, root); e != nil {
		return false, e
	}
	if e = os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), data, 0600); e != nil {
		return false, e
	}
	m, _, e := loadStackManifest(root)
	if e != nil {
		return false, e
	}
	tips := map[string]string{}
	for _, ch := range changes {
		tips[ch.Member] = ch.To
	}
	for _, n := range m.names() {
		r := m.Repos[n]
		if r.TrackBranch != "" {
			continue
		}
		sha := m.cursor(n)
		if tips[n] != "" {
			sha = tips[n]
		}
		if !stackIsSHA(sha) {
			return false, fmt.Errorf("canary setup: member %s has no full cursor", n)
		}
		r.UpstreamBranch = ""
		r.Branch = ""
		r.UpstreamTag = ""
		r.UpstreamCommit = sha
		m.LastSync[n] = sha
	}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return false, e
	}
	if e = os.WriteFile(filepath.Join(root, "rig.stack.jsonc"), b, 0600); e != nil {
		return false, e
	}
	exe, e := stackCIExecutable()
	if e != nil {
		return false, e
	}
	run := func(program string, args ...string) error { return stackCanaryRun(ctx, root, program, args...) }
	for _, phase := range []struct {
		name, program string
		args          []string
	}{
		{"git init", "git", []string{"init", "-q"}},
		{"stack init", exe, []string{"stack", "init"}},
		{"stack wire", exe, []string{"stack", "wire"}},
	} {
		if err := run(phase.program, phase.args...); err != nil {
			return false, fmt.Errorf("canary setup %s failed; outcome inconclusive (check credentials, network, engine and source compatibility): %w", phase.name, err)
		}
	}
	for _, command := range []string{build, test} {
		if err := run("bash", "-eo", "pipefail", "-c", command); err != nil {
			if ctx.Err() != nil {
				return false, fmt.Errorf("canary cancelled: %w", ctx.Err())
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return false, nil
			}
			return false, fmt.Errorf("cannot start canary command: %w", err)
		}
	}
	return true, nil
}

func stackCanaryPublish(ctx context.Context, a stackDepsAPI, repo, baseBranch, base string, manifest []byte, r stackCanaryResult) error {
	names := []string{}
	var body strings.Builder
	for _, ch := range r.Changes {
		names = append(names, ch.Member)
		fmt.Fprintf(&body, "\nMember %s: `%s..%s`\n", stackCanaryText(ch.Member), ch.From, ch.To)
		for _, sha := range ch.Commits {
			if stackIsSHA(sha) {
				fmt.Fprintf(&body, "- `%s`\n", sha)
			}
		}
	}
	if r.Batch {
		names = []string{"batch"}
	}
	sort.Strings(names)
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(names, "\n"))))[:16]
	branch := "rig-canary/" + key
	marker := "<!-- rig-canary:" + key + " -->"
	text := marker + "\nUpstream canary: reported green by the build job. The publisher verifies upstream ancestry, not the build outcome. Review before merging.\n" + body.String()
	if !r.Green {
		for _, n := range r.Failures {
			fmt.Fprintf(&body, "\nIndividual test reported failed: %s\n", stackCanaryText(n))
		}
		text = marker + "\nUpstream canary" + body.String()
		text += "\nBuild reported failed by the build job; the publisher cannot verify this outcome. Listed members and ranges are candidates; a batch failure does not establish individual blame.\n"
	}
	path := "/repos/" + repo
	var issues []struct {
		Number      int
		Body        string
		PullRequest json.RawMessage `json:"pull_request"`
	}
	// Follow pagination so an old marker cannot silently create duplicates.
	var issue int
	for page := 1; ; page++ {
		issues = nil
		if e := a.request(ctx, "GET", fmt.Sprintf("%s/issues?state=open&per_page=100&page=%d", path, page), nil, &issues); e != nil {
			return e
		}
		for _, i := range issues {
			if len(i.PullRequest) == 0 && strings.Contains(i.Body, marker) {
				issue = i.Number
			}
		}
		if len(issues) < 100 {
			break
		}
	}
	if !r.Green {
		var stale []struct{ Number int }
		if e := a.request(ctx, "GET", path+"/pulls?state=open&head="+strings.Split(repo, "/")[0]+":"+branch, nil, &stale); e != nil {
			return e
		}
		for _, pr := range stale {
			if e := a.request(ctx, "PATCH", fmt.Sprintf("%s/pulls/%d", path, pr.Number), map[string]string{"state": "closed"}, nil); e != nil {
				return e
			}
		}
		method, target := "POST", path+"/issues"
		if issue != 0 {
			method = "PATCH"
			target = fmt.Sprintf("%s/issues/%d", path, issue)
		}
		return a.request(ctx, method, target, map[string]any{"title": "Stack upstream canary failed", "body": text}, nil)
	}
	var prs []struct{ Number int }
	if e := a.request(ctx, "GET", path+"/pulls?state=open&head="+strings.Split(repo, "/")[0]+":"+branch, nil, &prs); e != nil {
		return e
	}
	// Git's data API creates exactly one manifest-only commit on the unfused seed.
	var commit struct {
		Tree struct{ SHA string }
		SHA  string
	}
	if e := a.request(ctx, "GET", path+"/git/commits/"+base, nil, &commit); e != nil {
		return e
	}
	var tree struct{ SHA string }
	if e := a.request(ctx, "POST", path+"/git/trees", map[string]any{"base_tree": commit.Tree.SHA, "tree": []any{map[string]string{"path": "rig.stack.jsonc", "mode": "100644", "type": "blob", "content": string(manifest)}}}, &tree); e != nil {
		return e
	}
	var created struct{ SHA string }
	if e := a.request(ctx, "POST", path+"/git/commits", map[string]any{"message": "stack: advance upstream cursors", "tree": tree.SHA, "parents": []string{base}}, &created); e != nil {
		return e
	}
	var ref struct{ Object struct{ SHA string } }
	err := a.request(ctx, "GET", path+"/git/ref/heads/"+branch, nil, &ref)
	if err == nil {
		err = a.request(ctx, "PATCH", path+"/git/refs/heads/"+branch, map[string]any{"sha": created.SHA, "force": true}, nil)
	} else {
		err = a.request(ctx, "POST", path+"/git/refs", map[string]any{"ref": "refs/heads/" + branch, "sha": created.SHA}, nil)
	}
	if err != nil {
		return err
	}
	if len(prs) > 0 {
		err = a.request(ctx, "PATCH", fmt.Sprintf("%s/pulls/%d", path, prs[0].Number), map[string]string{"body": text}, nil)
	} else {
		err = a.request(ctx, "POST", path+"/pulls", map[string]string{"title": "Stack: advance upstream cursors", "body": text, "head": branch, "base": baseBranch}, nil)
	}
	if err == nil && issue != 0 {
		err = a.request(ctx, "PATCH", fmt.Sprintf("%s/issues/%d", path, issue), map[string]string{"state": "closed"}, nil)
	}
	return err
}

// Only generated labels and SHAs enter reports. Never render upstream subjects,
// subprocess output or token-bearing diagnostics into issues or workflow logs.
func stackCanaryText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_/.", r) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "&#%d;", r)
		}
	}
	return b.String()
}

// API unavailability skips this result; invalid ancestry remains a hard error.
func stackCanaryValidateResult(ctx context.Context, api stackDepsAPI, m *stackManifest, result *stackCanaryResult) (bool, error) {
	for j := range result.Changes {
		ch := &result.Changes[j]
		upstream := strings.SplitN(m.Repos[ch.Member].Upstream, "/", 2)
		if len(upstream) != 2 || upstream[0] != "github.com" {
			return false, fmt.Errorf("publication requires GitHub upstreams")
		}
		// GitHub resolves SHAs across a fork network. Advancing the
		// cursor alone does not prove membership in upstream's branch.
		var membership struct{ Status string }
		tracked := url.PathEscape("refs/heads/" + m.branch(ch.Member))
		// Compare branch...target: behind means the target trails upstream.
		endpoint := "/repos/" + upstream[1] + "/compare/" + tracked + "..." + ch.To
		if e := api.request(ctx, "GET", endpoint, nil, &membership); e != nil {
			return false, nil
		}
		if membership.Status != "behind" && membership.Status != "identical" {
			return false, fmt.Errorf("cursor target is not on the tracked upstream branch")
		}
		ch.Commits = nil
		for page := 1; page <= 100; page++ {
			var compare struct {
				Status  string
				Commits []struct{ SHA string }
			}
			endpoint := fmt.Sprintf("/repos/%s/compare/%s...%s?per_page=100&page=%d", upstream[1], ch.From, ch.To, page)
			if e := api.request(ctx, "GET", endpoint, nil, &compare); e != nil {
				return false, nil
			}
			if compare.Status != "ahead" {
				return false, fmt.Errorf("upstream range must advance cursor")
			}
			for _, commit := range compare.Commits {
				if !stackIsSHA(commit.SHA) {
					return false, fmt.Errorf("invalid upstream commit")
				}
				ch.Commits = append(ch.Commits, commit.SHA)
			}
			if len(compare.Commits) < 100 {
				break
			}
			if page == 100 {
				return false, fmt.Errorf("upstream range exceeds 10000 commits; reconcile seed manually")
			}
		}

	}
	return true, nil
}

func stackCanaryMembers(result stackCanaryResult) string {
	var names []string
	for _, ch := range result.Changes {
		names = append(names, stackCanaryText(ch.Member))
	}
	return strings.Join(names, ", ")
}

var stackCanaryRun = func(ctx context.Context, root, program string, args ...string) error {
	p := exec.CommandContext(ctx, program, args...)
	p.Dir = root
	p.Env = append(stackCanaryEnv(), "RIG_STACK_CI_VERIFIED=1", "GIT_AUTHOR_NAME=rig", "GIT_AUTHOR_EMAIL=rig@localhost", "GIT_COMMITTER_NAME=rig", "GIT_COMMITTER_EMAIL=rig@localhost")
	return p.Run()
}
