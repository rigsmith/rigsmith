package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/ecosystem"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

// newStackCmd builds the `stack` command group — a fused stackspace of upstream
// forks: each project's history imported under a prefix of one repo through
// josh's reversible filters, so commits can span projects and any slice can
// leave as a clean PR branch on the matching fork (docs/STACK-DESIGN.md).
func newStackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stack",
		Short: "Fused stackspace — upstream forks as prefixes of one history",
		Long: "A stackspace fuses several upstream repos into one git history, each\n" +
			"under a prefix, via josh's reversible filters. Commits may span projects,\n" +
			"and leave one project at a time: `send` puts a prefix's changes on your\n" +
			"fork as a PR-ready branch, and `push` fast-forwards a project you own with\n" +
			"its history. Neither leaves any trace that the stackspace exists.\n\n" +
			"  rig stack init                      scaffold the manifest / import the repos\n" +
			"  rig stack add [upstream]            add a repo and import it (asks if not given)\n" +
			"  rig stack status                    cursor vs upstream, per repo\n" +
			"  rig stack pull [repo]               merge new upstream commits (all by default)\n" +
			"  rig stack send <repo> <new-branch>  a branch on your fork, prefixed stack/\n" +
			"  rig stack push <repo>               fast-forward a repo you own, history intact\n" +
			"  rig stack wire                      write the build overlay for the members\n" +
			"  rig stack doctor                    engine + manifest checks (--fix installs josh)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newStackInitCmd(), newStackAddCmd(), newStackStatusCmd(), newStackPullCmd(), newStackSendCmd(), newStackPushCmd(), newStackWireCmd(), newStackDoctorCmd())
	return cmd
}

// stackRoot is the stackspace root: the git top level, not resolveRoot's answer.
// resolveRoot finds the nearest *project* — a package manifest or solution —
// and every imported repo carries one of those, so from inside a fused project
// it would answer that project and the stackspace would look like it did not
// exist.
// An explicit --root still wins, since that is the user saying where to look.
func stackRoot(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if rootFlag != "" {
		return resolveRoot(cwd), nil
	}
	repo, err := gitrepo.Open(ctx, cwd)
	if err != nil {
		return "", fmt.Errorf("not inside a git repository — a stackspace is one")
	}
	top, err := repo.Toplevel(ctx)
	if err != nil || top == "" {
		return resolveRoot(cwd), nil
	}
	return top, nil
}

// stackspace opens the manifest and the stackspace repo together — every stack
// verb needs both, and "no manifest here" should read the same everywhere.
func stackspace(ctx context.Context) (*stackManifest, *cfgfind.Source, *gitrepo.Repo, error) {
	root, err := stackRoot(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	m, src, err := loadStackManifest(root)
	if err != nil {
		return nil, nil, nil, err
	}
	if m == nil {
		return nil, nil, nil, fmt.Errorf("no stack manifest here — run `rig stack init` at the stackspace root")
	}
	repo, err := gitrepo.Open(ctx, root)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stackspace %s is not a git repository", root)
	}
	return m, src, repo, nil
}

// stackEngine resolves the stackspace's josh version (manifest override, else the
// pinned default) and ensures the binary, printing install progress to out.
func stackEngine(ctx context.Context, m *stackManifest, cmd *cobra.Command) (string, error) {
	version := m.joshVersion()
	return ensureJoshProxy(ctx, version, cmd.OutOrStdout())
}

func newStackInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold the stack manifest, or import its repos into the stackspace",
		Long: "With no manifest, writes a commented rig.stack.jsonc to fill in. With a\n" +
			"manifest, imports each repo that has no cursor yet: fetches its upstream\n" +
			"history through the :prefix filter and merges it in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root, err := stackRoot(ctx)
			if err != nil {
				return err
			}
			m, src, err := loadStackManifest(root)
			if err != nil {
				return err
			}
			if m == nil {
				p, err := stackWriteTemplate(root)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s — fill in the repos, then run `rig stack init` again to import them\n", p)
				return nil
			}
			repo, err := gitrepo.Open(ctx, root)
			if err != nil {
				return fmt.Errorf("run `git init` first — the stackspace itself is an ordinary git repo")
			}
			// Import amends the merge commit, and StageAll before it stages the
			// whole tree: without this guard an unrelated edit sitting in the
			// worktree would be swallowed into the import.
			if dirty, err := repo.Dirty(ctx); err != nil {
				return err
			} else if dirty && !stackOnlyManifestDirty(ctx, repo, src) {
				return fmt.Errorf("stackspace has uncommitted changes — commit or stash before importing")
			}
			// A merge into an unborn HEAD fast-forwards instead of creating a
			// merge commit, which leaves the cursor amended onto the upstream
			// tip itself and breaks the ancestry every later pull merges against.
			// Root the stackspace on the manifest first.
			if repo.Unborn(ctx) {
				if _, err := repo.Commit(ctx, "stack: stackspace manifest"); err != nil {
					return fmt.Errorf("creating the stackspace's first commit: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "committed the manifest as the stackspace's root commit")
			}
			names := []string{}
			for _, name := range m.names() {
				if m.cursor(name) == "" {
					names = append(names, name)
				}
			}
			// Only reach for the engine once there is something to import: on a
			// fresh machine acquiring it can mean a multi-minute build, and a
			// re-run with nothing to do should not pay that.
			var bin string
			if len(names) > 0 {
				if bin, err = stackEngine(ctx, m, cmd); err != nil {
					return err
				}
			}
			imported := 0
			for _, name := range names {
				if err := stackPullOne(ctx, cmd.OutOrStdout(), repo, bin, src, m, name, true); err != nil {
					return fmt.Errorf("importing %s: %w", name, err)
				}
				imported++
			}
			if imported == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to import — every repo has a cursor; use `rig stack pull` for updates")
			}
			return nil
		},
	}
	return cmd
}

// stackOnlyManifestDirty reports whether a dedicated manifest file is the only
// uncommitted thing. Filling in the scaffolded rig.stack.jsonc and running init
// again is the documented first run, so that one file must not trip the dirty
// guard — the import commits it anyway.
func stackOnlyManifestDirty(ctx context.Context, repo *gitrepo.Repo, src *cfgfind.Source) bool {
	// Only a dedicated manifest earns the exemption. An inline `stack` block
	// shares .rig.json with every other rig setting, so waving that file
	// through would commit whatever else the user happened to be editing.
	if src == nil || src.File == "" || src.Path == "" {
		return false
	}
	paths, err := repo.DirtyPaths(ctx)
	if err != nil || len(paths) == 0 {
		return false
	}
	manifest, err := filepath.Rel(repo.Dir, src.File)
	if err != nil {
		return false
	}
	manifest = filepath.ToSlash(manifest)
	for _, p := range paths {
		if filepath.ToSlash(p) != manifest {
			return false
		}
	}
	return true
}

func newStackStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Each repo's cursor vs its upstream, and what has not left the stackspace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			m, _, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// One status call for the whole stackspace: the prefixes are filtered
			// out of it per repo below, rather than shelling out once each.
			dirty, err := repo.DirtyPaths(ctx)
			if err != nil {
				return err
			}
			for _, name := range m.names() {
				pin := m.pin(name)
				// Whether this prefix is holding work is answered from the
				// stackspace alone, so it is computed before anything reaches for
				// the network — the moment you most need to know is when you are
				// about to delete a stackspace, and that is exactly when you might
				// be on a plane.
				u := stackUnsentWork(ctx, repo, name, dirty)
				state := "up to date"
				tip, err := stackUpstreamTip(ctx, repo, m, name)
				switch {
				case err != nil:
					state = "upstream unreachable — " + stackFirstLine(err)
				case m.cursor(name) == "":
					state = "not imported — run `rig stack init`"
				case tip != m.cursor(name):
					state = fmt.Sprintf("upstream moved (%s) — `rig stack pull %s`", short(tip), name)
				case pin.pinned():
					// A pin cannot drift, so "up to date" would understate it: the
					// reader needs to know this prefix will never move on its own.
					state = "pinned to " + pin.describe()
				}
				// Work that has not left the stackspace exists only here, and the
				// stackspace is documented as disposable. That combination is how
				// it gets thrown away, so report it whatever else is true.
				switch {
				case u.Working && u.Commits:
					state += fmt.Sprintf("  ·  uncommitted and unsent changes — commit, then `rig stack send %s <branch>`", name)
				case u.Working:
					state += "  ·  uncommitted changes"
				case u.Commits:
					state += fmt.Sprintf("  ·  unsent changes — `rig stack send %s <branch>`", name)
				case !u.Known && m.cursor(name) != "":
					state += "  ·  cannot tell whether it has unsent changes (no import commit in this history)"
				}
				fmt.Fprintf(out, "%-24s %-10s %s\n", name, short(m.cursor(name)), state)
			}
			return nil
		},
	}
	return cmd
}

func newStackPullCmd() *cobra.Command {
	var repin bool
	cmd := &cobra.Command{
		Use:               "pull [repo]",
		Short:             "Merge new upstream commits into a repo's prefix (all repos by default)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: stackRepoCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			m, src, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			if err := m.requireRepos(); err != nil {
				return err
			}
			// A pinned prefix reuses the commit its pin last resolved to, so an
			// upstream that re-cuts a tag cannot move it. Following such a move is
			// a deliberate act, and this is how you say so.
			if repin {
				m.LastPin = nil
			}
			if dirty, err := repo.Dirty(ctx); err != nil {
				return err
			} else if dirty {
				return fmt.Errorf("stackspace has uncommitted changes — commit or stash before pulling")
			}
			names := m.names()
			if len(args) == 1 {
				if m.Repos[args[0]] == nil {
					return fmt.Errorf("no stack repo %q (have: %s)", args[0], strings.Join(names, ", "))
				}
				names = args[:1]
			}
			// Probe first: a stackspace that is already current needs no engine,
			// and acquiring one can mean a download or a multi-minute build. A
			// no-op pull must not fail for want of a tool it never uses.
			moved := make([]string, 0, len(names))
			for _, name := range names {
				tip, err := stackUpstreamTip(ctx, repo, m, name)
				if err != nil {
					return fmt.Errorf("pulling %s: %w", name, err)
				}
				if tip == m.cursor(name) {
					fmt.Fprintf(cmd.OutOrStdout(), "%s: nothing to pull\n", name)
					continue
				}
				moved = append(moved, name)
			}
			if len(moved) == 0 {
				return nil
			}
			bin, err := stackEngine(ctx, m, cmd)
			if err != nil {
				return err
			}
			for _, name := range moved {
				if err := stackPullOne(ctx, cmd.OutOrStdout(), repo, bin, src, m, name, false); err != nil {
					return fmt.Errorf("pulling %s: %w", name, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&repin, "repin", false, "resolve a tag pin again, taking it where upstream has moved it")

	return cmd
}

// stackImportedTree is the tree a prefix had when it was last imported, pulled
// or pushed — the baseline for "has anything happened here since".
//
// The cursor cannot answer it: that is an *upstream* commit id, and a stackspace
// only holds josh-rewritten commits, so the object is never here; `send` fetches
// it deliberately when it needs to root on one. What is here is rig's own
// marker commit, always a merge because the import is made with --no-ff, whose
// *second* parent is the filtered upstream side. That parent's tree is what
// upstream had. The merge's own tree is not: it already contains whatever local
// work it merged past, so measuring against it calls a prefix clean the moment
// anything has been pulled since the work was done.
func stackImportedTree(ctx context.Context, repo *gitrepo.Repo, name string) (string, bool) {
	marker, err := repo.LastCommitMatching(ctx, `^stack: (import|pull|push) `+regexp.QuoteMeta(name)+` @`)
	if err != nil || marker == "" {
		return "", false
	}
	tree, err := repo.RevParse(ctx, marker+"^2:"+name)
	if err != nil {
		// A marker without a second parent is not something rig writes, but a
		// rewritten history can produce one; its own tree is the best available
		// answer and is right whenever nothing was merged into it.
		tree, err = repo.RevParse(ctx, marker+":"+name)
	}
	if err != nil {
		return "", false
	}
	return tree, true
}

// stackUnsent is what a prefix is holding that has not left the stackspace —
// the thing that makes a disposable stackspace dangerous.
type stackUnsent struct {
	Commits bool // committed work the upstream repository does not have
	Working bool // edits in the index or the worktree, not yet committed
	Known   bool // false when the imported baseline cannot be established
}

func (u stackUnsent) any() bool { return u.Commits || u.Working }

// stackUnsentWork compares a prefix against what was last imported into it.
//
// Trees, not commits: a history amended or rebased without changing content has
// nothing to send and must not be reported as though it did.
//
// Unknown, rather than clean, when no baseline can be established — a history
// rewritten past rig's own commits cannot answer, and "nothing to send" is the
// one wrong answer there that loses work.
func stackUnsentWork(ctx context.Context, repo *gitrepo.Repo, name string, dirty []string) stackUnsent {
	u := stackUnsent{}
	// Uncommitted work is the most easily lost of all, and needs no baseline.
	prefix := name + "/"
	for _, p := range dirty {
		if strings.HasPrefix(p, prefix) {
			u.Working = true
			break
		}
	}
	imported, known := stackImportedTree(ctx, repo, name)
	if !known {
		return u
	}
	here, err := repo.RevParse(ctx, "HEAD:"+name)
	if err != nil {
		return u
	}
	u.Known = true
	u.Commits = imported != here
	return u
}

// stackResolveUpstream turns a prefix's pin into the upstream commit to import.
// A branch or tag is looked up on the remote; a commit is already the answer,
// which is also why a pinned prefix needs no network round trip to know it has
// nothing to pull.
// stackPinnedCursor is the commit a prefix is already pinned to, when its pin
// has been resolved before under this exact selector.
//
// Without it a tag is looked up afresh on every command, so an upstream that
// force-moves or re-cuts one drags the stackspace along — the single thing a pin
// exists to prevent. Editing the pin changes the recorded selector, so a
// deliberate repin still resolves; `pull --repin` clears the record to follow a
// tag that moved on purpose.
func stackPinnedCursor(m *stackManifest, name string) (string, bool) {
	pin := m.pin(name)
	if !pin.pinned() {
		return "", false
	}
	cursor := m.cursor(name)
	if cursor == "" || m.LastPin[name] != pin.describe() {
		return "", false
	}
	return cursor, true
}

// stackUpstreamTip is the commit a prefix should be at: its pin if that is
// already settled, otherwise whatever the pin resolves to upstream now.
func stackUpstreamTip(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, name string) (string, error) {
	if cursor, ok := stackPinnedCursor(m, name); ok {
		return cursor, nil
	}
	return stackResolveUpstream(ctx, repo, stackRemoteURL(m.Repos[name].Upstream), m.pin(name))
}

func stackResolveUpstream(ctx context.Context, repo *gitrepo.Repo, url string, pin stackPin) (string, error) {
	switch pin.Kind {
	case "commit":
		return pin.Value, nil
	case "tag":
		ref := "refs/tags/" + pin.Value
		// An annotated tag resolves to a tag object rather than a commit, and its
		// peeled entry is the commit. josh serves commits, and the cursor records
		// one, so the peeled value wins wherever it exists.
		found, err := repo.LsRemoteRefs(ctx, url, ref, ref+"^{}")
		if err != nil {
			return "", err
		}
		for _, k := range []string{ref + "^{}", ref} {
			if sha := found[k]; sha != "" {
				return sha, nil
			}
		}
		return "", fmt.Errorf("ls-remote %s: tag %q not found", url, pin.Value)
	default:
		return repo.LsRemote(ctx, url, "refs/heads/"+pin.Value)
	}
}

// stackPullOne imports or updates one repo's prefix: probe upstream's tip, stop at
// the cursor (idempotent, the josh-sync NothingToPull check), else fetch that
// exact SHA through the filter, merge, and commit the moved cursor with it.
func stackPullOne(ctx context.Context, out io.Writer, repo *gitrepo.Repo, bin string, src *cfgfind.Source, m *stackManifest, name string, initial bool) error {
	r := m.Repos[name]
	tip, err := stackUpstreamTip(ctx, repo, m, name)
	if err != nil {
		return err
	}
	if !initial && tip == m.cursor(name) {
		fmt.Fprintf(out, "%s: nothing to pull\n", name)
		return nil
	}
	host, path := stackSplitHost(r.Upstream)
	proxy, err := startJoshProxy(ctx, bin, host)
	if err != nil {
		return err
	}
	defer proxy.stop()
	verb := "pulled"
	msg := fmt.Sprintf("stack: pull %s @ %s", name, short(tip))
	if initial {
		verb = "imported"
		msg = fmt.Sprintf("stack: import %s @ %s", name, short(tip))
	}
	// The engine fetches upstream with whatever credentials its own client
	// presents, so reaching a private repo means forwarding ours to it. This is
	// the same credential git would use fetching that host directly, scoped to
	// the proxy's URL so a redirect cannot carry it anywhere else, and absent
	// entirely when no helper has one. Whether the repo actually needs it is not
	// knowable without asking the forge, so it rides along either way.
	auth, err := gitrepo.CredentialFor(ctx, stackRemoteURL(r.Upstream))
	if err != nil {
		return err
	}
	if auth != nil {
		auth.URLPrefix = proxy.base()
	}
	// The URL pins the upstream commit, and josh serves a pinned commit as HEAD
	// rather than under its branch name.
	// Read before the merge: afterwards the newest import marker is the commit
	// this pull is about to make, and the baseline would be the target itself.
	preTree, _ := repo.RevParse(ctx, "HEAD:"+name)
	preImported, preKnown := stackImportedTree(ctx, repo, name)

	conflicted, err := repo.FetchMergeUnrelated(ctx, proxy.url(path, tip, stackPrefixFilter(name)), "HEAD", msg, auth)
	if err != nil {
		if tail := proxy.tail(15); tail != "" {
			return fmt.Errorf("%w\n--- josh-proxy log:\n%s", err, tail)
		}
		return err
	}
	if conflicted {
		return fmt.Errorf("merge conflicts under %s/ — resolve, commit, then re-run to move the cursor", name)
	}

	// A merge cannot move a prefix backwards. Repin a project to an older tag or
	// commit and the target is already an ancestor of what is here, so the merge
	// reports nothing to do — and recording the cursor anyway would claim a
	// revision the directory does not contain, with `status` reporting the pin
	// while the sources stay newer.
	//
	// FETCH_HEAD is the prefixed target that was just fetched, so its tree under
	// the prefix is what this directory is supposed to hold.
	want, wantErr := repo.RevParse(ctx, "FETCH_HEAD:"+name)
	have, haveErr := repo.RevParse(ctx, "HEAD:"+name)
	if wantErr == nil && haveErr == nil && want != have {
		// Replacing the directory discards whatever is under it, so only do it
		// when there is nothing of the user's to discard. Their own commits would
		// survive in the history but be stranded there, which is a quiet way to
		// lose work.
		if !preKnown || preImported != preTree {
			return fmt.Errorf("%s/ holds changes of its own, and moving it to %s (%s) needs the directory replaced\n"+
				"send them first, or revert them, and run this again",
				name, short(tip), m.pin(name).describe())
		}
		if err := repo.ReplacePath(ctx, "FETCH_HEAD", name); err != nil {
			return fmt.Errorf("moving %s to %s: %w", name, m.pin(name).describe(), err)
		}
		verb = "moved"
	}

	// The cursor is written to disk before it can be committed, so keep the
	// bytes: a failure between here and the amend would otherwise leave the
	// cursor advanced past history that does not contain it, and every later
	// status and pull would believe this revision was already synced.
	before, readErr := os.ReadFile(src.File)
	restore := func() {
		if readErr == nil {
			_ = os.WriteFile(src.File, before, 0o644)
		}
	}
	if err := stackSetCursor(src, m, name, tip); err != nil {
		return err
	}
	if err := repo.StageAll(ctx); err != nil {
		restore()
		return err
	}
	// Amend the cursor edit into the merge commit, so one commit carries both
	// the history and the fact that it was synced — the reviewable unit a
	// cron-driven pull PR is built from.
	if _, err := repo.CommitAmendNoEdit(ctx); err != nil {
		restore()
		delete(m.LastSync, name)
		return err
	}
	fmt.Fprintf(out, "%s: %s upstream %s\n", name, verb, short(tip))
	return nil
}

func newStackSendCmd() *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:   "send <repo> <new-branch>",
		Short: "Put a repo's stackspace changes on your fork as a PR-ready branch",
		Long: "Takes this stackspace's version of <repo> and commits it on top of that\n" +
			"project's upstream tip, as <new-branch> on your fork. The branch holds\n" +
			"one commit whose diff is exactly what the stackspace changed, with none of\n" +
			"the stackspace's own history: nothing upstream has to know this repo is\n" +
			"fused with anything else. PR from there as usual.\n\n" +
			"<new-branch> is a branch you are creating on your fork, named per change\n" +
			"(read-timeout). It is unrelated to the manifest's upstreamBranch, which\n" +
			"is the branch of *upstream* this directory follows. Sending twice to the\n" +
			"same <new-branch> updates it, so an open PR can take review feedback.\n\n" +
			"The name is prefixed with `stack/` so these branches stay recognisable\n" +
			"among your own work on the same fork: `send lib read-timeout` creates\n" +
			"stack/read-timeout. Change it with the manifest's branchPrefix, or set\n" +
			"that to \"\" for bare names.",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: stackRepoCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			m, _, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			// The prefix keeps these branches recognisable on a fork that also
			// carries your own work, and is applied here rather than at the call
			// sites so the menu and the CLI cannot disagree about it.
			branch := m.sendBranch(name, args[1])
			r := m.Repos[name]
			if r == nil {
				if err := m.requireRepos(); err != nil {
					return err
				}
				return fmt.Errorf("no stack repo %q (have: %s)", name, strings.Join(m.names(), ", "))
			}
			if m.cursor(name) == "" {
				return fmt.Errorf("%s is not imported yet — run `rig stack init`", name)
			}
			if dirty, err := repo.Dirty(ctx); err != nil {
				return err
			} else if dirty {
				return fmt.Errorf("stackspace has uncommitted changes — commit them before sending")
			}

			// The prefix directory is this project as the stackspace has it, and
			// it is already free of the prefix inside: it is the tree upstream
			// wants, needing no filter to extract.
			tree, err := repo.RevParse(ctx, "HEAD:"+name)
			if err != nil {
				return fmt.Errorf("%s is not a directory in this stackspace: %w", name, err)
			}

			// Root it on the upstream tip so the branch's one commit shows only
			// what changed, and so it merges without the fork's history.
			if tree == "" {
				return fmt.Errorf("%s has no content at HEAD", name)
			}
			upstreamURL := stackRemoteURL(r.Upstream)
			tip, err := stackUpstreamTip(ctx, repo, m, name)
			if err != nil {
				return err
			}
			// The stackspace tree is a snapshot taken at the cursor. Rooting it on
			// a tip that has moved past the cursor would present every upstream
			// commit in between as though this branch had undone it.
			if tip != m.cursor(name) {
				return fmt.Errorf("upstream %s has moved to %s since this stackspace last pulled (%s)\n"+
					"sending now would revert those commits — run `rig stack pull %s` first",
					r.Upstream, short(tip), short(m.cursor(name)), name)
			}
			if err := repo.FetchObjects(ctx, upstreamURL, tip); err != nil {
				return err
			}

			// A new commit object never equals its parent, so the no-op has to be
			// read off the trees: same tree as upstream means nothing to send.
			tipTree, err := repo.RevParse(ctx, tip+"^{tree}")
			if err != nil {
				return err
			}
			if tipTree == tree {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: nothing to send — it matches upstream\n", name)
				return nil
			}

			// Local: message is the flag variable, and writing the default back
			// into it would leak this repo's message into the next send.
			msg := message
			if msg == "" {
				// Nothing about the stackspace belongs in a commit an upstream
				// maintainer reads — least of all the local directory it lives in.
				msg = fmt.Sprintf("Changes to %s", name)
			}
			commit, err := repo.CommitTree(ctx, tree, tip, msg)
			if err != nil {
				return err
			}

			// Each send synthesizes a fresh commit parented on the upstream tip,
			// so a second send to the same branch is a sibling of the first and a
			// plain push is refused as non-fast-forward — which would make it
			// impossible to update an open PR. Replace under a lease instead, so
			// the push still fails if someone else moved the branch meanwhile.
			if err := repo.PushRefForce(ctx, stackRemoteURL(r.Fork), commit, "refs/heads/"+branch); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sent %s to %s:%s — open the PR against %s\n",
				name, r.Fork, branch, r.Upstream)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message for the branch")
	return cmd
}

// newStackPushCmd exports a member you own back to its own repository, keeping
// the history that `send` deliberately discards.
func newStackPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <repo>",
		Short: "Fast-forward a repo you own with this stackspace's commits, history intact",
		Long: "For a project marked `\"owned\": true` in the manifest — one of yours,\n" +
			"not a fork you contribute to. Extracts everything the stackspace has done\n" +
			"under <repo>/ and fast-forwards that project's own branch with it.\n\n" +
			"Unlike `send`, nothing is squashed. Each stackspace commit that touched\n" +
			"<repo>/ arrives as its own commit, with its message, parented on what\n" +
			"upstream already had — so a change spanning several projects lands as a\n" +
			"matching commit in each of them. Commits that touched nothing under\n" +
			"<repo>/ do not appear at all.\n\n" +
			"`send` is the verb for someone else's project: it proposes one squashed\n" +
			"commit on a branch of your fork, which is what a reviewer wants and the\n" +
			"wrong thing entirely for a repository that is yours.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: stackRepoCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			out := cmd.OutOrStdout()
			m, src, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			r := m.Repos[name]
			switch {
			case r == nil && m.requireRepos() != nil:
				return m.requireRepos()
			case r == nil:
				return fmt.Errorf("no stack repo %q (have: %s)", name, strings.Join(m.names(), ", "))
			case m.cursor(name) == "":
				return fmt.Errorf("%s is not imported yet — run `rig stack init`", name)
			case !r.Owned:
				return fmt.Errorf("%s is not marked as yours — push fast-forwards a project's own branch, which is only right for a repo you own\n"+
					"set \"owned\": true on %s in the manifest, or use `rig stack send %s <branch>` to propose the change to its fork instead",
					name, name, name)
			case m.pin(name).pinned():
				// A pin names a fixed point in history; there is no branch there to
				// move, and advancing the cursor past it would contradict the pin.
				return fmt.Errorf("%s is pinned to %s — there is no branch to fast-forward.\n"+
					"replace the pin with upstreamBranch to follow a branch again", name, m.pin(name).describe())
			}
			if dirty, err := repo.Dirty(ctx); err != nil {
				return err
			} else if dirty {
				return fmt.Errorf("stackspace has uncommitted changes — commit them before pushing")
			}

			upstreamURL := stackRemoteURL(r.Upstream)
			branch := m.branch(name)
			tip, err := repo.LsRemote(ctx, upstreamURL, "refs/heads/"+branch)
			if err != nil {
				return err
			}
			// Same guard as send, for the same reason: the stackspace holds a
			// snapshot taken at the cursor, and building on anything else would
			// present upstream's own commits as though this push had undone them.
			if tip != m.cursor(name) {
				return fmt.Errorf("upstream %s has moved to %s since this stackspace last pulled (%s)\n"+
					"pushing now would revert those commits — run `rig stack pull %s` first",
					r.Upstream, short(tip), short(m.cursor(name)), name)
			}

			// Both engines before the push, not after: the stackspace has to take
			// back what it sends (see below), and discovering a missing binary
			// once the remote has already moved would leave exactly the split
			// state this is trying to avoid.
			filter, err := ensureJoshTool(ctx, m.joshVersion(), toolFilter, out)
			if err != nil {
				return err
			}
			proxy, err := ensureJoshTool(ctx, m.joshVersion(), toolProxy, out)
			if err != nil {
				return err
			}
			// :/<name> is the exact inverse of the :prefix=<name> this was imported
			// with, so the shared history filters back to upstream's own commit ids
			// and what is left on top is a fast-forward rather than a fork of it.
			ref := "refs/rigsmith/push/" + name
			if err := stackRunJoshFilter(ctx, filter, repo.Dir, ":/"+name, ref); err != nil {
				return err
			}
			head, err := repo.RevParse(ctx, ref)
			if err != nil {
				return err
			}
			if head == tip {
				fmt.Fprintf(out, "%s: nothing to push — it matches %s\n", name, r.Upstream)
				return nil
			}

			// No force and no lease: a push that is not a fast-forward means the
			// filtered history is not a continuation of upstream's, and overwriting
			// is never the right answer to that.
			if err := repo.PushRef(ctx, upstreamURL, head, "refs/heads/"+branch); err != nil {
				return fmt.Errorf("pushing %s to %s:%s: %w", name, r.Upstream, branch, err)
			}

			// Take back what we just sent, rather than only recording the cursor.
			//
			// The filtered commit is a different object from the stackspace commit
			// that produced it — same content under a different prefix and different
			// parents — so the stackspace does not contain it. Left that way, the
			// next pull that finds upstream moved re-imports our own commits as a
			// parallel line of development: a duplicate in the log at best, and a
			// conflict as soon as the same file has been touched since.
			//
			// Importing it here is the moment it costs nothing. The content is
			// identical to what the stackspace already has, because we just sent it,
			// so the merge is trivial — and from now on the prefixed commits are
			// ancestors and later pulls are ordinary.
			if err := stackPullOne(ctx, io.Discard, repo, proxy, src, m, name, false); err != nil {
				return fmt.Errorf("%s was pushed to %s:%s, but the stackspace could not take it back: %w\n"+
					"run `rig stack pull %s` — until then this stackspace still has the change only in its own shape",
					name, r.Upstream, branch, err, name)
			}
			fmt.Fprintf(out, "pushed %s to %s:%s (%s)\n", name, r.Upstream, branch, short(head))
			return nil
		},
	}
	return cmd
}

// stackRunJoshFilter rewrites the stackspace's HEAD through filter, leaving the
// result at ref. The engine works in place on the repository it is pointed at,
// touching no branch of its own, so the caller decides what happens next.
func stackRunJoshFilter(ctx context.Context, bin, dir, filter, ref string) error {
	cmd := exec.CommandContext(ctx, bin, filter, "--update", ref, "HEAD")
	cmd.Dir = dir
	var errb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("josh-filter %s: %w: %s", filter, err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// newStackPushMenuCmd is `push` for the menu: only projects marked as yours can
// be pushed, so the picker offers those and says so when there are none, rather
// than letting someone choose a repo the verb will refuse.
func newStackPushMenuCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "push",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			m, _, _, err := stackspace(cmd.Context())
			if err != nil {
				return err
			}
			owned := make([]string, 0, len(m.Repos))
			for _, n := range m.names() {
				if r := m.Repos[n]; r != nil && r.Owned && !m.pin(n).pinned() {
					owned = append(owned, n)
				}
			}
			if len(owned) == 0 {
				fmt.Fprintln(out, DimStyle.Render(`no repos here are marked "owned" — push fast-forwards a project's own branch, which is only right for one of yours`))
				return nil
			}
			name := owned[0]
			if len(owned) > 1 {
				opts := make([]huh.Option[string], 0, len(owned))
				for _, n := range owned {
					opts = append(opts, huh.NewOption(fmt.Sprintf("%s  →  %s", n, m.Repos[n].Upstream), n))
				}
				if err := huh.NewSelect[string]().
					Title("Push which repo?").Options(opts...).Filtering(true).Value(&name).Run(); err != nil {
					return err
				}
			}
			sub := newStackPushCmd()
			sub.SetContext(cmd.Context())
			sub.SetOut(out)
			sub.SetErr(cmd.ErrOrStderr())
			return sub.RunE(sub, []string{name})
		},
	}
}

// newStackWireCmd writes the build overlay a stackspace needs, from what the
// ecosystem adapters already know about the projects in it.
func newStackWireCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wire",
		Short: "Write the build overlay so members resolve each other from source",
		Long: "Works out which package references cross from one member of the stackspace\n" +
			"to another — those are the ones that would otherwise come from a registry —\n" +
			"and writes the build file that points them at the sources instead.\n\n" +
			"Nothing in any project file changes, so a member cloned on its own still\n" +
			"builds from packages exactly as it did. Re-run it after adding a member or\n" +
			"after a dependency moves; it rewrites its own file and refuses to touch one\n" +
			"you wrote yourself.\n\n" +
			"`rig stack doctor` reports the same findings without writing anything.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			m, _, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			byEco, orphans := stackRedirects(ctx, repo.Dir, m.names())
			// Patching a member's own build file is a commit to that repository,
			// and it travels back through `push` or `send`. Your own repos want
			// that line; a fork you contribute to should not carry rig plumbing
			// into somebody else's pull request.
			writable := m.ownedNames()
			// Reported before anything is written: a member nothing consumes is
			// usually why there was less to wire than expected.
			stackReportOrphans(out, m, orphans)
			if len(byEco) == 0 {
				fmt.Fprintln(out, "no package references cross between members — nothing to wire")
				return nil
			}
			for _, eco := range ecosystem.Default().All() {
				links := byEco[eco.Info().ID]
				if len(links) == 0 {
					continue
				}
				resp, err := eco.LocalOverlay(ctx, plugin.LocalOverlayRequest{
					Root: repo.Dir, Redirects: redirectsOf(links), Write: true, Writable: writable,
				})
				if err != nil {
					return err
				}
				if resp.Skipped {
					fmt.Fprintf(out, "· %s: %s\n", eco.Info().ID, resp.Reason)
					continue
				}
				for f := range resp.Files {
					fmt.Fprintf(out, "✓ %s — %d package(s) now resolve from this stackspace\n", f, len(links))
				}
				for _, l := range links {
					fmt.Fprintf(out, "    %s\n", l.describe())
				}
				for _, f := range resp.Fixed {
					fmt.Fprintf(out, "✓ %s — patched to stop hiding the overlay from what is under it\n", f)
				}
				// Problems the overlay cannot fix by existing. Reported here as
				// well as in doctor, because a wire that looks like it worked and
				// silently did not is the thing this whole path exists to stop.
				for _, p := range resp.Problems {
					fmt.Fprintf(out, "  ✗ %s — %s\n", p.Path, p.Message)
				}
			}
			return nil
		},
	}
	return cmd
}

// stackReportOrphans names fused repos nothing here consumes. An app is a leaf
// and belongs at the end of the graph, so one marked owned is left alone.
func stackReportOrphans(out io.Writer, m *stackManifest, orphans []stackOrphan) {
	for _, o := range orphans {
		if r := m.Repos[o.Member]; r != nil && r.Owned {
			continue
		}
		fmt.Fprintf(out, "· %s\n", o.describe())
		fmt.Fprintf(out, "    either that is not the repo your code depends on, or it moved to a\n")
		fmt.Fprintf(out, "    renamed fork of it — a package is matched by identity, not by origin\n")
	}
}

func newStackDoctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the stack engine and manifest; --fix installs the pinned josh",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			root, err := stackRoot(ctx)
			if err != nil {
				return err
			}
			m, _, err := loadStackManifest(root)
			if err != nil {
				return err
			}
			version := m.joshVersion()
			switch {
			case m == nil:
				fmt.Fprintln(out, "· no stack manifest here (fine outside a stackspace)")
			default:
				fmt.Fprintf(out, "✓ manifest: %d repo(s)\n", len(m.Repos))
			}
			bin, binErr := stackJoshProxyBin(version)
			if binErr == nil {
				binErr = stackJoshInstalled(bin)
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
				fmt.Fprintf(out, "✗ josh-proxy %s not installed — `rig stack doctor --fix` fetches a verified binary (or builds it where none is published)\n", version)
			}

			// The build wiring, which fails silently in every direction: an
			// overlay that was never written, a member whose own build file hides
			// it, a redirect naming a package nothing references. Each of those
			// leaves a build that succeeds against the published package and says
			// nothing, so checking is the only way anyone finds out.
			if m != nil {
				reports, orphans := stackCheckOverlay(ctx, root, m.names(), m.ownedNames())
				stackReportOrphans(out, m, orphans)
				for _, rep := range reports {
					fmt.Fprintf(out, "· %s: %d package(s) cross between members here\n", rep.Eco, len(rep.Links))
					for _, l := range rep.Links {
						fmt.Fprintf(out, "    %s\n", l.describe())
					}
					for _, p := range rep.Resp.Problems {
						where := p.Path
						if where == "" {
							where = "manifest"
						}
						fmt.Fprintf(out, "  ✗ %s — %s\n", where, p.Message)
					}
					if len(rep.Resp.Problems) == 0 {
						fmt.Fprintf(out, "  ✓ nothing found that would stop them\n")
					}
				}
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

// stackRepoCompletion offers the stackspace's repos for the verbs that take one.
func stackRepoCompletion(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx := context.Background()
	if cmd != nil {
		ctx = cmd.Context()
	}
	root, err := stackRoot(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	m, _, err := loadStackManifest(root)
	if err != nil || m == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return m.names(), cobra.ShellCompDirectiveNoFileComp
}

// stackMenuItems are the stack actions for `rig ui`. Without a manifest the
// group is just `init` — hiding it entirely would leave no way to start a
// stackspace from the menu — and outside a git repo it disappears.
func stackMenuItems() []menuItem {
	root, err := stackRoot(context.Background())
	if err != nil {
		return nil
	}
	m, _, err := loadStackManifest(root)
	if err != nil {
		// A manifest that exists but will not load is the scaffold, waiting to
		// be filled in. Dropping the group here would hide `init` at exactly
		// the moment it is the only thing left to do.
		return []menuItem{
			{label: "init", desc: "finish rig.stack.jsonc, then import — " + stackFirstLine(err), cmd: newStackInitCmd()},
		}
	}
	if m == nil {
		return []menuItem{
			{label: "init", desc: "scaffold rig.stack.jsonc to fuse repos here", cmd: newStackInitCmd()},
		}
	}
	// An empty manifest loads fine — it is what init scaffolds — but every other
	// verb acts on repos, and offering seven of them when there are none is a
	// menu that describes the tool rather than what you can do.
	if len(m.Repos) == 0 {
		return []menuItem{
			{label: "add", desc: "add the first repo to this stackspace", cmd: newStackAddCmd()},
			{label: "init", desc: "import the repos the manifest names", cmd: newStackInitCmd()},
		}
	}
	return []menuItem{
		{label: "init", desc: "import any repo the manifest names but has not fused yet", cmd: newStackInitCmd()},
		{label: "add", desc: "add a repo to this stackspace and import it", cmd: newStackAddCmd()},
		{label: "status", desc: "each repo's cursor against its upstream", cmd: newStackStatusCmd()},
		{label: "pull", desc: "merge new upstream commits into every repo", cmd: newStackPullCmd()},
		{label: "send", desc: "a repo's changes to your fork as a new branch (pick, then name it)", cmd: newStackSendMenuCmd()},
		{label: "push", desc: "a repo you own back to its own branch, history intact (pick one)", cmd: newStackPushMenuCmd()},
		{label: "wire", desc: "write the build overlay so members resolve each other from source", cmd: newStackWireCmd()},
		{label: "doctor", desc: "check the engine and manifest", cmd: newStackDoctorCmd()},
	}
}

// stackFirstLine is an error's first line, for a menu row that has one line.
func stackFirstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	if i := strings.LastIndex(line, ": "); i >= 0 && i+2 < len(line) {
		line = line[i+2:]
	}
	if len(line) > 60 {
		line = line[:57] + "…"
	}
	return line
}

// stackCommonPrefix is the branch prefix every named repo shares, or "" when
// they differ — the menu can only promise one when there is one.
func stackCommonPrefix(m *stackManifest, names []string) string {
	if len(names) == 0 {
		return ""
	}
	first := m.branchPrefix(names[0])
	for _, n := range names[1:] {
		if m.branchPrefix(n) != first {
			return ""
		}
	}
	return first
}

// newStackSendMenuCmd is the menu's wrapper around the arg-taking `stack send`:
// the repo comes from the manifest as a pick, the branch from a prompt. Hidden —
// it exists only for the menu, like the worktree new/open/rm wrappers.
func newStackSendMenuCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "send",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, _, _, err := stackspace(cmd.Context())
			if err != nil {
				return err
			}
			names := m.names()
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), DimStyle.Render("no repos in this stackspace"))
				return nil
			}

			name := names[0]
			var branch string
			fields := []huh.Field{}
			if len(names) > 1 {
				opts := make([]huh.Option[string], 0, len(names))
				for _, n := range names {
					opts = append(opts, huh.NewOption(fmt.Sprintf("%s  →  %s", n, m.Repos[n].Fork), n))
				}
				fields = append(fields, huh.NewSelect[string]().
					Title("Send which repo?").Options(opts...).Filtering(true).Value(&name))
			}
			// The prompt cannot be skipped by reading the manifest: this branch is
			// named per change, and the manifest's upstreamBranch is a different
			// thing entirely — the branch of upstream the directory follows.
			//
			// With one repo the destination fork is known now and worth naming;
			// with several it is only decided by the select above, which has not
			// run yet, so stay general rather than name the wrong one.
			where := "your fork"
			if len(names) == 1 {
				where = m.Repos[names[0]].Fork
			}
			// Show the prefix rather than let it surprise them after the fact.
			// It is uniform across repos unless a repo overrides it, so only
			// promise a specific one when every repo here agrees.
			desc := fmt.Sprintf("created on %s, holding one commit", where)
			if prefix := stackCommonPrefix(m, names); prefix != "" {
				desc = fmt.Sprintf("created on %s as %s<name> — e.g. read-timeout", where, prefix)
			}
			fields = append(fields, huh.NewInput().Title("New branch on your fork").
				Description(desc).
				Placeholder("read-timeout").
				Value(&branch))

			if err := huh.NewForm(huh.NewGroup(fields...)).
				WithKeyMap(huhEscKeyMap()).WithTheme(rigTheme()).Run(); err != nil {
				if errors.Is(err, huh.ErrUserAborted) {
					return nil
				}
				return err
			}
			if branch = strings.TrimSpace(branch); branch == "" {
				return nil
			}

			sub := newStackSendCmd()
			sub.SetContext(cmd.Context())
			sub.SetOut(cmd.OutOrStdout())
			sub.SetErr(cmd.ErrOrStderr())
			return sub.RunE(sub, []string{name, branch})
		},
	}
}
