package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

func newStackEdgeAdoptCmd() *cobra.Command {
	var dry bool
	c := &cobra.Command{
		Use: "adopt <member> <pr-url>", Short: "Bring an upstream PR into an import-rooted stackspace topic",
		Long: "Read a PR on the member's upstream and fetch only its head commit from its head repository.\n" +
			"The PR must target the member's tracked upstream branch.\n" +
			"Merge its change onto the cursor tree and create stack-pr-adopt-<member>-<number>, rooted\n" +
			"on the member's import commit. Conflicts and existing topics with different content refuse.\n" +
			"Print the adopted entry to add by hand, then merge the topic and refresh trackBranch.\n" +
			"Never edits the manifest or checkout, merges, proposes or pushes. --dry-run writes no branch.",
		Args: cobra.ExactArgs(2), ValidArgsFunction: stackRepoCompletion,
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
		return stackEdgeAdopt(cmd.Context(), stackDepsClient(), root, m, args[0], args[1], dry, cmd.OutOrStdout())
	}
	c.Flags().BoolVarP(&dry, "dry-run", "n", false, "Show the topic and adopted entry without writing a branch")
	return c
}

func stackEdgePRNumber(raw string, member *stackRepo) (int, error) {
	if !stackEdgePRUpstream(raw, member.Upstream) {
		return 0, fmt.Errorf("PR must be on the member's upstream %s", member.Upstream)
	}
	parts := strings.Split(strings.TrimSuffix(raw, "/"), "/")
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0, fmt.Errorf("PR number is too large")
	}
	return n, nil
}

func stackEdgeAdopt(ctx context.Context, api stackDepsAPI, root string, m *stackManifest, name, raw string, dry bool, out io.Writer) error {
	member := m.Repos[name]
	if member == nil {
		return fmt.Errorf("unknown stack member %s", name)
	}
	n, err := stackEdgePRNumber(raw, member)
	if err != nil {
		return err
	}
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		return err
	}
	imported := stackImportCommit(ctx, repo, name)
	if imported == "" || !stackIsSHA(m.cursor(name)) {
		return fmt.Errorf("member %s needs an import commit and a full cursor before adoption", name)
	}
	pr, err := api.pullRequest(ctx, member, n)
	if err != nil {
		return fmt.Errorf("PR %s: %w", raw, err)
	}
	if pr.Base.Ref != m.branch(name) {
		return fmt.Errorf("PR %s targets branch %q, but member %s tracks %q; the PR must target branch %q", raw, pr.Base.Ref, name, m.branch(name), m.branch(name))
	}
	if !stackIsSHA(pr.Head.SHA) {
		return fmt.Errorf("PR %s has no valid head commit", raw)
	}
	host, _ := stackSplitHost(member.Upstream)
	headRepo := host + "/" + pr.Head.Repo.FullName
	if err := stackValidRepoSpec(headRepo); err != nil {
		return fmt.Errorf("PR %s has no valid head repository", raw)
	}
	remote := stackRemoteURL(headRepo)
	// No API-provided URL or ref reaches Git. Disable configured ref mappings,
	// tags and submodule recursion so this fetch writes only head objects.
	fetch := exec.CommandContext(ctx, "git", "--no-replace-objects", "fetch", "--no-tags", "--no-write-fetch-head", "--recurse-submodules=no", "--refmap=", "--", remote, pr.Head.SHA)
	fetch.Dir = root
	fetch.Env = append(os.Environ(), stackAuthFor(ctx, headRepo).Env()...)
	if err := fetch.Run(); err != nil {
		return fmt.Errorf("PR %s: fetching head commit from %s failed", raw, headRepo)
	}
	base, err := repo.MergeBase(ctx, pr.Head.SHA, m.cursor(name))
	if err != nil || base == "" {
		return fmt.Errorf("PR %s: cannot find merge base with cursor %s; cursor history must be available locally", raw, m.cursor(name))
	}
	merged, err := stackCIGit(ctx, root, "merge-tree", "--write-tree", "--merge-base="+base, m.cursor(name), pr.Head.SHA)
	if err != nil {
		return fmt.Errorf("PR %s needs rebasing onto the cursor %s, or should be brought in by hand as a topic", raw, m.cursor(name))
	}
	tree := strings.TrimSpace(string(merged))
	if !stackIsSHA(tree) {
		return fmt.Errorf("PR %s: merge-tree returned no unique tree", raw)
	}
	tree, err = stackEdgeAdoptTree(ctx, root, imported, name, tree)
	if err != nil {
		return err
	}
	branch := stackEdgeAdoptBranch(name, n)
	ref := "refs/heads/" + branch
	old, err := stackCIGit(ctx, root, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return err
		}
	}
	if err == nil {
		oldTree, err := repo.RevParse(ctx, strings.TrimSpace(string(old))+"^{tree}")
		if err != nil {
			return err
		}
		if oldTree != tree {
			return fmt.Errorf("PR %s: topic %s already exists with different content; refusing to overwrite it (preserve or rename it before adopting again)", raw, branch)
		}
		fmt.Fprintf(out, "%s already has the same content; nothing to do\n", branch)
	} else if dry {
		fmt.Fprintf(out, "Would create %s rooted on %s\n", branch, imported)
	} else {
		commit, err := repo.CommitTree(ctx, tree, imported, fmt.Sprintf("stack edge: adopt %s at %s", raw, pr.Head.SHA))
		if err != nil {
			return err
		}
		// Compare-and-create also refuses a branch created during planning.
		if _, err := stackCIGit(ctx, root, "update-ref", ref, commit, strings.Repeat("0", 40)); err != nil {
			return err
		}
		fmt.Fprintf(out, "Created %s rooted on %s\n", branch, imported)
	}
	fmt.Fprintf(out, "Next: merge %s into the integration line, add this entry to edge.%s.adopted:\n{ \"pr\": %q, \"commit\": %q }\nThen run `rig stack propose %s --track-only`.\n", branch, name, raw, pr.Head.SHA, name)
	return nil
}

func stackEdgeAdoptBranch(member string, number int) string {
	// Prefixes are directory names, so they can contain characters Git refs
	// forbid. Escape those without making distinct member names collide.
	member = strings.NewReplacer(".", "%2E", "~", "%7E", ":", "%3A").Replace(url.PathEscape(member))
	return stackTopicPrefix + "adopt-" + member + "-" + strconv.Itoa(number)
}

// Replace just the member entry in the import tree. mktree avoids both the
// user's index and checkout, preserving all sibling and root entries verbatim.
func stackEdgeAdoptTree(ctx context.Context, root, imported, name, tree string) (string, error) {
	entries, err := stackCIGit(ctx, root, "ls-tree", "-z", imported)
	if err != nil {
		return "", err
	}
	found := false
	rows := bytes.Split(entries, []byte{0})
	for i, row := range rows {
		meta, path, _ := bytes.Cut(row, []byte{'\t'})
		if string(path) == name && bytes.HasPrefix(meta, []byte("040000 tree ")) {
			rows[i] = []byte("040000 tree " + tree + "\t" + name)
			found = true
		}
	}
	if !found {
		return "", fmt.Errorf("import commit has no member tree %s", name)
	}
	p := exec.CommandContext(ctx, "git", "--no-replace-objects", "mktree", "-z")
	p.Dir, p.Stdin = root, bytes.NewReader(bytes.Join(rows, []byte{0}))
	b, err := p.Output()
	if err != nil {
		return "", fmt.Errorf("building adopted topic tree: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

type stackEdgeAdoptedMove struct {
	PR      string `json:"pr"`
	Pinned  string `json:"pinned"`
	Head    string `json:"head"`
	Warning string `json:"warning"`
}

// Advisory only: callers keep building pinned content and retain their release
// and retirement verdicts. CI can include these same warnings in its report.
func stackEdgeAdoptedMoved(ctx context.Context, api stackDepsAPI, member *stackRepo, adopted []stackEdgeAdopted) ([]stackEdgeAdoptedMove, error) {
	moved := []stackEdgeAdoptedMove{}
	var failures []error
	for _, a := range adopted {
		n, err := stackEdgePRNumber(a.PR, member)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		pr, err := api.pullRequest(ctx, member, n)
		if err != nil {
			failures = append(failures, fmt.Errorf("PR %s: %w", a.PR, err))
			continue
		}
		if !stackIsSHA(pr.Head.SHA) {
			failures = append(failures, fmt.Errorf("PR %s has no valid head commit", a.PR))
			continue
		}
		if !strings.EqualFold(a.Commit, pr.Head.SHA) {
			warning := fmt.Sprintf("PR %s has moved from %s to %s; re-run `rig stack edge adopt` to update the topic, then update the pin", a.PR, a.Commit, pr.Head.SHA)
			moved = append(moved, stackEdgeAdoptedMove{PR: a.PR, Pinned: a.Commit, Head: pr.Head.SHA, Warning: warning})
		}
	}
	return moved, errors.Join(failures...)
}
