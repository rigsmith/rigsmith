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
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/agentrig/ghrepo"
	"github.com/spf13/cobra"
)

// Seed references deliberately reserve @ only for the owner/repo shorthand.
// URLs, scp remotes and local paths retain git's normal spelling.
func stackSeedReference(s string) (remote, ref string, err error) {
	s = strings.TrimSpace(s)
	if err := stackValidateHomeRemote(s); err != nil {
		return "", "", err
	}
	if s == "" || strings.HasPrefix(s, "-") {
		return "", "", fmt.Errorf("empty or invalid seed reference")
	}
	if !strings.Contains(s, ":") {
		if _, err := os.Stat(s); err == nil {
			remote, err = filepath.Abs(s)
			return remote, "", err
		}
	}
	if !strings.Contains(s, ":") && !strings.HasPrefix(s, ".") && !filepath.IsAbs(s) && strings.Count(strings.SplitN(s, "@", 2)[0], "/") == 1 {
		remote, ref, _ = strings.Cut(s, "@")
		if strings.Contains(s, "@") && ref == "" {
			return "", "", fmt.Errorf("seed reference needs a ref after @")
		}
		return "https://github.com/" + strings.TrimSuffix(remote, ".git") + ".git", ref, nil
	}
	return s, "", nil
}

func stackHomeGit(ctx context.Context, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	b, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, stackSafeHomeOutput(strings.TrimSpace(string(b))))
	}
	// NUL-delimited output contains pathnames whose whitespace is significant.
	if bytes.IndexByte(b, 0) >= 0 {
		return string(b), nil
	}
	return strings.TrimSpace(string(b)), nil
}

var stackHomeURLPattern = regexp.MustCompile("[a-zA-Z][a-zA-Z0-9+.-]*://[^\\s'\"]+")

func stackSafeHomeOutput(s string) string {
	return stackHomeURLPattern.ReplaceAllStringFunc(s, ghrepo.SafeRemote)
}

func stackHomeConfig(ctx context.Context, dir, key string) string {
	s, _ := stackHomeGit(ctx, dir, "config", "--local", "--get", "rig.stackHome"+key)
	return s
}

// Every reachable commit is checked, not merely the tip: deleted member trees
// must not smuggle fused ancestors into the home.
func stackCheckHome(ctx context.Context, dir string, m *stackManifest) error {
	commits, err := stackHomeGit(ctx, dir, "rev-list", "HEAD")
	if err != nil {
		return err
	}
	for _, sha := range strings.Fields(commits) {
		names, err := stackHomeGit(ctx, dir, "ls-tree", "--name-only", "-z", sha)
		if err != nil {
			return err
		}
		for _, name := range strings.Split(names, "\x00") {
			if m.Repos[name] != nil {
				return &stackHomeTreeError{Name: name, Commit: sha}
			}
		}
	}
	return nil
}

type stackHomeTreeError struct{ Name, Commit string }

func (e *stackHomeTreeError) Error() string {
	return fmt.Sprintf("seed contains member tree %s in %s; refusing fused history", e.Name, e.Commit)
}

func stackReportHomeDoctor(out io.Writer, err error) {
	if err == nil {
		return
	}
	var tree *stackHomeTreeError
	if errors.As(err, &tree) {
		fmt.Fprintf(out, "✗ seed origin: %v\n", err)
	} else {
		fmt.Fprintf(out, "· seed origin could not be checked: %v\n", err)
	}
}

func stackPublishAfterPull(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, out ...io.Writer) error {
	if err := stackPublishHome(ctx, repo, m, out...); err != nil {
		return fmt.Errorf("pull succeeded locally, but the manifest home was not updated: %w", err)
	}
	return nil
}

// Credentials belong in Git's credential helper, never persisted in the home URL.
func stackValidateHomeRemote(remote string) error {
	u, err := url.Parse(remote)
	if err == nil && u.User != nil {
		_, password := u.User.Password()
		if u.Scheme == "http" || u.Scheme == "https" || password {
			return fmt.Errorf("seed URL credentials are not supported; configure a Git credential helper and use a URL without userinfo")
		}
	}
	return nil
}

func stackConfigureHome(ctx context.Context, dir, remote, branch, base string) error {
	if err := stackValidateHomeRemote(remote); err != nil {
		return err
	}
	for _, pair := range [][2]string{{"URL", remote}, {"Branch", branch}, {"Base", base}} {
		if _, err := stackHomeGit(ctx, dir, "config", "--local", "rig.stackHome"+pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}

// Publication has no object connection to the fused repository. Only an archive
// crosses the boundary; the parent is always the separately cloned seed tip.
type stackHomePublishPolicy struct{ automatic, force bool }

func stackPublishHome(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, out ...io.Writer) error {
	return stackPublishConfiguredHome(ctx, repo, m, stackHomePublishPolicy{automatic: true}, out...)
}

func stackPublishConfiguredHome(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, policy stackHomePublishPolicy, out ...io.Writer) error {
	remote := stackHomeConfig(ctx, repo.Dir, "URL")
	if remote == "" {
		return nil
	}
	branch := stackHomeConfig(ctx, repo.Dir, "Branch")
	base := stackHomeConfig(ctx, repo.Dir, "Base")
	err := stackPublishHomeToWithPolicy(ctx, repo, m, remote, branch, base, policy, out...)
	if err != nil {
		return fmt.Errorf("local stack changes are retained; manifest home was not updated: %w; retry with `rig stack seed --publish %q` after reconciling seed updates", err, ghrepo.SafeRemote(remote))
	}
	return nil
}

func stackPublishHomeTo(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, remote, branch, base string, out ...io.Writer) error {
	return stackPublishHomeToWithPolicy(ctx, repo, m, remote, branch, base, stackHomePublishPolicy{}, out...)
}

func stackPublishHomeToWithPolicy(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, remote, branch, base string, policy stackHomePublishPolicy, out ...io.Writer) error {
	if err := stackValidateHomeRemote(remote); err != nil {
		return err
	}
	if dryRun {
		if len(out) > 0 {
			fmt.Fprintf(out[0], "would publish committed manifest, README and root overlays to %s (branch %s)\n", ghrepo.SafeRemote(remote), branch)
		}
		return nil
	}
	if dirty, err := repo.Dirty(ctx); err != nil {
		return err
	} else if dirty {
		if policy.automatic {
			if len(out) > 0 {
				fmt.Fprintln(out[0], "manifest home publication deferred — commit or stash other changes, then run rig stack seed")
			}
			return nil
		}
		return fmt.Errorf("commit stack changes before publishing the manifest home")
	}
	if !policy.force {
		problems := stackSeedRebuildProblems(ctx, repo, m)
		if !policy.automatic && len(problems) > 0 {
			return errors.Join(problems...)
		}
		for _, problem := range problems {
			if len(out) > 0 {
				fmt.Fprintf(out[0], "warning: manifest home cannot reconstruct all local member work: %v; publishing recorded cursors and proposals only\n", problem)
			}
		}
	}
	if branch == "READ_ONLY" {
		return fmt.Errorf("seed is pinned to a tag; publication requires a branch")
	}
	dir, err := os.MkdirTemp("", "rig-stack-home-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	args := []string{"clone", "--no-local"}
	args = append(args, "--", remote, dir)
	if _, err := stackHomeGit(ctx, repo.Dir, args...); err != nil {
		return err
	}
	emptyHooks := filepath.Join(dir, ".git", "rig-publication-hooks")
	if err := os.MkdirAll(emptyHooks, 0755); err != nil {
		return err
	}
	if _, err := stackHomeGit(ctx, dir, "config", "--local", "core.hooksPath", emptyHooks); err != nil {
		return err
	}
	if branch != "" {
		if _, err := stackHomeGit(ctx, dir, "show-ref", "--verify", "refs/remotes/origin/"+branch); err == nil {
			if _, err := stackHomeGit(ctx, dir, "checkout", "-B", branch, "refs/remotes/origin/"+branch); err != nil {
				return err
			}
		} else if _, err := stackHomeGit(ctx, dir, "show-ref", "--verify", "refs/tags/"+branch); err == nil {
			return fmt.Errorf("seed ref %q is a tag; publication requires a branch", branch)
		} else if tip, _ := stackHomeGit(ctx, dir, "rev-parse", "--verify", "HEAD"); tip == "" {
			if _, err := stackHomeGit(ctx, dir, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("seed branch %q does not exist; publication requires a branch", branch)
		}
	}
	tip, _ := stackHomeGit(ctx, dir, "rev-parse", "--verify", "HEAD")
	if base != "" && tip != base {
		return fmt.Errorf("seed moved since last sync (%s -> %s); reconcile its manifest before publishing", base, tip)
	}
	if tip != "" {
		if err := stackCheckHome(ctx, dir, m); err != nil {
			return err
		}
	}
	if branch == "" {
		branch, err = stackHomeGit(ctx, dir, "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return err
		}
	}
	names, err := repo.TopLevelNames(ctx, "HEAD")
	if err != nil {
		return err
	}
	var keep []string
	for _, n := range names {
		if m.Repos[n] == nil {
			keep = append(keep, n)
		}
	}
	data, err := repo.ArchiveTar(ctx, "HEAD", keep)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	if err := untar(bytes.NewReader(data), dir); err != nil {
		return err
	}
	if _, err := stackHomeGit(ctx, dir, "add", "-A", "--force", "--", "."); err != nil {
		return err
	}
	diff, err := stackHomeGit(ctx, dir, "diff", "--cached", "--name-only")
	if err != nil {
		return err
	}
	if diff != "" && tip != "" && base == "" {
		return fmt.Errorf("existing seed differs from this stack; set up from the seed and reconcile before publishing")
	}
	if diff != "" {
		if _, err := stackHomeGit(ctx, dir, "-c", "user.name=rig", "-c", "user.email=rig@localhost", "-c", "commit.gpgsign=false", "commit", "-m", "stack: update manifest home"); err != nil {
			return err
		}
	}
	if err := stackCheckHome(ctx, dir, m); err != nil {
		return err
	}
	if _, err := stackHomeGit(ctx, dir, "push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		if strings.Contains(err.Error(), "refusing to update checked out branch") {
			return fmt.Errorf("%w; publish to a bare seed repository for local manifest homes", err)
		}
		return err
	}
	tip, err = stackHomeGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	return stackConfigureHome(ctx, repo.Dir, remote, branch, tip)
}

func newStackHomeCmd() *cobra.Command {
	return &cobra.Command{Use: "home [pointer-file]", Short: "Read a member's .rig-stack seed pointer", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		path := ".rig-stack"
		if len(args) > 0 {
			path = args[0]
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := strings.TrimSuffix(strings.TrimSuffix(string(b), "\r\n"), "\n")
		if strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return fmt.Errorf(".rig-stack must contain exactly one seed reference without whitespace or control characters")
		}
		if _, _, err := stackSeedReference(s); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), s)
		return nil
	}}
}

func stackDoctorHome(ctx context.Context, root string, m *stackManifest) error {
	remote := stackHomeConfig(ctx, root, "URL")
	if remote == "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "rig-stack-doctor-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if _, err := stackHomeGit(ctx, root, "clone", "--no-local", "--", remote, dir); err != nil {
		return err
	}
	branch := stackHomeConfig(ctx, root, "Branch")
	if branch != "" {
		target := "refs/remotes/origin/" + branch
		if branch == "READ_ONLY" {
			target = stackHomeConfig(ctx, root, "Base")
		}
		commit, err := stackHomeGit(ctx, dir, "rev-parse", "--verify", target+"^{commit}")
		if err != nil {
			return err
		}
		if _, err := stackHomeGit(ctx, dir, "checkout", "--detach", commit); err != nil {
			return err
		}
	}
	return stackCheckHome(ctx, dir, m)
}

func stackGuardMemberHome(ctx context.Context, repo *gitrepo.Repo, remote string) error {
	home := stackHomeConfig(ctx, repo.Dir, "URL")
	if home == "" {
		home, _ = stackHomeGit(ctx, repo.Dir, "remote", "get-url", "origin")
	}
	if home == "" {
		return nil
	}
	refuse := func() error {
		return fmt.Errorf("member destination is the manifest home; refusing to push member history")
	}
	if stackNormalizeSpec(home) == stackNormalizeSpec(remote) {
		return refuse()
	}
	memberDestination, err := stackEffectivePushURL(ctx, repo.Dir, remote)
	if err != nil {
		return err
	}
	homeDestination, err := stackEffectivePushURL(ctx, repo.Dir, home)
	if err != nil {
		return err
	}
	if stackNormalizeSpec(memberDestination) == stackNormalizeSpec(homeDestination) ||
		stackNormalizeSpec(memberDestination) == stackNormalizeSpec(home) {
		return refuse()
	}
	return nil
}

// Git chooses the longest matching prefix, with pushInsteadOf preferred for
// pushes. Reading through git config includes system/global/local and -c rules.
func stackEffectivePushURL(ctx context.Context, dir, remote string) (string, error) {
	rules, err := stackHomeGit(ctx, dir, "config", "--null", "--get-regexp", "^url\\..*\\.(insteadof|pushinsteadof)$")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return remote, nil
		}
		return "", err
	}
	type rewrite struct{ base, prefix string }
	var fetch, push []rewrite
	for _, record := range strings.Split(rules, "\x00") {
		key, value, ok := strings.Cut(record, "\n")
		if !ok {
			continue
		}
		base := strings.TrimPrefix(key, "url.")
		if strings.HasSuffix(base, ".pushinsteadof") {
			push = append(push, rewrite{strings.TrimSuffix(base, ".pushinsteadof"), value})
		} else {
			fetch = append(fetch, rewrite{strings.TrimSuffix(base, ".insteadof"), value})
		}
	}
	apply := func(rules []rewrite) (string, bool) {
		best := -1
		target := ""
		for _, rule := range rules {
			if strings.HasPrefix(remote, rule.prefix) && len(rule.prefix) > best {
				best = len(rule.prefix)
				target = rule.base + strings.TrimPrefix(remote, rule.prefix)
			}
		}
		return target, best >= 0
	}
	if target, ok := apply(push); ok {
		return target, nil
	}
	if target, ok := apply(fetch); ok {
		return target, nil
	}
	return remote, nil
}
