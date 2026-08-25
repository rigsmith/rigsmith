package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

// newWsCmd builds the `ws` command group — a fused workspace of upstream
// forks: each project's history imported under a prefix of one repo through
// josh's reversible filters, so commits can span projects and any slice can
// leave as a clean PR branch on the matching fork (docs/WORKSPACE-DESIGN.md).
func newWsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ws",
		Short: "Fused workspace — upstream forks as prefixes of one history",
		Long: "A ws workspace fuses several upstream repos into one git history, each\n" +
			"under a prefix, via josh's reversible filters. Commits may span projects;\n" +
			"`send` extracts a prefix's changes back onto your fork as an ordinary\n" +
			"PR-ready branch. Upstream never learns the workspace exists.\n\n" +
			"  rig ws init                 scaffold the manifest / import the repos\n" +
			"  rig ws status               cursor vs upstream, per repo\n" +
			"  rig ws pull [repo]          merge new upstream commits (all repos by default)\n" +
			"  rig ws send <repo> <branch> extract changes to a branch on your fork\n" +
			"  rig ws doctor               engine + manifest checks (--fix installs josh)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newWsInitCmd(), newWsStatusCmd(), newWsPullCmd(), newWsSendCmd(), newWsDoctorCmd())
	return cmd
}

// wsWorkspace opens the manifest and the workspace repo together — every ws
// verb needs both, and "no manifest here" should read the same everywhere.
func wsWorkspace(ctx context.Context) (*wsManifest, *cfgfind.Source, *gitrepo.Repo, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, nil, err
	}
	root := resolveRoot(cwd)
	m, src, err := loadWsManifest(root)
	if err != nil {
		return nil, nil, nil, err
	}
	if m == nil {
		return nil, nil, nil, fmt.Errorf("no ws manifest here — run `rig ws init` at the workspace root")
	}
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ws workspace %s is not a git repository", root)
	}
	return m, src, repo, nil
}

// wsEngine resolves the workspace's josh version (manifest override, else the
// pinned default) and ensures the binary, printing install progress to out.
func wsEngine(ctx context.Context, m *wsManifest, cmd *cobra.Command) (string, error) {
	version := wsJoshVersion
	if m != nil && m.Josh != "" {
		version = m.Josh
	}
	return ensureJoshProxy(ctx, version, cmd.OutOrStdout())
}

func newWsInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold the ws manifest, or import its repos into the workspace",
		Long: "With no manifest, writes a commented rig.ws.jsonc to fill in. With a\n" +
			"manifest, imports each repo that has no cursor yet: fetches its upstream\n" +
			"history through the :prefix filter and merges it in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root := resolveRoot(cwd)
			m, src, err := loadWsManifest(root)
			if err != nil {
				return err
			}
			if m == nil {
				p, err := wsWriteTemplate(root)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s — fill in the repos, then run `rig ws init` again to import them\n", p)
				return nil
			}
			repo, err := gitrepo.Open(ctx, root)
			if err != nil {
				return fmt.Errorf("run `git init` first — the workspace itself is an ordinary git repo")
			}
			bin, err := wsEngine(ctx, m, cmd)
			if err != nil {
				return err
			}
			imported := 0
			for _, name := range m.names() {
				if m.cursor(name) != "" {
					continue // already imported; `pull` owns updates
				}
				if err := wsPullOne(ctx, cmd, repo, bin, src, m, name, true); err != nil {
					return fmt.Errorf("importing %s: %w", name, err)
				}
				imported++
			}
			if imported == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to import — every repo has a cursor; use `rig ws pull` for updates")
			}
			return nil
		},
	}
	return cmd
}

func newWsStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Each repo's cursor vs its upstream branch tip",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			m, _, repo, err := wsWorkspace(ctx)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, name := range m.names() {
				r := m.Repos[name]
				tip, err := repo.LsRemote(ctx, wsHTTPSURL(r.Upstream), "refs/heads/"+m.branch(name))
				if err != nil {
					fmt.Fprintf(out, "%-24s %s (upstream unreachable: %v)\n", name, short(m.cursor(name)), err)
					continue
				}
				state := "up to date"
				switch {
				case m.cursor(name) == "":
					state = "not imported — run `rig ws init`"
				case tip != m.cursor(name):
					state = fmt.Sprintf("upstream moved (%s) — `rig ws pull %s`", short(tip), name)
				}
				fmt.Fprintf(out, "%-24s %-10s %s\n", name, short(m.cursor(name)), state)
			}
			return nil
		},
	}
	return cmd
}

func newWsPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull [repo]",
		Short: "Merge new upstream commits into a repo's prefix (all repos by default)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			m, src, repo, err := wsWorkspace(ctx)
			if err != nil {
				return err
			}
			if dirty, err := repo.Dirty(ctx); err != nil {
				return err
			} else if dirty {
				return fmt.Errorf("workspace has uncommitted changes — commit or stash before pulling")
			}
			names := m.names()
			if len(args) == 1 {
				if m.Repos[args[0]] == nil {
					return fmt.Errorf("no ws repo %q (have: %s)", args[0], strings.Join(names, ", "))
				}
				names = args[:1]
			}
			bin, err := wsEngine(ctx, m, cmd)
			if err != nil {
				return err
			}
			for _, name := range names {
				if err := wsPullOne(ctx, cmd, repo, bin, src, m, name, false); err != nil {
					return fmt.Errorf("pulling %s: %w", name, err)
				}
			}
			return nil
		},
	}
	return cmd
}

// wsPullOne imports or updates one repo's prefix: probe upstream's tip, stop at
// the cursor (idempotent, the josh-sync NothingToPull check), else fetch that
// exact SHA through the filter, merge, and commit the moved cursor with it.
func wsPullOne(ctx context.Context, cmd *cobra.Command, repo *gitrepo.Repo, bin string, src *cfgfind.Source, m *wsManifest, name string, initial bool) error {
	r := m.Repos[name]
	out := cmd.OutOrStdout()
	tip, err := repo.LsRemote(ctx, wsHTTPSURL(r.Upstream), "refs/heads/"+m.branch(name))
	if err != nil {
		return err
	}
	if !initial && tip == m.cursor(name) {
		fmt.Fprintf(out, "%s: nothing to pull\n", name)
		return nil
	}
	host, path := wsSplitHost(r.Upstream)
	proxy, err := startJoshProxy(ctx, bin, host)
	if err != nil {
		return err
	}
	defer proxy.stop()
	verb := "pulled"
	msg := fmt.Sprintf("ws: pull %s @ %s", name, short(tip))
	if initial {
		verb = "imported"
		msg = fmt.Sprintf("ws: import %s @ %s", name, short(tip))
	}
	conflicted, err := repo.FetchMergeUnrelated(ctx, proxy.url(path, tip, wsPrefixFilter(name)), m.branch(name), msg)
	if err != nil {
		return err
	}
	if conflicted {
		return fmt.Errorf("merge conflicts under %s/ — resolve, commit, then re-run to move the cursor", name)
	}
	if err := wsSetCursor(src, m, name, tip); err != nil {
		return err
	}
	if err := repo.StageAll(ctx); err != nil {
		return err
	}
	// Amend the cursor edit into the merge commit, so one commit carries both
	// the history and the fact that it was synced — the reviewable unit a
	// cronned pull PR is built from.
	if _, err := repo.CommitAmendNoEdit(ctx); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %s upstream %s\n", name, verb, short(tip))
	return nil
}

func newWsSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <repo> <branch>",
		Short: "Extract a repo's workspace changes onto your fork as a PR-ready branch",
		Long: "Pushes the current HEAD through josh's reverse filter at your fork: only\n" +
			"the commits touching <repo>'s prefix arrive, re-rooted on upstream history\n" +
			"with correct parents, as <branch> on the fork. PR from there as usual.\n\n" +
			"The proxy fronts https, so pushing authenticates with your git credential\n" +
			"helper (a GitHub PAT). SSH-only setups: see docs/WORKSPACE-DESIGN.md.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name, branch := args[0], args[1]
			m, _, repo, err := wsWorkspace(ctx)
			if err != nil {
				return err
			}
			r := m.Repos[name]
			if r == nil {
				return fmt.Errorf("no ws repo %q (have: %s)", name, strings.Join(m.names(), ", "))
			}
			bin, err := wsEngine(ctx, m, cmd)
			if err != nil {
				return err
			}
			host, path := wsSplitHost(r.Fork)
			proxy, err := startJoshProxy(ctx, bin, host)
			if err != nil {
				return err
			}
			defer proxy.stop()
			if err := repo.Push(ctx, proxy.url(path, "", wsPrefixFilter(name)), "refs/heads/"+branch); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sent %s to %s:%s — open the PR against %s\n",
				name, r.Fork, branch, r.Upstream)
			return nil
		},
	}
	return cmd
}

func newWsDoctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the ws engine and manifest; --fix installs the pinned josh",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			m, _, err := loadWsManifest(resolveRoot(cwd))
			if err != nil {
				return err
			}
			version := wsJoshVersion
			if m != nil && m.Josh != "" {
				version = m.Josh
			}
			switch {
			case m == nil:
				fmt.Fprintln(out, "· no ws manifest here (fine outside a workspace)")
			default:
				fmt.Fprintf(out, "✓ manifest: %d repo(s)\n", len(m.Repos))
			}
			bin, binErr := wsJoshProxyBin(version)
			if binErr == nil {
				_, binErr = os.Stat(bin)
			}
			switch {
			case binErr == nil:
				fmt.Fprintf(out, "✓ josh-proxy %s installed\n", version)
			case fix:
				if _, err := ensureJoshProxy(ctx, version, out); err != nil {
					return err
				}
				fmt.Fprintf(out, "✓ josh-proxy %s installed\n", version)
			default:
				fmt.Fprintf(out, "✗ josh-proxy %s not installed — `rig ws doctor --fix` builds it via cargo\n", version)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "install what's missing")
	return cmd
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	if sha == "" {
		return "—"
	}
	return sha
}
