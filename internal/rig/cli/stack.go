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
	"github.com/rigsmith/rigsmith/core/gitrepo"
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
			"  rig stack setup                     set up a fresh clone: engine, members, overlay\n" +
			"  rig stack init                      scaffold the manifest / import the repos\n" +
			"  rig stack add [upstream]            add a repo and import it (asks if not given)\n" +
			"  rig stack rm <repo>                 remove a repo: manifest, tree and overlay\n" +
			"  rig stack seed <dir>                a small repo of just the root files, to rebuild from elsewhere\n" +
			"  rig stack status                    cursor vs upstream, per repo\n" +
			"  rig stack pull [repo]               merge new upstream commits (all by default)\n" +
			"  rig stack propose [repo] [branch]   a branch on your fork, prefixed stack/ (asks)\n" +
			"  rig stack push [repo]               fast-forward a repo you own, history intact\n" +
			"  rig stack wire                      write the build overlay for the members\n" +
			"  rig stack pack [repo]               build a member's packages here, where the overlay applies\n" +
			"  rig stack doctor                    engine + manifest checks (--fix installs josh)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if stdinStdoutTTY() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newStackSetupCmd(), newStackInitCmd(), newStackAddCmd(), newStackRemoveCmd(), newStackSeedCmd(), newStackStatusCmd(), newStackPullCmd(), newStackSendCmd(), newStackPushCmd(), newStackWireCmd(), newStackPackCmd(), newStackDoctorCmd())
	return refuseUnknownVerb(cmd)
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
			"history through the :prefix filter and merges it in.\n\n" +
			"A repo whose cursor is recorded but whose directory is missing — a clone\n" +
			"of `rig stack seed`'s output, or of the root files alone — is rebuilt at\n" +
			"the commit the cursor names, so the stackspace comes back as it was left.\n" +
			"A repo with `trackBranch` set, or one last proposed to a branch that still\n" +
			"exists on its fork, is rebuilt from that branch instead, which is where\n" +
			"work that has left as a proposal and not yet merged actually lives.",
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
			} else if dirty {
				if _, only := stackOnlyManifestDirty(ctx, repo, src); !only {
					return fmt.Errorf("stackspace has uncommitted changes — commit or stash before importing")
				}
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
			// Two kinds of member need importing: one with no cursor, which has
			// never been imported, and one with a cursor but no directory —
			// a seed (`rig stack seed`) or any clone of the root files alone,
			// where the manifest remembers what each prefix held and the tree
			// does not have it. The second is reconstituted at what the manifest
			// recorded, not at upstream's tip, so it comes back as it was left.
			names, rebuild := []string{}, map[string]bool{}
			for _, name := range m.names() {
				switch {
				case m.cursor(name) == "":
					names = append(names, name)
				case !stackPrefixPresent(ctx, repo, name):
					names = append(names, name)
					rebuild[name] = true
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
				opts := stackPullOpts{initial: true}
				if rebuild[name] {
					opts.at = m.cursor(name)
				}
				if opts.fork, err = stackImportFromFork(ctx, repo, m, name, rebuild[name]); err != nil {
					return err
				}
				if err := stackPullOne(ctx, cmd.OutOrStdout(), repo, bin, src, m, name, opts); err != nil {
					return fmt.Errorf("importing %s: %w", name, err)
				}
				imported++
			}
			if imported == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "nothing to import — every repo has a cursor; use `rig stack pull` for updates")
			}
			// A seed clone is a manifest, a build overlay, and nothing that says
			// what either is — so the next person reads a build file pointing at
			// directories that are not there and concludes the repo is broken.
			// Best-effort: failing to write a README must not fail an import that
			// worked.
			switch wrote, err := writeStackReadme(root, m); {
			case err != nil:
				// Best-effort, but not silent: the import worked and the
				// guidance it promises is missing, and only this line says so.
				fmt.Fprintf(cmd.ErrOrStderr(), "could not write README.md: %v\n", err)
			case wrote:
				fmt.Fprintln(cmd.OutOrStdout(), "wrote README.md — what this is, and how to set it up")
				// Committed, and by path alone. init refuses a dirty tree, and
				// so do pull and propose — so a file this verb generates and
				// leaves loose makes the next verb refuse over something the
				// user never wrote. A rebuild from a seed hits it immediately:
				// the heading follows the directory name, so a clone under a
				// different one rewrites the file and nothing works again
				// until someone commits it.
				if _, err := repo.CommitPaths(ctx, "stack: README", "README.md"); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "could not commit README.md: %v\n", err)
				}
			}
			return nil
		},
	}
	return cmd
}

// stackOnlyManifestDirty reports whether a dedicated manifest file is the only
// uncommitted thing, and names it relative to the stackspace root when so.
// Filling in the scaffolded rig.stack.jsonc and running init again is the
// documented first run, so that one file must not trip init's dirty guard —
// the import commits it anyway. pull keeps its guard, and uses the name to say
// which file is in the way.
func stackOnlyManifestDirty(ctx context.Context, repo *gitrepo.Repo, src *cfgfind.Source) (string, bool) {
	// Only a dedicated manifest earns the exemption. An inline `stack` block
	// shares .rig.json with every other rig setting, so waving that file
	// through would commit whatever else the user happened to be editing.
	if src == nil || src.File == "" || src.Path == "" {
		return "", false
	}
	paths, err := repo.DirtyPaths(ctx)
	if err != nil || len(paths) == 0 {
		return "", false
	}
	manifest, err := filepath.Rel(repo.Dir, src.File)
	if err != nil {
		return "", false
	}
	manifest = filepath.ToSlash(manifest)
	for _, p := range paths {
		if filepath.ToSlash(p) != manifest {
			return "", false
		}
	}
	return manifest, true
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
				case !stackPrefixPresent(ctx, repo, name):
					// A cursor with no directory under it: an import that
					// recorded a revision it never brought in, or a removal
					// that left the manifest entry. "up to date" would be
					// what the cursor says, and the tree says otherwise.
					state = fmt.Sprintf("cursor at %s but no %s/ directory — `rig stack setup` reconstitutes it", short(m.cursor(name)), name)
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
					state += fmt.Sprintf("  ·  uncommitted and unsent changes — commit, then `rig stack propose %s <branch>`", name)
				case u.Working:
					state += "  ·  uncommitted changes"
				case u.Commits:
					state += fmt.Sprintf("  ·  unsent changes — `rig stack propose %s <branch>`", name)
				case !u.Known && m.cursor(name) != "":
					state += "  ·  cannot tell whether it has unsent changes (no import commit in this history)"
				}
				// Staleness is NOT summarised on this line: the per-topic listing
				// below names each stale topic where its own entry is, which is
				// where a reader looks for it. Saying it twice was an artefact of
				// the listing arriving after the summary did.
				//
				// `propose` sends the prefix's WHOLE divergence, not the change you
				// have in mind, so a second pull request for this repo would carry
				// the first one's too. Nothing else says so, and the place it is
				// otherwise discovered is a maintainer asking why the diff touches
				// something unrelated.
				//
				// Said, not counted. A count needs a range, and every range against
				// the integration line is wrong here: the newest import marker sits
				// on top of the fixes, so `marker..HEAD` omits all of them. The
				// sentence was the point anyway.
				if u.Commits || u.Proposed {
					state += "  ·  `propose` sends this prefix's whole divergence (--from <branch> for one topic)"
				}
				fmt.Fprintf(out, "%-24s %-10s %s\n", name, short(m.cursor(name)), state)
				// Then each topic in flight for this member, and where it went.
				// Existence, reach and staleness are asked of the repository; only
				// the destination comes from the manifest, because only that is
				// unknowable from here.
				topics, terr := stackTopics(ctx, repo, m, name)
				if terr != nil {
					// Said, not swallowed: an empty list here would read as "no
					// topics in flight", which is the opposite of "cannot tell".
					fmt.Fprintf(out, "  (cannot list topics for %s — %s)\n", name, stackFirstLine(terr))
				}
				for _, t := range topics {
					where := "not proposed yet"
					if pr, ok := m.Proposals[name][t.Name]; ok && pr.Branch != "" {
						where = "→ " + m.Repos[name].Fork + ":" + pr.Branch
						// Recorded against a branch NAME, and names get reused —
						// so what was proposed is compared with what is there now.
						// Catches a topic recreated under an old name, and the
						// commoner case of a pull request left behind its branch.
						if tip, terr := repo.RevParse(ctx, t.Name); terr == nil && pr.Commit != "" && tip != pr.Commit {
							where += "  (branch has moved since; propose again to update it)"
						}
					}
					if t.Stale {
						where += "  (rooted before the last pull — `propose --from` refuses it)"
					}
					fmt.Fprintf(out, "  %-30s %s\n", t.Name, where)
				}
			}
			// The whole point of the convention is not having to remember what is
			// in flight. Only the conventionally-named ones: a branch the user
			// named themselves is theirs, and guessing at it would be listing
			// their work back at them.
			if topics, terr := repo.BranchesWithPrefix(ctx, stackTopicPrefix); terr == nil && len(topics) > 0 {
				fmt.Fprintf(out, "\ntopics in flight: %s\n", strings.Join(topics, ", "))
				fmt.Fprintf(out, "  propose one with `rig stack propose <repo> <name> --from %s`\n", strings.TrimPrefix(topics[0], stackTopicPrefix))
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
				// The manifest is what a pull reads, and a pin that stopped
				// resolving — upstream renamed or deleted the branch — is fixed
				// by editing it. That edit is then the thing tripping this guard,
				// which read cold says the fix was wrong. Name the file and the
				// step between it and the pull.
				if manifest, ok := stackOnlyManifestDirty(ctx, repo, src); ok {
					// -a rather than a pathspec: the guard has just established
					// that the manifest is the only thing dirty, and a pathspec
					// would be relative to wherever inside the stackspace the
					// user is standing.
					return fmt.Errorf("stackspace has uncommitted changes — only %s; commit it (`git commit -am \"stack: manifest\"`), then pull again", manifest)
				}
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
					// The cursor is the only thing a pull consults, and one
					// left over a missing directory would be reported as
					// current forever. init is the verb that rebuilds a
					// prefix at its cursor, so say so rather than "nothing".
					if !stackPrefixPresent(ctx, repo, name) {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: cursor at %s but no %s/ directory — `rig stack setup` reconstitutes it\n", name, short(tip), name)
						continue
					}
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
				if err := stackPullOne(ctx, cmd.OutOrStdout(), repo, bin, src, m, name, stackPullOpts{}); err != nil {
					return fmt.Errorf("pulling %s: %w", name, err)
				}
			}
			// A pull moves the prefix on; a topic branch does not come with it,
			// and its tree is now upstream as it USED to be. propose refuses such
			// a topic, but only when you next reach for it — which may be days
			// later, with no memory of the pull that caused it. Say it here.
			//
			// Deliberately NOT rebased automatically. The obvious target is the
			// commit this pull just made, and that is wrong: an import merges into
			// HEAD, and HEAD has already merged your topics, so re-rooting onto it
			// folds the very fix the topic isolates back into it. Re-rooting a
			// topic correctly means replaying it onto upstream's new tree with
			// none of the integration line's fixes, which is a separate piece of
			// work and not a flag.
			for _, name := range moved {
				stale, serr := stackStaleTopicNames(ctx, repo, m, name)
				if serr != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "\n%s: cannot tell which topics this pull left behind — %s\n", name, stackFirstLine(serr))
					continue
				}
				if len(stale) == 0 {
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "\n%s: %d topic(s) rooted before this pull — `propose --from` will refuse them until re-rooted:\n", name, len(stale))
				for _, t := range stale {
					fmt.Fprintf(cmd.OutOrStdout(), "    %s\n", t)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  branch again from the new import and replay the fix; proposing as-is would revert what upstream landed.\n")
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
	marker := stackImportCommit(ctx, repo, name)
	if marker == "" {
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
	// Proposed is set when Commits is clear only because propose has put
	// this tree on the fork — a claim the stackspace's own history cannot
	// make, and one stackProposedOnFork can check before anything acts on it.
	Proposed bool
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
	// Work that propose has put on the fork has left, even though nothing in
	// this history records it: propose keeps the commit it pushed under a
	// ref, and a prefix holding exactly that tree has nothing left to send.
	if u.Commits {
		for _, ref := range []string{"refs/rigsmith/propose/", "refs/rigsmith/integration/"} {
			// The integration ref matters for a prefix proposed with --from: the
			// topic's branch holds part of the divergence, so the propose ref
			// legitimately does not match, while trackBranch holds all of it and
			// the work has in fact left.
			if sent, err := repo.RevParse(ctx, ref+name+"^{tree}"); err == nil && sent == here {
				u.Commits, u.Proposed = false, true
				break
			}
		}
	}
	return u
}

// stackImportCommit is the commit that last brought a member's prefix up to
// upstream — rig's own import/pull marker.
//
// This, and NOT the cursor, is what local ancestry is measured against. The
// cursor is a raw upstream commit, while an import merges josh-REWRITTEN
// content, so the upstream commit itself is an ancestor of nothing in this
// history. Verified against a real stackspace: all three members' cursors
// answered "not an ancestor of HEAD", which measured that way would have made
// every topic look stale and refused every `propose --from`.
func stackImportCommit(ctx context.Context, repo *gitrepo.Repo, name string) string {
	// One walk down HEAD's first-parent line — where pulls land — taking the
	// first commit that is a sync of this prefix, by either of the two things
	// a sync can be recognised by.
	//
	// By shape: a merge whose second parent is a filtered commit of the
	// prefix, a tree holding the prefix directory and nothing else, which is
	// what josh's :prefix filter yields and no commit made in the stackspace
	// looks like, since each carries the manifest at the root. rig gives the
	// merges it makes a marker subject, but a pull that conflicts is finished
	// by the user under whatever subject they choose, and a baseline read
	// from subjects alone stopped at the sync before — which is where the
	// cursor no longer was. Upstream's own merges would pass the same test,
	// but they sit behind second parents and the first-parent walk never
	// reaches them.
	//
	// By subject: a sync that made no merge — a repin backwards, a removed
	// member restored — is a plain commit, and its marker subject is the only
	// thing that says what it was.
	marker := regexp.MustCompile(`^stack: (import|pull|push) ` + regexp.QuoteMeta(name) + ` @`)
	if commits, err := repo.FirstParentCommits(ctx, "HEAD"); err == nil {
		for _, c := range commits {
			if len(c.Parents) > 1 {
				if names, err := repo.TopLevelNames(ctx, c.Parents[1]); err == nil && len(names) == 1 && names[0] == name {
					return c.Hash
				}
			}
			if marker.MatchString(c.Subject) {
				return c.Hash
			}
		}
	}
	// Anywhere else in the history is the fallback: a marker merged in from a
	// side branch sits off the first-parent line.
	found, err := repo.LastCommitMatching(ctx, marker.String())
	if err != nil {
		return ""
	}
	return found
}

// stackTopicTouches reports whether a topic changes a member at all, by
// comparing its prefix tree against the import's.
//
// Content, NOT history. The workflow merges topics into the integration line, so
// a topic's commits are ancestors of the newest import marker and any
// `marker..topic` range is empty — a range test called every topic irrelevant to
// every member the moment it was merged, which is to say always.
func stackTopicTouches(ctx context.Context, repo *gitrepo.Repo, base, topic, name string) (bool, error) {
	baseTree, berr := repo.RevParse(ctx, base+":"+name)
	if berr != nil {
		// The import has no such directory, which is a stackspace this member is
		// not really in — nothing to compare, and nothing wrong either.
		return false, nil
	}
	topicTree, terr := repo.RevParse(ctx, topic+":"+name)
	if terr != nil {
		// The topic does not carry this member's directory at all: absence, not
		// failure. It is the ordinary case for a topic belonging to another member.
		return false, nil
	}
	return baseTree != topicTree, nil
}

// stackTopic is one in-flight topic branch of this stackspace, as it stands
// against a member.
type stackTopic struct {
	Name string
	// Stale is set when the topic predates the member's latest import: its
	// prefix tree is upstream as it USED to be, so proposing it would present
	// everything upstream landed since as reverted.
	Stale bool
}

// stackTopics lists the conventionally-named topic branches that touch a member,
// and says which of them a pull has left behind.
//
// One traversal answering both questions. They were two functions, each listing
// the branches and finding the import again — not merely twice the work, since
// two derivations of one thing can disagree, and `status` printed both.
//
// Only the conventionally-named branches are examined. A branch the user named
// themselves may be anything at all, and calling someone's unrelated work a
// stale topic is worse than saying nothing.
func stackTopics(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, name string) ([]stackTopic, error) {
	base := stackImportCommit(ctx, repo, name)
	if base == "" {
		// No marker to measure against. Not an error: a stackspace whose history
		// was rewritten past rig's own commits legitimately has none, and there
		// is simply nothing to say about topics there.
		return nil, nil
	}
	branches, err := repo.BranchesWithPrefix(ctx, stackTopicPrefix)
	if err != nil {
		// Failing to enumerate is not the same as there being none, and the
		// difference matters: silence here reads as "no topics in flight".
		return nil, fmt.Errorf("listing %s* branches: %w", stackTopicPrefix, err)
	}
	var out []stackTopic
	for _, b := range branches {
		touches, terr := stackTopicTouches(ctx, repo, base, b, name)
		if terr != nil {
			return nil, fmt.Errorf("comparing %s against %s: %w", b, name, terr)
		}
		if !touches {
			continue
		}
		// An ancestry check that fails errs toward STALE. The two answers are not
		// symmetric: calling a stale topic current hides the one thing this
		// reports — that proposing it would revert what upstream landed — while
		// calling a current one stale costs a line of noise and a propose that
		// then succeeds.
		current, aerr := repo.IsAncestor(ctx, base, b)
		out = append(out, stackTopic{Name: b, Stale: aerr != nil || !current})
	}
	return out, nil
}

// stackStaleTopicNames is stackTopics filtered to the ones a pull left behind.
func stackStaleTopicNames(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, name string) ([]string, error) {
	topics, err := stackTopics(ctx, repo, m, name)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, t := range topics {
		if t.Stale {
			stale = append(stale, t.Name)
		}
	}
	return stale, nil
}

// stackFileDirty reports whether one path has changes git has not recorded —
// staged or not, tracked or not.
func stackFileDirty(ctx context.Context, repo *gitrepo.Repo, path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	dirty, err := repo.DirtyPaths(ctx)
	if err != nil {
		return false, err
	}
	want := filepath.Base(path)
	for _, p := range dirty {
		if filepath.Base(p) == want {
			return true, nil
		}
	}
	return false, nil
}

// stackTreeOf is a commit's tree, or "" when it cannot be read. "" never
// compares equal to a real tree, so an unreadable commit falls through to the
// push rather than being mistaken for a match.
func stackTreeOf(ctx context.Context, repo *gitrepo.Repo, commit string) string {
	tree, err := repo.RevParse(ctx, commit+"^{tree}")
	if err != nil {
		return ""
	}
	return tree
}

// stackBranchHolds reports whether the fork's branch is exactly at commit.
//
// Asked about the branch being proposed TO, not the one the manifest remembers.
// Branch resolution prefixes and can settle on an alternate candidate, so the
// remembered name and the resolved one are not always the same string, and
// comparing them misses an unchanged proposal and pushes again for nothing.
//
// Any doubt answers false — an unreachable fork, a missing ref, a branch that
// has moved — so the caller pushes. A redundant commit costs a little noise; a
// wrong "nothing to send" leaves the work nowhere.
func stackBranchHolds(ctx context.Context, repo *gitrepo.Repo, fork, branch, commit string) bool {
	ref := "refs/heads/" + branch
	found, err := repo.LsRemoteRefs(ctx, stackRemoteURL(fork), stackAuthFor(ctx, fork), ref)
	if err != nil {
		return false
	}
	return found[ref] == commit
}

// stackProposedOnFork confirms that the branch a member was last proposed to
// still holds, on the fork, the commit propose pushed. The ref under
// refs/rigsmith/propose says the work left; this says it is still where it
// went. rm and seed ask before acting as though the work exists elsewhere: a
// branch moved or deleted since, or a fork that cannot be asked, is an error
// naming what to check rather than a guess. A manifest with no record of the
// branch has nothing to look for, and the ref speaks alone.
func stackProposedOnFork(ctx context.Context, repo *gitrepo.Repo, m *stackManifest, name string) error {
	branch, r := m.LastPropose[name], m.Repos[name]
	if branch == "" || r == nil {
		return nil
	}
	want, err := repo.RevParse(ctx, "refs/rigsmith/propose/"+name)
	if err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	found, err := repo.LsRemoteRefs(ctx, stackRemoteURL(r.Fork), stackAuthFor(ctx, r.Fork), ref)
	if err != nil {
		return fmt.Errorf("%s's work was proposed to %s:%s, and the fork cannot be asked whether it is still there (%s) — check it, then --force", name, r.Fork, branch, stackFirstLine(err))
	}
	if found[ref] != want {
		return fmt.Errorf("%s's work was proposed to %s:%s, which no longer holds it as pushed — if it merged, `rig stack pull %s` first; otherwise check the fork, then --force", name, r.Fork, branch, name)
	}
	return nil
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
		found, err := repo.LsRemoteRefs(ctx, url, stackAuthForURL(ctx, url), ref, ref+"^{}")
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
		return repo.LsRemote(ctx, url, "refs/heads/"+pin.Value, stackAuthForURL(ctx, url))
	}
}

// stackPullOpts says how stackPullOne should take a prefix in.
type stackPullOpts struct {
	// initial marks an import: no cursor short-circuit, and the commit reads
	// "import" rather than "pull".
	initial bool
	// at imports a specific upstream commit rather than the resolved tip —
	// what reconstituting a prefix from its recorded cursor needs.
	at string
	// fork imports the tree from a branch of the fork instead of from
	// upstream. The cursor then records the upstream commit that branch is
	// based on, so everything that measures against upstream keeps working.
	fork *stackForkRef
}

// stackForkRef is a branch on the member's fork, resolved.
type stackForkRef struct {
	Branch string
	Commit string
}

// stackPullOne imports or updates one repo's prefix: probe upstream's tip, stop at
// the cursor (idempotent, the josh-sync NothingToPull check), else fetch that
// exact SHA through the filter, merge, and commit the moved cursor with it.
func stackPullOne(ctx context.Context, out io.Writer, repo *gitrepo.Repo, bin string, src *cfgfind.Source, m *stackManifest, name string, opts stackPullOpts) error {
	r := m.Repos[name]
	initial := opts.initial
	tip := opts.at
	// Resolved anew when importing from a fork branch even if a cursor was
	// given: the branch's base is found against upstream as it is now. A
	// branch proposed after the cursor moved on is based past it, and a
	// merge-base against the old cursor would answer with the old cursor —
	// leaving the rebuilt prefix reporting, and later re-merging, upstream
	// commits it already holds.
	if tip == "" || opts.fork != nil {
		var err error
		if tip, err = stackUpstreamTip(ctx, repo, m, name); err != nil {
			return err
		}
	}
	if !initial && tip == m.cursor(name) {
		fmt.Fprintf(out, "%s: nothing to pull\n", name)
		return nil
	}
	// What is fetched through the filter, and from where. Upstream, unless
	// the tree is to come from a branch of the fork — in which case the cursor
	// is still an upstream commit: the one that branch grew from, found by
	// merge-base over the unfiltered objects, so that `status` compares the
	// right things and a later pull merges rather than duplicates.
	source, fetch := r.Upstream, tip
	if opts.fork != nil {
		source, fetch = r.Fork, opts.fork.Commit
		upstreamURL, forkURL := stackRemoteURL(r.Upstream), stackRemoteURL(r.Fork)
		if err := repo.FetchObjects(ctx, forkURL, fetch, stackAuthForURL(ctx, forkURL)); err != nil {
			return fmt.Errorf("fetching %s:%s: %w", r.Fork, opts.fork.Branch, err)
		}
		if err := repo.FetchObjects(ctx, upstreamURL, tip, stackAuthForURL(ctx, upstreamURL)); err != nil {
			return err
		}
		base, err := repo.MergeBase(ctx, fetch, tip)
		if err != nil {
			return err
		}
		if base == "" {
			return fmt.Errorf("%s:%s shares no history with %s — a branch to import from has to be based on upstream's", r.Fork, opts.fork.Branch, r.Upstream)
		}
		tip = base
	}
	host, path := stackSplitHost(source)
	// Two scopes of the same credential, because two different processes
	// authenticate with it. This one is for the engine's own fetch of upstream,
	// so it is scoped to the forge; the one below is for our fetch from the
	// engine, scoped to the loopback proxy.
	proxy, err := startJoshProxy(ctx, bin, host, stackAuthFor(ctx, source))
	if err != nil {
		return err
	}
	defer proxy.stop()
	verb := "pulled"
	msg := fmt.Sprintf("stack: pull %s @ %s", name, short(tip))
	if initial {
		verb = "imported"
		msg = fmt.Sprintf("stack: import %s @ %s", name, short(tip))
		if opts.at != "" {
			verb = "reconstituted"
		}
		if opts.fork != nil {
			msg += fmt.Sprintf(" (from %s:%s %s)", r.Fork, opts.fork.Branch, short(fetch))
		}
	}
	// The engine fetches upstream with whatever credentials its own client
	// presents, so reaching a private repo means forwarding ours to it. This is
	// the same credential git would use fetching that host directly, scoped to
	// the proxy's URL so a redirect cannot carry it anywhere else, and absent
	// entirely when no helper has one. Whether the repo actually needs it is not
	// knowable without asking the forge, so it rides along either way.
	auth, err := gitrepo.CredentialFor(ctx, stackRemoteURL(source))
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
	// Whether the merge below actually moves history is the only thing that
	// separates the two reasons the prefix can differ from its target. See the
	// replace guard.
	preHead, preHeadErr := repo.Head(ctx)

	fetched, err := repo.FetchRef(ctx, proxy.url(path, fetch, stackPrefixFilter(name)), "HEAD", auth)
	if err != nil {
		if tail := proxy.tail(15); tail != "" {
			return fmt.Errorf("%w\n--- josh-proxy log:\n%s", err, tail)
		}
		return err
	}

	// What arrived, checked before anything merges it. An empty fetch is not an
	// empty upstream: josh serves `:prefix=<name>`, so anything it has to give
	// comes back with a tree at <name>, and nothing there means the engine had
	// nothing to filter.
	//
	// The usual reason is a credential that cannot read a private upstream —
	// josh answers that with an empty history rather than a refusal, and the
	// ls-remote above already passed because the credential authenticates, it
	// just cannot see this repo. Everything downstream is guarded on
	// wantErr == nil, so letting it through skips the replace check entirely and
	// records a cursor for a revision that was never imported, after which
	// `status` calls the member up to date against a directory that does not
	// exist and every later pull short-circuits on the cursor.
	//
	// Before the merge, so a rejected import leaves no commit to unwind.
	want, wantErr := repo.RevParse(ctx, fetched+":"+name)
	if wantErr != nil {
		return fmt.Errorf("%s: fetched %s from %s and it carried no %s/ tree\n"+
			"if that upstream is private, the credential in use cannot read it — check `gh auth status`, then run this again",
			name, short(fetch), source, name)
	}

	conflicted, err := repo.MergeUnrelated(ctx, fetched, msg)
	if err != nil {
		return err
	}
	if conflicted {
		// Conflicts outside the prefix are settled here, in this stackspace's
		// favour; ones inside it are the user's, and the message names them
		// rather than the prefix that was asked for.
		if err := stackSettleConflicts(ctx, out, repo, name); err != nil {
			return err
		}
	}

	// A merge cannot move a prefix backwards. Repin a project to an older tag or
	// commit and the target is already an ancestor of what is here, so the merge
	// reports nothing to do — and recording the cursor anyway would claim a
	// revision the directory does not contain, with `status` reporting the pin
	// while the sources stay newer.
	//
	// want (read before the merge) is the prefixed target that was fetched, so
	// its tree under the prefix is what this directory is supposed to hold.
	have, haveErr := repo.RevParse(ctx, "HEAD:"+name)
	// Only when the merge did nothing. A prefix differs from its target for two
	// reasons and the trees cannot tell them apart: either the merge could not
	// move it (the repin above), or the merge moved it and combined upstream
	// with work that was already here — in which case `have` is the merge
	// result and differing from upstream is exactly right.
	//
	// Replacing in that second case discards the merge that just succeeded, and
	// refusing reports failure for work that is already done: the commit and its
	// marker are made by then, so the cursor alone stays behind and every later
	// pull repeats the refusal. Nothing recovers it, and the advice it prints —
	// send them first — cannot help when the work has already been sent.
	nowHead, nowHeadErr := repo.Head(ctx)
	merged := preHeadErr == nil && nowHeadErr == nil && nowHead != preHead
	// A merge that did nothing NOW may have been done before: a pull that
	// conflicted inside the prefix stops with the merge open and the cursor
	// where it was, the user resolves and commits, and this run is the re-run
	// the conflict message asked for. The target is then already in HEAD's
	// history, and the prefix differs from upstream because the resolution
	// kept something — which is a finished merge, not a directory to replace.
	// Judged by history rather than by tree, since a resolution that keeps
	// nothing of its own is the only one a tree comparison would pass.
	taken := !merged && stackTargetTaken(ctx, repo, name, fetched)
	if haveErr != nil {
		// The prefix is not in HEAD at all — removed by `rig stack rm`, or an
		// import that never produced one. Taking such a member back is the case
		// the merge cannot serve: its filtered history is already an ancestor
		// (it was imported once), so the merge is a no-op and nothing restores
		// the directory the removal deleted.
		//
		// Unconditional, unlike the branch below: there is no directory here, so
		// there is nothing of the user's to discard by writing one.
		if err := repo.ReplacePath(ctx, fetched, name); err != nil {
			return fmt.Errorf("restoring %s from %s: %w", name, short(fetch), err)
		}
	} else if want != have && !merged && !taken {
		// Replacing the directory discards whatever is under it, so only do it
		// when there is nothing of the user's to discard. Their own commits would
		// survive in the history but be stranded there, which is a quiet way to
		// lose work.
		if !preKnown || preImported != preTree {
			return fmt.Errorf("%s/ holds changes of its own, and moving it to %s (%s) needs the directory replaced\n"+
				"send them first, or revert them, and run this again",
				name, short(tip), m.pin(name).describe())
		}
		if err := repo.ReplacePath(ctx, fetched, name); err != nil {
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
		delete(m.LastSync, name)
		return err
	}
	// The cursor claims this directory holds that revision, so it cannot be
	// written while the directory is not going to be there. Against the index
	// rather than HEAD: a replace above has just written a tree HEAD has never
	// seen, and the index is what the commit below will carry.
	//
	// The guard after the fetch catches an empty upstream; this catches every
	// other way the prefix can end up absent. It is the one that has to hold — a
	// cursor over a missing directory makes `status` report the member up to
	// date and every later pull short-circuit on it, and nothing after that ever
	// looks at the tree again.
	if present, err := repo.PathInIndex(ctx, name); err != nil || !present {
		restore()
		delete(m.LastSync, name)
		if err != nil {
			return err
		}
		return fmt.Errorf("%s: importing %s left no %s/ directory, so there is nothing for a cursor to point at", name, short(fetch), name)
	}
	// Amend the cursor edit into the merge commit, so one commit carries both
	// the history and the fact that it was synced — the reviewable unit a
	// cron-driven pull PR is built from.
	//
	// Only when there is a merge commit to amend. Where the merge did nothing —
	// a repin backwards, or a removed member whose filtered history is already
	// an ancestor — amending would rewrite whatever the user committed last, and
	// git refuses outright when the result would be empty. That refusal is the
	// `--amend --no-edit: would make it empty` dead end: nothing was wrong with
	// the import, only with what it tried to fold itself into.
	//
	// When the merge was finished by hand, HEAD is that merge only if nothing
	// has been committed since; the cursor is amended into it then, as it
	// would have been, and otherwise recorded in a commit of its own rather
	// than folded into whatever the user committed last. That commit is not
	// an import marker — it has no upstream side for the baseline to be read
	// from — so its subject must not read as one.
	commit := func() error {
		switch {
		case merged, taken && stackHeadTook(ctx, repo, fetched):
			_, err := repo.CommitAmendNoEdit(ctx)
			return err
		case taken:
			_, err := repo.Commit(ctx, fmt.Sprintf("stack: cursor %s @ %s", name, short(tip)))
			return err
		default:
			_, err := repo.Commit(ctx, msg)
			return err
		}
	}
	if err := commit(); err != nil {
		restore()
		delete(m.LastSync, name)
		return err
	}
	if taken {
		verb = "recorded the resolved merge to"
	}
	if opts.fork != nil {
		// Rebuilt from the branch this member was last proposed to, so its
		// content is already on the fork — and that fact has to survive the
		// rebuild. propose records it under this ref, and status, rm and propose
		// itself all read it to know the work has left. Without it a rebuilt
		// stackspace reports work as unsent that is demonstrably on the fork,
		// re-pushes an identical commit on the next propose, and makes rm ask
		// about work that is not at risk.
		//
		// The fork's own commit is exactly what propose put there: its tree is
		// this member's tree, which is why the comparison against HEAD:<name>
		// holds.
		if err := repo.SetRef(ctx, "refs/rigsmith/propose/"+name, opts.fork.Commit); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s from %s:%s (%s), based on upstream %s\n", name, verb, r.Fork, opts.fork.Branch, short(fetch), short(tip))
	} else {
		fmt.Fprintf(out, "%s: %s upstream %s\n", name, verb, short(tip))
	}
	return nil
}

// stackTargetTaken reports whether the filtered upstream commit a pull is
// bringing in is already in the stackspace's history by way of a merge that
// went FORWARD from where the prefix was last synced — a pull that conflicted
// and was resolved and committed by hand, whose re-run is what records it.
//
// Ancestry alone cannot say that: repinning a prefix to an older release also
// finds the target already in HEAD, and there the answer is to replace the
// directory. The two are told apart by the last sync's upstream side, which
// is where the prefix last stood: a target at or past it was merged in, one
// behind it is a move backwards. The last sync is the newest merge that took
// a filtered commit of the prefix in, whatever its subject — a resolved pull
// committed under the user's own words counts, or the baseline would stay at
// the sync before it and let a repin back to that one through as though it
// were a merge already made. No such merge, or one with no upstream side,
// answers no, and the replace guard decides as before.
func stackTargetTaken(ctx context.Context, repo *gitrepo.Repo, name, target string) bool {
	if ok, err := repo.IsAncestor(ctx, target, "HEAD"); err != nil || !ok {
		return false
	}
	marker := stackImportCommit(ctx, repo, name)
	if marker == "" {
		return false
	}
	last, err := repo.RevParse(ctx, marker+"^2")
	if err != nil {
		return false
	}
	ok, err := repo.IsAncestor(ctx, last, target)
	return err == nil && ok
}

// stackHeadTook reports whether HEAD is itself the merge that brought target
// in — the commit a resolved pull is amended into, as an unconflicted one is.
func stackHeadTook(ctx context.Context, repo *gitrepo.Repo, target string) bool {
	side, err := repo.RevParse(ctx, "HEAD^2")
	if err != nil {
		return false
	}
	want, err := repo.RevParse(ctx, target)
	return err == nil && side == want
}

// stackPrefixPresent reports whether HEAD has a directory for the prefix.
func stackPrefixPresent(ctx context.Context, repo *gitrepo.Repo, name string) bool {
	_, err := repo.RevParse(ctx, "HEAD:"+name)
	return err == nil
}

// stackTopicPrefix is the RECOMMENDED name for a stackspace's own in-flight
// branches — the topics `propose --from` sends one at a time. It is a
// convention, not a rule: --from takes any branch, and this is only what it
// falls back to when the name it was given is not itself a branch, and what
// `status` lists so you can see what is in flight without remembering it.
//
// Distinct from branchPrefix (`stack/`), which names the branches that appear
// on your FORK. These two live in different repositories and mean different
// things — one is work in progress here, the other is a pull request there — so
// sharing a spelling would only invite reading one as the other.
const stackTopicPrefix = "stack-pr-"

// stackResolveTopic turns what --from was given into a branch that exists.
// An exact name wins: the convention is a suggestion, and a branch the user
// actually named is never second-guessed. Otherwise the conventional name is
// tried, so `--from reader-wedge` finds stack-pr-reader-wedge.
func stackResolveTopic(ctx context.Context, repo *gitrepo.Repo, given string) (string, error) {
	if repo.BranchExists(ctx, given) {
		return given, nil
	}
	if conventional := stackTopicPrefix + given; repo.BranchExists(ctx, conventional) {
		return conventional, nil
	}
	return "", fmt.Errorf("no branch %q in this stackspace, and no %q either — `git branch` to see what is here", given, stackTopicPrefix+given)
}

func newStackSendCmd() *cobra.Command {
	var message string
	var fromBranch string
	cmd := &cobra.Command{
		Use:     "propose [repo] [new-branch]",
		Aliases: []string{"send"},
		Short:   "Propose a repo's stackspace changes to its upstream, via your fork",
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
			"among your own work on the same fork: `propose lib read-timeout` creates\n" +
			"stack/read-timeout. Change it with the manifest's branchPrefix, or set\n" +
			"that to \"\" for bare names.\n\n" +
			"By default the branch carries the WHOLE of what this stackspace has for\n" +
			"<repo> — every fix it is holding, not just your latest. That is right\n" +
			"while one thing is in flight, and wrong the moment two are: a second\n" +
			"pull request would show the first one's changes too.\n\n" +
			"--from <branch> proposes one topic branch of this stackspace instead,\n" +
			"so each fix is its own pull request:\n\n" +
			"    git switch -c stack-pr-reader-wedge <the import commit>\n" +
			"    ...fix, commit...\n" +
			"    git switch main && git merge stack-pr-reader-wedge\n" +
			"    rig stack propose lib reader-wedge --from reader-wedge\n\n" +
			"`stack-pr-<name>` is a recommended convention, not a rule: --from takes\n" +
			"any branch, falls back to the conventional name when what you gave it\n" +
			"is not itself a branch, and `status` lists the ones named that way so\n" +
			"you can see what is in flight.\n\n" +
			"A topic rooted on the import holds upstream plus its own change and\n" +
			"nothing else, so there is no patch to replay and nothing that can fail\n" +
			"to apply as histories intertwine. Branch off a line that already carries\n" +
			"another unmerged fix and the topic contains that fix too — propose says\n" +
			"which commits it is sending, so you see that before a reviewer does.\n\n" +
			"A topic is deliberately not the whole divergence, so it cannot also be\n" +
			"what a rebuild elsewhere reconstitutes from. That is trackBranch, which\n" +
			"--from requires and keeps current with everything the prefix carries.\n\n" +
			"With --dry-run, says what it would push and where, and stops there:\n" +
			"nothing reaches the fork, and nothing local records a proposal.",
		Args:              cobra.MaximumNArgs(2),
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
			name, typed := "", ""
			if len(args) > 0 {
				name = args[0]
			}
			if len(args) > 1 {
				typed = args[1]
			}
			// Ask for what was not given. Only on a terminal: a script with a
			// missing argument should be told so, not left waiting on a form
			// nobody is there to answer.
			reused := false
			if name == "" || typed == "" {
				if !stdinStdoutTTY() {
					// The branch this repo was last proposed on is the one an open
					// pull request is already watching, so without a terminal to
					// ask, reusing it is the useful answer rather than a refusal.
					if name != "" && m.LastPropose[name] != "" {
						typed = m.LastPropose[name]
						reused = true
					} else {
						missing := "a repo and a new branch name"
						if name != "" {
							missing = "a new branch name for " + name
						}
						return fmt.Errorf("propose needs %s — `rig stack propose <repo> <new-branch>`, or run it on a terminal to be asked", missing)
					}
				} else {
					name, typed, err = stackAskSend(m, name, typed)
					if err != nil {
						return err
					}
					if typed == "" {
						return nil // backed out
					}
				}
			}
			r := m.Repos[name]
			if r == nil {
				if err := m.requireRepos(); err != nil {
					return err
				}
				return fmt.Errorf("no stack repo %q (have: %s)", name, strings.Join(m.names(), ", "))
			}
			// The prefix keeps these branches recognisable on a fork that also
			// carries your own work, and is applied here rather than at the call
			// sites so the menu and the CLI cannot disagree about it.
			branch, other := m.proposeBranch(name, typed)
			if other != "" {
				// The remembered branch reads two ways (a prefix changed since
				// it was recorded); the fork knows which one exists, and that
				// is the one an open pull request is watching.
				found, err := repo.LsRemoteRefs(ctx, stackRemoteURL(r.Fork), stackAuthFor(ctx, r.Fork), "refs/heads/"+branch, "refs/heads/"+other)
				if err != nil {
					return fmt.Errorf("%s was last proposed to %s or %s, and %s cannot be asked which exists: %w\nname the branch in full to propose without asking", name, branch, other, r.Fork, err)
				}
				if _, ok := found["refs/heads/"+branch]; !ok {
					if _, ok := found["refs/heads/"+other]; ok {
						branch = other
					}
				}
			}
			if reused {
				fmt.Fprintf(cmd.OutOrStdout(), "proposing to %s again\n", branch)
			}
			if m.cursor(name) == "" {
				return fmt.Errorf("%s is not imported yet — run `rig stack init`", name)
			}
			if dirty, err := stackDirtyUnder(ctx, repo, name); err != nil {
				return err
			} else if dirty {
				return fmt.Errorf("%s/ has uncommitted changes — commit them, or they will not be in what you propose", name)
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
			if err := repo.FetchObjects(ctx, upstreamURL, tip, stackAuthForURL(ctx, upstreamURL)); err != nil {
				return err
			}

			// A new commit object never equals its parent, so the no-op has to be
			// read off the trees: same tree as upstream means nothing to send.
			tipTree, err := repo.RevParse(ctx, tip+"^{tree}")
			if err != nil {
				return err
			}
			// Everything the prefix carries, kept aside before a topic narrows
			// what is proposed: it is what trackBranch is updated with below, so
			// a rebuild still gets the fixes this pull request leaves out.
			fullTree := tree
			// Declared out here because it is written under --from and read at the
			// recording step further down, after the push.
			topicCommit := ""
			if fromBranch != "" {
				// Without somewhere to keep the whole divergence, proposing one
				// topic would make every rebuild — CI included — silently drop the
				// other fixes, while the local worktree still had them and looked
				// fine. Refuse rather than be quietly wrong.
				if r.TrackBranch == "" {
					return fmt.Errorf("--from needs a trackBranch for %s: the proposed branch would hold only %s, and a rebuild reconstitutes from it\n"+
						"set \"trackBranch\" on %s in the manifest (a branch of %s, e.g. %q) — propose keeps it current with everything the prefix carries",
						name, fromBranch, name, r.Fork, m.sendBranch(name, "integration"))
				}
				// Proposing ONTO trackBranch would have the integration push below
				// force-overwrite the pull request with the whole divergence — the
				// reviewer would open a topic's PR and find every carried fix in it.
				// The two branches serve opposite purposes and cannot be one.
				if branch == r.TrackBranch {
					return fmt.Errorf("%s is %s's trackBranch, which propose keeps at the whole divergence — a pull request there would be overwritten with every carried fix\n"+
						"name the proposal something else", branch, name)
				}
				// A topic rooted on the import holds upstream's tree plus its own
				// change and nothing else, so its prefix tree IS what upstream
				// should see. No patch to replay, and nothing that can fail to
				// apply — which is the whole reason this is a branch rather than a
				// range of commits.
				topic, terr := stackResolveTopic(ctx, repo, fromBranch)
				if terr != nil {
					return terr
				}
				fromBranch = topic
				topicTree, terr := repo.RevParse(ctx, fromBranch+":"+name)
				if terr != nil {
					return fmt.Errorf("%s has no %s/ in it, so there is nothing of that project to propose: %w", fromBranch, name, terr)
				}
				// Read HERE, with the tree that is about to be built and pushed —
				// not after the push. Re-reading a mutable branch afterwards can
				// record a revision that was never sent, and a failed re-read would
				// silently record nothing and turn movement detection off.
				var cerr error
				topicCommit, cerr = repo.RevParse(ctx, fromBranch)
				if cerr != nil {
					return fmt.Errorf("cannot read %s to record what is being proposed: %w", fromBranch, cerr)
				}
				// A topic rooted before the last pull holds the prefix as upstream
				// USED to be. Committing that tree onto the current tip would
				// present every upstream commit since as though this branch had
				// reverted it — the same failure the stale-cursor guard above
				// prevents for a whole-prefix propose, which cannot see this one
				// because HEAD has been pulled and the topic has not.
				//
				// Measured against rig's own import marker, not the cursor: the
				// cursor is a raw upstream commit while an import merges
				// josh-rewritten content, so the cursor is an ancestor of nothing
				// here and would call every topic stale.
				importedAt := stackImportCommit(ctx, repo, name)
				if importedAt == "" {
					return fmt.Errorf("cannot find the commit that imported %s, so cannot tell whether %s predates it", name, fromBranch)
				}
				if current, aerr := repo.IsAncestor(ctx, importedAt, fromBranch); aerr != nil {
					return fmt.Errorf("cannot tell whether %s has %s's latest import in it: %w", fromBranch, name, aerr)
				} else if !current {
					return fmt.Errorf("%s was rooted before %s was last brought up to upstream (%s), so proposing it would revert the commits that landed in between\n"+
						"re-root it: branch again from that commit and replay this fix onto it",
						fromBranch, name, short(importedAt))
				}
				// What the pull request will actually contain, said out loud.
				//
				// There is deliberately no check that the topic is "rooted
				// correctly", because there cannot be one: a topic branched off a
				// line already carrying another fix genuinely CONTAINS that fix,
				// and which commits constitute this change is precisely what the
				// branch encodes. rig cannot tell "that came along by accident"
				// from "that is part of my change" — only the author can. So it
				// reports, and the author sees a second subject they did not
				// expect before a maintainer does.
				//
				// Measured from the import marker, so this is the topic's own
				// history since the prefix was last brought up to upstream.
				// Not prefix-filtered: a cross-cutting commit belongs in every
				// member's proposal, and one that happens to touch only another
				// member is still part of what this branch is.
				if carried, cerr := repo.LogRange(ctx, importedAt, fromBranch); cerr == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "%s: proposing %s — %d commit(s) since the import\n", name, fromBranch, len(carried))
					for _, c := range carried {
						fmt.Fprintf(cmd.OutOrStdout(), "    %s %s\n", short(c.SHA), c.Subject)
					}
				}
				tree = topicTree
			}
			if tipTree == tree {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: nothing to send — it matches upstream\n", name)
				return nil
			}
			// Already on the fork, unchanged, on the branch being asked for.
			// Differing from upstream is the normal state of a member whose work
			// is proposed and not yet merged, so upstream alone cannot answer
			// this — a stackspace rebuilt from its own proposed branch differs
			// from upstream the moment it is rebuilt, and would push an
			// identical commit every time.
			//
			// The fork is asked rather than trusted from the local ref: a branch
			// deleted or moved since means the work is no longer where the ref
			// says, and calling that "nothing to send" would leave it nowhere.
			// Any doubt falls through and pushes, which costs a redundant commit
			// and never a lost one.
			if sentCommit, serr := repo.RevParse(ctx, "refs/rigsmith/propose/"+name); serr == nil &&
				stackTreeOf(ctx, repo, sentCommit) == tree &&
				stackBranchHolds(ctx, repo, r.Fork, branch, sentCommit) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: nothing to send — %s:%s already holds it\n", name, r.Fork, branch)
				return nil
			}

			// What this member gets from the stackspace rather than from a feed.
			// Read here, before the push, so the same answer serves a dry run
			// and a real proposal; reporting happens at whichever exit is taken.
			pins, pinScanFailed := stackStackOnlyPins(ctx, repo.Dir, m, name)

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
			// A dry run stops here, with the commit built and nothing sent. The
			// object stays in the local store unreferenced, which is harmless;
			// the ref under refs/rigsmith/propose and the manifest's memory of
			// the branch both record a push, so neither is written for a push
			// that did not happen.
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "would push %s to %s:%s (proposing to %s)\n",
					short(commit), r.Fork, branch, r.Upstream)
				stackReportStackOnlyPins(cmd.OutOrStdout(), pins, name, pinScanFailed)
				return nil
			}

			// Each send synthesizes a fresh commit parented on the upstream tip,
			// so a second send to the same branch is a sibling of the first and a
			// plain push is refused as non-fast-forward — which would make it
			// impossible to update an open PR. Replace under a lease instead, so
			// the push still fails if someone else moved the branch meanwhile.
			if err := repo.PushRefForce(ctx, stackRemoteURL(r.Fork), commit, "refs/heads/"+branch, stackAuthFor(ctx, r.Fork)); err != nil {
				return err
			}
			// Kept under a local ref as well: this commit's tree is what the
			// fork now holds, and status and rm compare against it to know the
			// work has left — nothing in the stackspace's own history says so.
			if err := repo.SetRef(ctx, "refs/rigsmith/propose/"+name, commit); err != nil {
				return err
			}
			// A selected branch holds part of what this prefix carries, and
			// `init` reconstitutes from trackBranch — so trackBranch is where the
			// rest has to be, or a rebuild elsewhere quietly builds without the
			// fixes this pull request left out. Pushed after the proposal, so a
			// failed proposal does not move it.
			//
			// Its commit is rooted on the same upstream tip, so the branch reads
			// as "upstream, plus everything we carry" — which is what it is, and
			// what makes it a sane thing to open by hand.
			if fromBranch != "" {
				intCommit, ierr := repo.CommitTree(ctx, fullTree, tip,
					fmt.Sprintf("Everything the stackspace carries for %s, including what is not yet proposed", name))
				if ierr != nil {
					return ierr
				}
				if ierr := repo.PushRefForce(ctx, stackRemoteURL(r.Fork), intCommit, "refs/heads/"+r.TrackBranch, stackAuthFor(ctx, r.Fork)); ierr != nil {
					return fmt.Errorf("the proposal reached %s:%s, but %s could not be updated: %w\na rebuild would be missing what this pull request left out — push it before seeding", r.Fork, branch, r.TrackBranch, ierr)
				}
				// Recorded like the proposal ref, so status and seed can tell
				// that the unproposed commits have left too.
				if ierr := repo.SetRef(ctx, "refs/rigsmith/integration/"+name, intCommit); ierr != nil {
					return ierr
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s now carries everything, for rebuilds\n", name, r.TrackBranch)
			}
			// Whether the manifest was already the user's business before this
			// command touched it. Asked BEFORE the write below, because
			// afterwards rig's own edit is indistinguishable from theirs.
			manifestWasDirty, dirtyErr := stackFileDirty(ctx, repo, src.File)
			if dirtyErr != nil {
				return dirtyErr
			}
			// Remembered after the push, not before: a branch nothing reached is
			// not the one to offer back next time.
			if err := stackRememberProposed(src, m, name, branch); err != nil {
				return err
			}
			// And, for a topic, WHICH topic went where. lastPropose holds one
			// branch per prefix, so with two fixes in flight for one member it can
			// only remember the second; this is the record `status` reads to say
			// where each one is.
			if fromBranch != "" {
				// topicCommit, captured with the tree that was pushed — not a fresh
				// read, which could have moved in between.
				if err := stackRememberProposal(src, m, name, fromBranch, branch, topicCommit); err != nil {
					return fmt.Errorf("%s reached %s:%s, but recording where it went failed: %w\npropose it again to record it — the push already happened, so re-sending is a no-op on the fork", fromBranch, r.Fork, branch, err)
				}
			}
			// And committed, because leaving it in the work tree makes it two
			// kinds of wrong. `seed` refuses to export a stackspace with
			// uncommitted changes — reasonably, since a seed has to be a
			// revision that exists — so a propose left the next seed impossible
			// until something else happened to commit. And a seed taken anyway
			// carries the previous branch name, so a rebuild reaches for work
			// that is not there.
			//
			// Only this file: sweeping the user's half-finished edits into a
			// record of where their work went is not this command's business.
			// The message deliberately does not match the baseline marker
			// pattern — the tree here is local work, not what upstream had, and
			// reading it as a baseline would quietly disable the guard in pull.
			//
			// Not when the file was already edited: committing then would put
			// the user's unrelated change into a commit that claims to record a
			// proposal, under a message describing something else entirely. The
			// record still reached the file, and it goes in with whatever they
			// commit next.
			switch {
			case manifestWasDirty:
				fmt.Fprintf(cmd.OutOrStdout(),
					"%s: recorded the branch in %s, left uncommitted — that file already had changes of yours\n",
					name, filepath.Base(src.File))
			default:
				if _, err := repo.CommitPaths(ctx, fmt.Sprintf("stack: propose %s -> %s", name, branch), src.File); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "proposed %s — pushed to %s:%s, open the PR against %s\n",
				name, r.Fork, branch, r.Upstream)
			stackReportStackOnlyPins(cmd.OutOrStdout(), pins, name, pinScanFailed)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message for the branch")
	cmd.Flags().StringVar(&fromBranch, "from", "", "propose only what this stackspace branch adds (a topic branch); requires trackBranch")
	return cmd
}

// newStackPushCmd exports a member you own back to its own repository, keeping
// the history that `send` deliberately discards.
func newStackPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push [repo]",
		Short: "Fast-forward a repo you own with this stackspace's commits, history intact",
		Long: "For a project marked `\"owned\": true` in the manifest — one of yours,\n" +
			"named, or inferred when exactly one repo here is yours.\n" +
			"not a fork you contribute to. Extracts everything the stackspace has done\n" +
			"under <repo>/ and fast-forwards that project's own branch with it.\n\n" +
			"Unlike `send`, nothing is squashed. Each stackspace commit that touched\n" +
			"<repo>/ arrives as its own commit, with its message, parented on what\n" +
			"upstream already had — so a change spanning several projects lands as a\n" +
			"matching commit in each of them. Commits that touched nothing under\n" +
			"<repo>/ do not appear at all.\n\n" +
			"`send` is the verb for someone else's project: it proposes one squashed\n" +
			"commit on a branch of your fork, which is what a reviewer wants and the\n" +
			"wrong thing entirely for a repository that is yours.\n\n" +
			"With --dry-run, prints the target, the branch and the commits that would\n" +
			"go, and stops there: nothing reaches the remote, and nothing local\n" +
			"records a push.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: stackRepoCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			m, src, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			// Only a repo of your own can be pushed, so with exactly one there is
			// nothing to disambiguate and naming it is ceremony. With several, ask
			// rather than guess — this one writes to somebody's remote.
			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				owned := m.ownedNames()
				switch len(owned) {
				case 1:
					name = owned[0]
				case 0:
					if err := m.requireRepos(); err != nil {
						return err
					}
					return fmt.Errorf("no repo here is marked as yours — set \"owned\": true on the one you push to, or use `rig stack propose <repo> <branch>` to propose a change to a fork")
				default:
					return fmt.Errorf("several repos here are yours (%s) — name the one to push", strings.Join(owned, ", "))
				}
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
					"set \"owned\": true on %s in the manifest, or use `rig stack propose %s <branch>` to propose the change to its fork instead",
					name, name, name)
			case m.pin(name).pinned():
				// A pin names a fixed point in history; there is no branch there to
				// move, and advancing the cursor past it would contradict the pin.
				return fmt.Errorf("%s is pinned to %s — there is no branch to fast-forward.\n"+
					"replace the pin with upstreamBranch to follow a branch again", name, m.pin(name).describe())
			}
			if dirty, err := stackDirtyUnder(ctx, repo, name); err != nil {
				return err
			} else if dirty {
				return fmt.Errorf("%s/ has uncommitted changes — commit them, or they will not be in what you push", name)
			}

			upstreamURL := stackRemoteURL(r.Upstream)
			branch := m.branch(name)
			tip, err := repo.LsRemote(ctx, upstreamURL, "refs/heads/"+branch, stackAuthForURL(ctx, upstreamURL))
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

			// The filter is the one engine a dry run needs: it is what says what
			// would go. The proxy is ensured further down, once a dry run has
			// returned — a preview should not download or build a tool it never
			// uses.
			filter, err := ensureJoshTool(ctx, m.joshVersion(), toolFilter, out)
			if err != nil {
				return err
			}
			// :/<name> is the exact inverse of the :prefix=<name> this was imported
			// with, so the shared history filters back to upstream's own commit ids
			// and what is left on top is a fast-forward rather than a fork of it.
			ref := "refs/rigsmith/push/" + name
			// A dry run leaves no trace, and the filter writes this ref: note what
			// it held (nothing, usually) so that it can be put back on the way out.
			prior := ""
			if dryRun {
				if v, err := repo.RevParse(ctx, ref); err == nil {
					prior = v
				}
			}
			if err := stackRunJoshFilter(ctx, filter, repo.Dir, ":/"+name, ref); err != nil {
				return err
			}
			head, err := repo.RevParse(ctx, ref)
			if err != nil {
				return err
			}
			if head == tip {
				fmt.Fprintf(out, "%s: nothing to push — it matches %s\n", name, r.Upstream)
				if dryRun {
					return stackRestoreRef(ctx, repo, ref, prior)
				}
				return nil
			}

			// A dry run has done its work by now: the filter ran locally, and what
			// sits between upstream's tip and the filtered head is exactly what a
			// push would carry. Say so and stop — before the push, and before the
			// take-back below, which records a push that has not happened.
			if dryRun {
				commits, err := repo.LogRange(ctx, tip, head)
				if err != nil {
					return fmt.Errorf("listing what would be pushed to %s:%s: %w", r.Upstream, branch, err)
				}
				fmt.Fprintf(out, "would push %s to %s:%s (%s)\n", name, r.Upstream, branch, short(head))
				for _, c := range commits {
					fmt.Fprintf(out, "  %s %s\n", short(c.SHA), c.Subject)
				}
				return stackRestoreRef(ctx, repo, ref, prior)
			}

			// The second engine before the push, not after: the stackspace has to
			// take back what it sends (see below), and discovering a missing
			// binary once the remote has already moved would leave exactly the
			// split state this is trying to avoid.
			proxy, err := ensureJoshTool(ctx, m.joshVersion(), toolProxy, out)
			if err != nil {
				return err
			}

			// No force and no lease: a push that is not a fast-forward means the
			// filtered history is not a continuation of upstream's, and overwriting
			// is never the right answer to that.
			if err := repo.PushRef(ctx, upstreamURL, head, "refs/heads/"+branch, stackAuthForURL(ctx, upstreamURL)); err != nil {
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
			if err := stackPullOne(ctx, io.Discard, repo, proxy, src, m, name, stackPullOpts{}); err != nil {
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

// stackRestoreRef puts ref back to what it held before a dry run wrote it:
// prior, or nothing at all when there was nothing.
func stackRestoreRef(ctx context.Context, repo *gitrepo.Repo, ref, prior string) error {
	if prior != "" {
		return repo.SetRef(ctx, ref, prior)
	}
	return repo.DeleteRef(ctx, ref)
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
			m, _, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			// Asked before wiring, because a wire that defers still returns
			// nil and the refresh below would go ahead anyway — writing a file
			// nobody asked for into a tree that is about to be imported into.
			// The heading follows the directory name, so a seed cloned under a
			// different one is dirtied on sight, and the `setup` that follows
			// refuses to import over it.
			deferred := len(stackMissingPrefixes(repo.Dir, m.names())) > 0
			if _, err := stackWire(ctx, cmd.OutOrStdout(), m, repo, "", false); err != nil {
				return err
			}
			if deferred {
				return nil // it changed nothing, so it leaves nothing behind
			}
			// Regenerated with the overlay, so the member table follows the
			// manifest rather than going stale the first time one is added.
			switch wrote, err := writeStackReadme(repo.Dir, m); {
			case err != nil:
				fmt.Fprintf(cmd.ErrOrStderr(), "could not refresh README.md: %v\n", err)
			case wrote:
				fmt.Fprintln(cmd.OutOrStdout(), "refreshed README.md")
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

// stackDirtyUnder reports whether a prefix has uncommitted changes.
//
// send and push export a prefix's tree at HEAD, so uncommitted work under it
// would be silently left out of what leaves — which is worth refusing over.
// Uncommitted work anywhere else cannot reach the export at all, and refusing
// for it means `rig stack wire` writing the stackspace's own overlay blocks a
// push that has nothing to do with it.
//
// import and pull keep the whole-tree check, and should: they amend a merge
// commit and stage everything, so an unrelated edit is swallowed into it.
func stackDirtyUnder(ctx context.Context, repo *gitrepo.Repo, name string) (bool, error) {
	paths, err := repo.DirtyPaths(ctx)
	if err != nil {
		return false, err
	}
	prefix := name + "/"
	for _, p := range paths {
		if strings.HasPrefix(filepath.ToSlash(p), prefix) {
			return true, nil
		}
	}
	return false, nil
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
				reports, orphans, notes, failed := stackCheckOverlay(ctx, root, m)
				stackReportOrphans(out, m, orphans)
				stackReportNotes(out, notes)
				stackReportScanFailures(out, failed)
				for _, rep := range reports {
					if len(rep.Links) == 0 {
						fmt.Fprintf(out, "· %s: no package reference crosses between members here\n", rep.Eco)
					} else {
						fmt.Fprintf(out, "· %s: %d package(s) cross between members here\n", rep.Eco, len(rep.Links))
					}
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
		{label: "setup", desc: "set up a fresh clone: fusion engine, member directories, build overlay", cmd: newStackSetupCmd()},
		{label: "init", desc: "import any repo the manifest names but has not fused yet", cmd: newStackInitCmd()},
		{label: "add", desc: "add a repo to this stackspace and import it", cmd: newStackAddCmd()},
		{label: "rm", desc: "remove a repo from this stackspace — manifest, tree and overlay (pick one)", cmd: newStackRemoveMenuCmd()},
		{label: "status", desc: "each repo's cursor against its upstream, and how much it diverges by", cmd: newStackStatusCmd()},
		{label: "pull", desc: "merge new upstream commits into every repo", cmd: newStackPullCmd()},
		{label: "propose", desc: "ALL of a repo's changes to its upstream, via a branch on your fork (asks; --from proposes one topic)", cmd: newStackSendCmd()},
		{label: "push", desc: "a repo you own back to its own branch, history intact (pick one)", cmd: newStackPushMenuCmd()},
		{label: "wire", desc: "write the build overlay so members resolve each other from source", cmd: newStackWireCmd()},
		{label: "pack", desc: "build a member's packages here, where the overlay makes siblings resolve", cmd: newStackPackCmd()},
		{label: "doctor", desc: "check the engine and manifest", cmd: newStackDoctorCmd()},
		{label: "seed", desc: "export just the root files as a small repo, to rebuild this stackspace elsewhere (asks where)", cmd: newStackSeedMenuCmd()},
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

// stackAskSend fills in whatever `propose` was not given:
// the repo comes from the manifest as a pick, the branch from a prompt. Hidden —
// it exists only for the menu, like the worktree new/open/rm wrappers.
// stackAskSend fills in whatever `send` was not given. Extracted from what was
// a second, hidden copy of the command that only the menu could reach: the same
// form now answers a bare `rig stack propose`, so the picker is not a different
// feature from the verb.
//
// Returns an empty branch when the user backs out, which the caller treats as
// nothing to do rather than as a failure.
func stackAskSend(m *stackManifest, name, branch string) (string, string, error) {
	// Offer back the branch this repo was last proposed on: proposing again to
	// the same one is how an open pull request takes review feedback, so it is
	// usually wanted several times and is tedious to retype exactly.
	if branch == "" && name != "" {
		branch = m.LastPropose[name]
	}
	names := m.names()
	fields := []huh.Field{}
	if name == "" {
		name = names[0]
		if len(names) > 1 {
			opts := make([]huh.Option[string], 0, len(names))
			for _, n := range names {
				opts = append(opts, huh.NewOption(fmt.Sprintf("%s  →  %s", n, m.Repos[n].Fork), n))
			}
			fields = append(fields, huh.NewSelect[string]().
				Title("Send which repo?").Options(opts...).Filtering(true).Value(&name))
		}
	}
	if branch == "" {
		// The prompt cannot be skipped by reading the manifest: this branch is
		// named per change, and the manifest's upstreamBranch is a different
		// thing entirely — the branch of upstream the directory follows.
		//
		// Name the destination fork when it is already settled; when a select
		// above has yet to decide it, stay general rather than name the wrong one.
		where := "your fork"
		if len(fields) == 0 && m.Repos[name] != nil {
			where = m.Repos[name].Fork
		}
		// Show the prefix rather than let it surprise them after the fact. It is
		// uniform unless a repo overrides it, so only promise a specific one when
		// every repo here agrees.
		desc := fmt.Sprintf("created on %s, holding one commit", where)
		if prefix := stackCommonPrefix(m, names); prefix != "" {
			desc = fmt.Sprintf("created on %s as %s<name> — e.g. read-timeout", where, prefix)
		}
		title := "New branch on your fork"
		if branch != "" {
			// It is not new any more, and pressing enter updates the pull request
			// that is already open on it.
			title = "Branch on your fork"
			desc = fmt.Sprintf("%s — enter updates the pull request already on it", m.sendBranch(name, branch))
		}
		fields = append(fields, huh.NewInput().Title(title).
			Description(desc).
			Placeholder("read-timeout").
			Value(&branch))
	}
	if len(fields) == 0 {
		return name, branch, nil
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).
		WithKeyMap(huhEscKeyMap()).WithTheme(rigTheme()).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return name, "", nil
		}
		return name, "", err
	}
	return name, strings.TrimSpace(branch), nil
}
