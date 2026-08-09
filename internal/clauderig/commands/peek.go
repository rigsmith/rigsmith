package commands

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/peek"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/spf13/cobra"
)

// defaultPeekLimit bounds `peek list`. A real repo holds hundreds of sessions
// and titles cost a blob read each, so the default shows a useful window rather
// than everything.
const defaultPeekLimit = 25

// NewPeekCmd builds the `peek` group — read another machine's sessions out of
// the synced remote without merging.
//
// During the 2026-08-07 divergence the Air couldn't see a day of the Pro's work,
// yet every one of those transcripts was already in the Air's own staging repo,
// fetched and unmerged. Reading them needed a single git command. This makes
// that a first-class operation, so catching up on another machine never requires
// taking on a merge.
func NewPeekCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek",
		Short: "Read another machine's sessions from the remote, without merging",
		Long: "Browse sessions straight out of the synced remote's object store. Read-only\n" +
			"and merge-free — a fetch is all it takes to see another machine's history.\n\n" +
			"  list         sessions on the remote, newest sync first\n" +
			"  show         print one session's transcript\n" +
			"  materialize  copy one session onto this machine (never overwrites)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if Interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPeekListCmd(), newPeekShowCmd(), newPeekMaterializeCmd())
	return cmd
}

func newPeekListCmd() *cobra.Command {
	var device string
	var limit int
	var all bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Show sessions present on the remote",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			repo, err := openStaging(ctx)
			if err != nil {
				return err
			}
			sessions, err := peek.List(ctx, repo, peek.DefaultRef)
			if err != nil {
				return remoteReadHint(err)
			}
			if len(sessions) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no sessions on the remote yet"))
				return nil
			}

			machines := peek.Machines(sessions)
			sessions = peek.FilterMachine(sessions, device)
			if len(sessions) == 0 {
				return fmt.Errorf("no sessions from %q on the remote (machines seen: %v)", device, machines)
			}

			shown := sessions
			if !all && limit > 0 && len(shown) > limit {
				shown = shown[:limit]
			}
			shown = peek.Titles(ctx, repo, peek.DefaultRef, shown)

			fmt.Fprintln(out, HeaderStyle.Render("clauderig peek"))
			for _, s := range shown {
				title := s.Title
				if title == "" {
					title = DimStyle.Render("(no readable prompt)")
				}
				fmt.Fprintf(out, "  %s  %s\n", OkStyle.Render(shortID(s.ID)), title)
				fmt.Fprintf(out, "      %s\n", DimStyle.Render(fmt.Sprintf("%s · %s · %s",
					orDash(s.Machine), s.Slug, humanizeSince(s.SyncedAt))))
			}
			// Never let a bounded listing read as the whole set.
			if len(shown) < len(sessions) {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf(
					"showing %d of %d — use --all or --limit", len(shown), len(sessions))))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&device, "device", "", "only sessions last synced by this machine")
	cmd.Flags().IntVar(&limit, "limit", defaultPeekLimit, "how many sessions to show")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "show every session, ignoring --limit")
	return cmd
}

func newPeekShowCmd() *cobra.Command {
	var raw bool
	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Print one remote session's transcript",
		Long: "Read a session out of the remote and print it. An id prefix is enough as long\n" +
			"as it's unambiguous. Nothing is written to this machine.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			repo, err := openStaging(ctx)
			if err != nil {
				return err
			}
			s, err := findRemoteSession(ctx, repo, args[0])
			if err != nil {
				return err
			}
			blob, err := peek.Read(ctx, repo, peek.DefaultRef, s)
			if err != nil {
				return err
			}

			if raw {
				_, err := out.Write(blob)
				return err
			}
			fmt.Fprintln(out, HeaderStyle.Render("session "+s.ID))
			fmt.Fprintf(out, "  %s\n\n", DimStyle.Render(fmt.Sprintf("%s · %s · synced %s",
				orDash(s.Machine), s.Slug, humanizeSince(s.SyncedAt))))
			return renderTranscript(out, blob)
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "print the raw JSONL instead of the rendered conversation")
	return cmd
}

func newPeekMaterializeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "materialize <session-id>",
		Aliases: []string{"get"},
		Short:   "Copy a remote session onto this machine",
		Long: "Write one remote session into ~/.claude/projects so you can resume it here.\n\n" +
			"Strictly additive: it creates nothing but that one transcript, and refuses if\n" +
			"a session with the same id already exists locally — the local copy may still\n" +
			"be running, and overwriting it would lose every turn since the remote's\n" +
			"snapshot. Nothing else about this machine is touched.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.DetectFor(cfg)
			cliLoc, st := cfg.RootLocation("cli", me)
			if st != pathmap.StatusResolved {
				return fmt.Errorf("can't resolve this machine's CLI root")
			}
			repo, err := openStaging(ctx)
			if err != nil {
				return err
			}
			s, err := findRemoteSession(ctx, repo, args[0])
			if err != nil {
				return err
			}

			staging, _ := config.StagingDir()
			man, _ := manifest.Load(staging) // nil is fine — the slug is then used as-is

			res, err := peek.Materialize(ctx, repo, peek.DefaultRef, s,
				filepath.Join(cliLoc, "projects"), man, me.Resolver())
			if errors.Is(err, peek.ErrExists) {
				fmt.Fprintf(out, "  %s\n", WarnStyle.Render("already here: "+res.Path))
				fmt.Fprintln(out, DimStyle.Render("  Left untouched — the local copy may have moved on since the remote's snapshot."))
				return nil
			}
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "%s %s\n", OkStyle.Render("Brought over"), s.ID)
			fmt.Fprintf(out, "  %s\n", DimStyle.Render(res.Path))
			if res.Rewrote {
				fmt.Fprintf(out, "  %s\n", DimStyle.Render("project folder rewritten for this machine: "+res.Slug))
			}
			return nil
		},
	}
}

// openStaging opens the local staging repo, with a clear message when there
// isn'tone yet.
func openStaging(ctx context.Context) (*gitrepo.Repo, error) {
	staging, err := config.StagingDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(staging, ".git")); err != nil {
		return nil, fmt.Errorf("no staging repo yet — run `clauderig sync` or `clauderig init` first")
	}
	return gitrepo.Open(ctx, staging)
}

func findRemoteSession(ctx context.Context, repo *gitrepo.Repo, idOrPrefix string) (peek.Session, error) {
	sessions, err := peek.List(ctx, repo, peek.DefaultRef)
	if err != nil {
		return peek.Session{}, remoteReadHint(err)
	}
	return peek.Find(sessions, idOrPrefix)
}

// remoteReadHint turns "that ref doesn't exist" into the actionable version:
// peek reads origin/main, which is only there once something has been fetched.
func remoteReadHint(err error) error {
	return fmt.Errorf("%w\n(peek reads %s — run `clauderig pull` first if this machine has never fetched)",
		err, peek.DefaultRef)
}

// renderTranscript prints the conversation rather than the JSONL, skipping the
// harness bookkeeping that makes a raw transcript unreadable. `--raw` is there
// for when you want the bytes.
func renderTranscript(out io.Writer, blob []byte) error {
	sc := bufio.NewScanner(bytes.NewReader(blob))
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20) // transcript lines get large
	for sc.Scan() {
		line := sc.Text()
		if !session.IsConversationLine(line) {
			continue
		}
		who, text, ok := session.MessageText(line)
		if !ok {
			continue
		}
		style := DimStyle
		if who == "user" {
			style = OkStyle
		}
		if _, err := fmt.Fprintf(out, "%s %s\n\n", style.Render(who+":"), text); err != nil {
			return err
		}
	}
	return sc.Err()
}
