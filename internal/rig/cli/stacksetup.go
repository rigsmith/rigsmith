package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

// newStackSetupCmd is the one command a fresh clone of a stackspace needs.
//
// The steps existed; the order did not. Following the obvious one — install the
// engine, then import — meant running `rig stack doctor --fix` first, because
// that is what installs josh. At that moment no member directory exists, so
// nothing crosses between members, so the overlay looks left over and doctor
// advised deleting it. Taking that advice throws away the file the workspace is
// about to need. The ordering was forced, so every first-timer met it.
//
// Doing the steps here, in an order where each can see what it is judging, puts
// doctor back to being a health check you run when something is wrong rather
// than a required setup step.
func newStackSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Set up a freshly cloned stackspace: engine, members, build overlay",
		Long: "Runs the steps a new clone needs, in an order where each can see what it\n" +
			"is judging: installs the fusion engine, reconstitutes the member\n" +
			"directories, writes the build overlay if it is missing, commits what it\n" +
			"generated, and prints the status.\n\n" +
			"Safe to run again — every step is a no-op once it has been done.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, out := cmd.Context(), cmd.OutOrStdout()
			root, err := stackRoot(ctx)
			if err != nil {
				return err
			}
			m, _, err := loadStackManifest(root)
			if err != nil {
				return err
			}
			if m == nil {
				return fmt.Errorf("no stack manifest here — `rig stack init` scaffolds one to fill in")
			}
			// An empty manifest loads fine — it is what `init` scaffolds — but
			// setting up nothing installs an engine, writes a memberless README
			// and reports success, which is a worse answer than the one this
			// state already has.
			if err := m.requireRepos(); err != nil {
				return err
			}

			repo, err := gitrepo.Open(ctx, root)
			if err != nil {
				return err
			}
			// What is already dirty is the user's, and stays theirs. Everything
			// setup goes on to produce is the difference against this.
			before, err := repo.DirtyPaths(ctx)
			if err != nil {
				return err
			}

			// 1. The engine, before anything that needs it. Acquiring it can
			//    mean a multi-minute build on a fresh machine, which is worth
			//    saying before the terminal goes quiet.
			fmt.Fprintln(out, "· fusion engine")
			if _, err := ensureJoshProxy(ctx, m.joshVersion(), out); err != nil {
				return err
			}

			// 2. The members. init is what knows how to reconstitute one at the
			//    commit its cursor names, and is already a no-op for a member
			//    that is present.
			fmt.Fprintln(out, "· members")
			if err := runStackSubcommand(cmd, "init"); err != nil {
				return err
			}

			// 3. The overlay, now that there is something to judge it against.
			fmt.Fprintln(out, "· build overlay")
			if err := runStackSubcommand(cmd, "wire"); err != nil {
				return err
			}

			// 4. The overlay and README are meant to be committed — a seed
			//    carries both — and leaving them loose makes the claim above
			//    false: the next run's import refuses a dirty tree, and the
			//    files it is refusing over are the ones this command just
			//    wrote. Only what setup added, by path, so a user's edits are
			//    not swept into a commit describing something else.
			if made, err := stackCommitSetupOutput(ctx, repo, before); err != nil {
				return err
			} else if made {
				fmt.Fprintln(out, "· committed the build overlay and README")
			}

			// 5. What they have, in the words they will see from here on.
			fmt.Fprintln(out, "· status")
			return runStackSubcommand(cmd, "status")
		},
	}
}

// stackSteps are the verbs setup runs, built here rather than looked up through
// Parent(): the menu runs a command that was never attached to one, so a parent
// walk is a nil dereference in the very place a first-timer is most likely to
// arrive from.
var stackSteps = map[string]func() *cobra.Command{
	"init":   newStackInitCmd,
	"wire":   newStackWireCmd,
	"status": newStackStatusCmd,
}

// runStackSubcommand runs another `rig stack` verb in this process, so setup is
// the same code path as running the steps by hand rather than a second
// implementation of each that could drift from it.
func runStackSubcommand(parent *cobra.Command, name string) error {
	build, ok := stackSteps[name]
	if !ok {
		return fmt.Errorf("internal: `rig stack %s` is missing", name)
	}
	c := build()
	// A command reached this way never went through Execute, so it has no
	// context of its own; without this the step runs with a nil one.
	c.SetContext(parent.Context())
	c.SetOut(parent.OutOrStdout())
	c.SetErr(parent.ErrOrStderr())
	return c.RunE(c, nil)
}

// stackCommitSetupOutput commits the files setup produced and nothing else.
//
// The difference against what was dirty beforehand, rather than a list the
// steps report: a step that patches a member's own build file is setup's output
// too, and asking the tree is the one account that covers every step without
// each of them having to say so.
func stackCommitSetupOutput(ctx context.Context, repo *gitrepo.Repo, before []string) (bool, error) {
	after, err := repo.DirtyPaths(ctx)
	if err != nil {
		return false, err
	}
	was := make(map[string]bool, len(before))
	for _, p := range before {
		was[p] = true
	}
	var added []string
	for _, p := range after {
		if !was[p] {
			added = append(added, p)
		}
	}
	if len(added) == 0 {
		return false, nil
	}
	sort.Strings(added)
	return repo.CommitPaths(ctx, "stack: build overlay and README", added...)
}
