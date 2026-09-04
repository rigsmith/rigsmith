package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/cfgfind"
	"github.com/rigsmith/rigsmith/core/confkit"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/spf13/cobra"
)

// newStackRemoveCmd takes a repo back out of a stackspace: the counterpart of
// add. Not every repo that looks fusable can be built fused, and that is a
// normal thing to discover after the import — so removal has to be a verb, and
// it has to do all three things a member touches (manifest, tree, overlay),
// because the one people forget by hand is the overlay, which then keeps a
// ProjectReference to a directory that is no longer there.
func newStackRemoveCmd() *cobra.Command {
	var force, keepTree bool
	cmd := &cobra.Command{
		Use:               "rm <repo>",
		Aliases:           []string{"remove"},
		Short:             "Remove a repo from this stackspace — manifest, tree and overlay",
		ValidArgsFunction: stackRepoCompletion,
		Long: "Removes a repo from the stackspace: its manifest entry and cursor go, its\n" +
			"directory is deleted from the tree, the build overlay is rewritten so nothing\n" +
			"still points at it, and the result is committed. Nothing outside this\n" +
			"stackspace changes — the upstream and your fork are untouched.\n\n" +
			"A repo holding work that has not left the stackspace — commits `status`\n" +
			"reports as unsent, or uncommitted edits under it — is refused, because that\n" +
			"work exists nowhere else. So is one holding files git ignores — build\n" +
			"output, a local .env — which no history gets back once the directory\n" +
			"goes. `--force` removes it anyway; `--keep-tree` leaves the directory in\n" +
			"place as an ordinary part of this repository while it stops being a member.\n\n" +
			"  rig stack rm pty-core\n" +
			"  rig stack rm pty-core --keep-tree",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			m, src, repo, err := stackspace(ctx)
			if err != nil {
				return err
			}
			name := args[0]
			if m.Repos[name] == nil {
				if err := m.requireRepos(); err != nil {
					return err
				}
				return fmt.Errorf("no stack repo %q (have: %s)", name, strings.Join(m.names(), ", "))
			}

			// The removal is a commit that stages the whole tree, so anything
			// else that is dirty would be swallowed into it — the same rule pull
			// and init apply. Edits under the prefix are a different matter, and
			// refused for a different reason, below.
			dirty, err := repo.DirtyPaths(ctx)
			if err != nil {
				return err
			}
			for _, p := range dirty {
				if !strings.HasPrefix(p, name+"/") && !stackIsManifestPath(repo.Dir, src, p) {
					return fmt.Errorf("stackspace has uncommitted changes outside %s/ — commit or stash them before removing", name)
				}
			}
			// Work that has not left the stackspace exists only here. Losing it
			// silently is the one outcome worth a refusal, and "cannot tell" has
			// to count as unsent: a history rewritten past rig's own markers is
			// exactly where a wrong guess is unrecoverable.
			u := stackUnsentWork(ctx, repo, name, dirty)
			// --keep-tree keeps the directory, and the removal commit stages
			// the whole tree: edits under the prefix would ride into "stack:
			// remove" unread. --force means "discard them", and keeping the
			// tree discards nothing, so the two do not combine over a dirty
			// prefix at all.
			if keepTree && u.Working {
				return fmt.Errorf("%s has uncommitted edits, which --keep-tree would sweep into the removal commit — commit or stash them first", name)
			}
			if !force {
				switch {
				case u.Working && u.Commits:
					return fmt.Errorf("%s has uncommitted edits and commits that have not left the stackspace — `rig stack propose %s <branch>` first, or --force to discard them", name, name)
				case u.Working:
					return fmt.Errorf("%s has uncommitted edits — commit and propose them first, or --force to discard them", name)
				case u.Commits:
					return fmt.Errorf("%s has commits that have not left the stackspace — `rig stack propose %s <branch>` first, or --force to discard them", name, name)
				case !u.Known && stackPrefixPresent(ctx, repo, name):
					// A directory with no import to compare against — a cursor
					// hand-restored, or a tree committed before any import —
					// might hold anything; only a manifest-only entry is safe.
					return fmt.Errorf("cannot tell whether %s holds unsent changes (no import commit in this history) — check with `git log -- %s/`, then --force", name, name)
				}
				// "It left as a proposal" is this history's word alone;
				// the fork's is the one that counts before the tree goes.
				if u.Proposed {
					if err := stackProposedOnFork(ctx, repo, m, name); err != nil {
						return err
					}
				}
			}

			// Ignored files are the one thing git cannot give back: never
			// tracked, so no history holds them, and the removal takes the
			// directory whole. Named, so what is about to go is not a surprise;
			// --keep-tree leaves them where they are.
			if !force && !keepTree {
				ignored, ierr := repo.IgnoredPaths(ctx, name)
				if ierr != nil {
					return ierr
				}
				if len(ignored) > 0 {
					sample := ignored
					if len(sample) > 3 {
						sample = sample[:3]
					}
					return fmt.Errorf("%s/ holds %d file(s) git ignores (%s) that go with the directory and no history gets back — move what matters, then --force, or --keep-tree", name, len(ignored), strings.Join(sample, ", "))
				}
			}

			// The manifest is edited on disk before the tree goes, so keep its
			// bytes: a removal that fails after the edit would otherwise leave
			// the member gone from the file and present in the tree, which is
			// the half-done state this verb exists to prevent.
			before, readErr := os.ReadFile(src.File)
			if err := stackForgetRepo(src, m, name); err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ %s removed from %s (entry and cursor)\n", name, src.Origin)

			// Everything up to the commit is undone the same way, whichever
			// step failed: the manifest's bytes go back, the tracked tree
			// under the prefix is restored from HEAD (ignored build output
			// under it is gone, and said so), and any overlay file already
			// rewritten or removed is put back as HEAD has it. Nothing has
			// been committed, and a member half-removed is worse than one
			// still in.
			undo := func(touched []string) string {
				if readErr == nil {
					_ = os.WriteFile(src.File, before, 0o644)
				}
				restored := "the manifest was put back"
				if !keepTree {
					if rerr := repo.ReplacePath(ctx, "HEAD", name); rerr == nil {
						restored = "the manifest and " + name + "/ were put back (ignored build output under it is gone)"
					}
				}
				// Overlay files are put back as HEAD has them. One rig
				// wrote but nobody committed is gone with that — it is
				// generated, and `rig stack wire` writes it again — but
				// that is said, not left to be discovered.
				var uncommitted []string
				for _, f := range touched {
					if !stackRestoreFromHead(ctx, repo, f) {
						uncommitted = append(uncommitted, f)
					}
				}
				if len(touched) > 0 {
					restored += ", and so were the overlay files already rewritten"
				}
				if len(uncommitted) > 0 {
					restored += fmt.Sprintf(" (%s had never been committed and is gone; `rig stack wire` writes it again)", strings.Join(uncommitted, ", "))
				}
				return restored
			}

			if keepTree {
				fmt.Fprintf(out, "  %s/ kept — it is an ordinary directory of this repository now\n", name)
			} else if err := repo.RemoveTree(ctx, name); err != nil {
				return fmt.Errorf("removing %s/: %w (%s)", name, err, undo(nil))
			} else {
				fmt.Fprintf(out, "  %s/ deleted from the tree\n", name)
			}
			// The overlay is rewritten from the members that remain — or removed
			// outright when nothing crosses between them any more — so no build
			// file keeps a redirect into the directory that just left. An
			// overlay somebody wrote by hand is the usual reason that fails;
			// an ecosystem that failed may not have been the first asked, so
			// what the earlier ones wrote is put back too.
			touched, err := stackWire(ctx, out, m, repo, "  ", true)
			if err != nil {
				return fmt.Errorf("rewriting the build overlay: %w\n%s — fix the overlay, then run `rig stack rm %s` again", err, undo(touched), name)
			}

			changed, err := repo.Commit(ctx, "stack: remove "+name)
			if err != nil {
				// A commit that fails — a hook refusing it, say — would leave
				// the whole removal staged and uncommitted, which is the
				// half-done state again, only larger.
				return fmt.Errorf("committing the removal: %w\n%s — run `rig stack rm %s` again once that is fixed", err, undo(touched), name)
			}
			// push and propose leave what they exported under refs; nothing
			// reads them once the member is gone. Dropped only now, after the
			// commit: a removal rolled back above has to find the propose ref
			// still there, or the retry would call sent work unsent.
			_ = repo.DeleteRef(ctx, "refs/rigsmith/push/"+name)
			_ = repo.DeleteRef(ctx, "refs/rigsmith/propose/"+name)
			if changed {
				fmt.Fprintf(out, "  committed: stack: remove %s\n", name)
			}
			fmt.Fprintf(out, "  the upstream and your fork are untouched; `rig stack add` fuses it again\n")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove even if the repo holds work that has not left the stackspace")
	cmd.Flags().BoolVar(&keepTree, "keep-tree", false, "leave the directory in the tree; only stop treating it as a member")
	return cmd
}

// newStackRemoveMenuCmd is rm for the menu, where there is no argument to
// type: it asks which repo, then runs the real verb.
func newStackRemoveMenuCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "rm",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			m, _, _, err := stackspace(cmd.Context())
			if err != nil {
				return err
			}
			if err := m.requireRepos(); err != nil {
				return err
			}
			names := m.names()
			opts := make([]huh.Option[string], 0, len(names))
			for _, n := range names {
				opts = append(opts, huh.NewOption(fmt.Sprintf("%s  →  %s", n, m.Repos[n].Upstream), n))
			}
			name := names[0]
			if err := huh.NewSelect[string]().
				Title("Remove which repo?").
				Description("its manifest entry, directory and overlay redirects go; upstream and fork are untouched").
				Options(opts...).Filtering(true).Value(&name).Run(); err != nil {
				return err
			}
			sub := newStackRemoveCmd()
			sub.SetContext(cmd.Context())
			sub.SetOut(out)
			sub.SetErr(cmd.ErrOrStderr())
			return sub.RunE(sub, []string{name})
		},
	}
}

// stackForgetRepo drops a repo's entry and every machine-written record of it
// (cursor, pin, last proposed branch) from the manifest file, comments and
// formatting intact, and from m.
//
// The entry is deleted in place at whatever depth the manifest keeps it. The
// records live in top-level maps: a dedicated manifest deletes one member of
// each, and an inline `stack` block in .rig.json rewrites the whole map — the
// same way a pull records a cursor there.
//
// The file is either wholly edited or wholly as it was: the entry goes first
// and the records after, each a separate write, so a record that cannot be
// updated puts the original bytes back rather than leaving the member gone
// from the file and its cursor still in it.
func stackForgetRepo(src *cfgfind.Source, m *stackManifest, name string) (err error) {
	w := confkit.Writer{SchemaURL: stackSchemaURL}
	embedded := src.Path == ""
	before, readErr := os.ReadFile(src.File)
	defer func() {
		if err != nil && readErr == nil {
			_ = os.WriteFile(src.File, before, 0o644)
		}
	}()
	entry := []string{"repos", name}
	if embedded {
		entry = []string{"stack", "repos", name}
	}
	if !w.Delete(src.File, entry) {
		return fmt.Errorf("could not remove %s from %s — take its entry out by hand, then re-run", name, src.File)
	}
	delete(m.Repos, name)

	maps := []struct {
		key string
		val map[string]string
	}{{"lastSync", m.LastSync}, {"lastPin", m.LastPin}, {"lastPropose", m.LastPropose}}
	for _, kv := range maps {
		if _, has := kv.val[name]; !has {
			continue
		}
		delete(kv.val, name)
		path := []string{kv.key}
		if embedded {
			path = []string{"stack", kv.key}
		}
		var ok bool
		switch {
		case len(kv.val) == 0:
			ok = w.Delete(src.File, path)
		case embedded:
			raw, err := json.Marshal(kv.val)
			if err != nil {
				return err
			}
			ok = w.Set(src.File, path, string(raw))
		default:
			ok = w.Delete(src.File, append(path, name))
		}
		if !ok {
			return fmt.Errorf("could not update %s in %s", kv.key, src.File)
		}
	}
	return nil
}

// stackIsManifestPath reports whether a dirty path (as git prints it, relative
// and slash-separated) is the manifest file itself, which rm is about to
// rewrite and commit anyway. Compared as paths, not as strings: the manifest's
// path is native, and on Windows that means backslashes.
func stackIsManifestPath(root string, src *cfgfind.Source, p string) bool {
	if src == nil || src.Path == "" {
		return false
	}
	rel, err := filepath.Rel(root, src.File)
	if err != nil {
		return false
	}
	return filepath.ToSlash(rel) == filepath.ToSlash(p)
}

// stackRestoreFromHead puts one path back as HEAD has it — or removes it,
// when HEAD never had it, reporting false — so a file an overlay write
// created, rewrote or deleted reads as it did before the write. Best effort:
// this runs on the way out of a failure the caller is about to report.
func stackRestoreFromHead(ctx context.Context, repo *gitrepo.Repo, rel string) bool {
	if _, err := repo.RevParse(ctx, "HEAD:"+rel); err != nil {
		_ = os.Remove(filepath.Join(repo.Dir, filepath.FromSlash(rel)))
		return false
	}
	_ = repo.ReplacePath(ctx, "HEAD", rel)
	return true
}

// stackWire computes the redirects between the members m names and writes each
// ecosystem's overlay, printing what it did with the given indent. It is the
// body of `rig stack wire`, shared with rm — which has to rewrite the overlay
// too, and must not describe it any differently. touched lists every file an
// ecosystem wrote, patched or removed, whether or not a later one then
// failed: rm puts them back on failure, since an overlay describing the
// stackspace without the member, left beside a manifest that still has it,
// is the half-done state rm exists to avoid.
//
// strict makes a failed ecosystem scan an error rather than a note: `wire`
// on its own can leave that ecosystem's overlay alone and say so, but rm is
// about to commit a member's removal, and an overlay it could not rewrite
// may still point into the directory that left.
func stackWire(ctx context.Context, out io.Writer, m *stackManifest, repo *gitrepo.Repo, indent string, strict bool) (touched []string, err error) {
	byEco, orphans, notes, failed := stackRedirects(ctx, repo.Dir, m.names(), m.publishing())
	if strict && len(failed) > 0 {
		names := make([]string, 0, len(failed))
		for id := range failed {
			names = append(names, id)
		}
		sort.Strings(names)
		var parts []string
		for _, id := range names {
			parts = append(parts, fmt.Sprintf("%s: %v", id, failed[id]))
		}
		return nil, fmt.Errorf("could not scan every ecosystem, so an overlay may still point at the member (%s)", strings.Join(parts, "; "))
	}
	// Patching a member's own build file is a commit to that repository, and
	// it travels back through `push` or `send`. Your own repos want that line;
	// a fork you contribute to should not carry rig plumbing into somebody
	// else's pull request.
	writable := m.ownedNames()
	// Reported before anything is written: a member nothing consumes is
	// usually why there was less to wire than expected.
	stackReportOrphans(out, m, orphans)
	stackReportNotes(out, notes)
	// A scan that failed is reported as such, never as "nothing to wire":
	// the overlay it would have written is still needed.
	stackReportScanFailures(out, failed)
	wired := false
	for _, eco := range stackEcosystems() {
		links := byEco[eco.Info().ID]
		// An ecosystem whose scan failed has not said "nothing crosses"; it
		// has said nothing. Acting on that as an empty answer would take an
		// overlay away over a transient failure, so it is left alone (and the
		// note above says so).
		if _, bad := failed[eco.Info().ID]; bad {
			continue
		}
		// An ecosystem with nothing to redirect is still asked, with Write set:
		// that is how an overlay left over from members that have since gone
		// is taken away rather than kept pointing at directories that left.
		resp, err := eco.LocalOverlay(ctx, localOverlayRequest(repo.Dir, links, writable))
		if err != nil {
			// An adapter can patch a member's own build file and then fail
			// on the root overlay; what it patched is touched all the same.
			touched = append(touched, resp.Fixed...)
			touched = append(touched, resp.Removed...)
			return touched, err
		}
		for f := range resp.Files {
			touched = append(touched, f)
		}
		touched = append(touched, resp.Removed...)
		touched = append(touched, resp.Fixed...)
		for _, f := range resp.Removed {
			fmt.Fprintf(out, "%s✓ %s removed — nothing crosses between members any more\n", indent, f)
		}
		if len(links) == 0 {
			continue
		}
		wired = true
		if resp.Skipped {
			fmt.Fprintf(out, "%s· %s: %s\n", indent, eco.Info().ID, resp.Reason)
			continue
		}
		for f := range resp.Files {
			fmt.Fprintf(out, "%s✓ %s — %d package(s) now resolve from this stackspace\n", indent, f, len(links))
		}
		for _, l := range links {
			fmt.Fprintf(out, "%s    %s\n", indent, l.describe())
		}
		for _, f := range resp.Fixed {
			fmt.Fprintf(out, "%s✓ %s — patched to stop hiding the overlay from what is under it\n", indent, f)
		}
		// Problems the overlay cannot fix by existing. Reported here as well
		// as in doctor, because a wire that looks like it worked and silently
		// did not is the thing this whole path exists to stop.
		for _, p := range resp.Problems {
			fmt.Fprintf(out, "%s  ✗ %s — %s\n", indent, p.Path, p.Message)
		}
	}
	if !wired && len(failed) == 0 {
		fmt.Fprintf(out, "%sno package references cross between members — nothing to wire\n", indent)
	}
	return touched, nil
}
