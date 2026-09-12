package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rigsmith/rigsmith/core/climenu"
	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/peek"
	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
	"github.com/spf13/cobra"
)

// NewPeekCmd builds `codexrig peek`: read another machine's sessions out of the
// sync repo without restoring anything.
func NewPeekCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek",
		Short: "Read another machine's sessions without restoring",
		Long: "Another computer synced a conversation you want to look at. A fetch has\n" +
			"already brought it down; this reads it straight out of the repo, so you do\n" +
			"not have to write that machine's whole setup over yours to see one session.\n\n" +
			"`peek get` copies one onto this machine, where `codex resume` can open it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && interactive() {
				return climenu.Run(cmd)
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPeekListCmd(), newPeekShowCmd(), newPeekGetCmd())
	return cmd
}

// openStaging opens the sync repo, with the message somebody actually needs when
// there is not one.
func openStaging(cmd *cobra.Command) (*gitrepo.Repo, error) {
	staging, err := config.StagingDir()
	if err != nil {
		return nil, err
	}
	repo, err := gitrepo.Open(cmd.Context(), staging)
	if err != nil {
		return nil, errors.New("no sync repo on this machine yet — run `codexrig init`, then `codexrig pull`")
	}
	return repo, nil
}

// peekSessions lists what the repo holds, with the hint that the answer depends
// on having fetched recently.
func peekSessions(cmd *cobra.Command, ref, device string) (*gitrepo.Repo, []peek.Session, error) {
	repo, err := openStaging(cmd)
	if err != nil {
		return nil, nil, err
	}
	sessions, err := peek.List(cmd.Context(), repo, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("%w — `codexrig pull` fetches what the other machines have pushed", err)
	}
	if device != "" {
		sessions = peek.FilterMachine(sessions, device)
	}
	return repo, sessions, nil
}

func newPeekListCmd() *cobra.Command {
	var device, ref string
	var limit int
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Sessions in the sync repo, whichever machine put them there",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			repo, sessions, err := peekSessions(cmd, ref, device)
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no sessions in the repo — are rollouts being synced? (`codexrig config get syncSessions`)"))
				return nil
			}
			total := len(sessions)
			if !all && limit > 0 && len(sessions) > limit {
				sessions = sessions[:limit]
			}
			// Titles cost a bounded read each, so only for the rows shown.
			sessions = peek.Titles(cmd.Context(), repo, ref, sessions)

			if asJSON {
				return writeJSON(out, map[string]any{"sessions": sessions, "total": total})
			}
			for _, s := range sessions {
				title := s.Title
				if title == "" {
					title = DimStyle.Render("(untitled session)")
				}
				fmt.Fprintf(out, "%s %s\n", AccentStyle.Render("●"), clip(title, 92))
				bits := []string{shortID(s.ID)}
				if s.Machine != "" {
					bits = append(bits, s.Machine)
				}
				if !s.SyncedAt.IsZero() {
					bits = append(bits, "synced "+humanSince(s.SyncedAt))
				}
				if s.Cwd != "" {
					bits = append(bits, tildeHome(s.Cwd))
				}
				fmt.Fprintf(out, "  %s\n", DimStyle.Render(strings.Join(bits, " · ")))
			}
			if total > len(sessions) {
				fmt.Fprintf(out, "\n%s\n", DimStyle.Render(fmt.Sprintf("showing %d of %d — use --all or --limit", len(sessions), total)))
			}
			if names := peek.Machines(sessions); len(names) > 1 {
				fmt.Fprintf(out, "%s\n", DimStyle.Render("machines: "+strings.Join(names, ", ")+" — filter with --device"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&device, "device", "", "only sessions synced by this machine")
	cmd.Flags().StringVar(&ref, "ref", peek.DefaultRef, "the git ref to read")
	cmd.Flags().IntVar(&limit, "limit", 25, "show at most this many")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "show every session")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the listing as JSON")
	return cmd
}

func newPeekShowCmd() *cobra.Command {
	var ref string
	var raw bool
	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Print a session's conversation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			repo, sessions, err := peekSessions(cmd, ref, "")
			if err != nil {
				return err
			}
			s, err := peek.Find(sessions, args[0])
			if err != nil {
				return err
			}
			body, err := peek.Read(cmd.Context(), repo, ref, s)
			if err != nil {
				return err
			}
			if raw {
				_, err := out.Write(body)
				return err
			}
			for _, line := range strings.Split(string(body), "\n") {
				if strings.TrimSpace(line) == "" || !rollout.IsConversationLine(line) {
					continue
				}
				role, text, ok := rollout.MessageText(line)
				if !ok || strings.TrimSpace(text) == "" || role == rollout.RoleDeveloper {
					// Developer turns are Codex's own injected context — the
					// sandbox rules, the project's AGENTS.md — and printing
					// them buries the conversation in boilerplate.
					continue
				}
				fmt.Fprintf(out, "%s: %s\n\n", HeaderStyle.Render(role), text)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", peek.DefaultRef, "the git ref to read")
	cmd.Flags().BoolVar(&raw, "raw", false, "print the rollout's bytes instead of the conversation")
	return cmd
}

func newPeekGetCmd() *cobra.Command {
	var ref string
	cmd := &cobra.Command{
		Use:     "get <session-id>",
		Aliases: []string{"materialize"},
		Short:   "Copy one session onto this machine, so `codex resume` can open it",
		Long: "Writes the rollout into your Codex home and nothing else — not the other\n" +
			"machine's config, not its skills.\n\n" +
			"It refuses rather than overwrite: a rollout already here is at least as\n" +
			"complete, and may be the file a live session is writing into right now.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			repo, sessions, err := peekSessions(cmd, ref, "")
			if err != nil {
				return err
			}
			s, err := peek.Find(sessions, args[0])
			if err != nil {
				return err
			}
			home, err := codexhome.Default()
			if err != nil {
				return err
			}
			got, err := peek.Get(cmd.Context(), repo, ref, s, home)
			if errors.Is(err, peek.ErrExists) {
				// Not a failure. Somebody asking for a session they already have
				// wants to open it, and the answer is the command that does.
				fmt.Fprintf(out, "%s\n", DimStyle.Render("already on this machine"))
				fmt.Fprintf(out, "  %s\n", resumeLine(s))
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s (%s)\n", OkStyle.Render("wrote"), got.Path, humanBytes(int64(got.Bytes)))
			fmt.Fprintf(out, "  %s\n", resumeLine(s))
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", peek.DefaultRef, "the git ref to read")
	return cmd
}

func resumeLine(s peek.Session) string {
	if s.Cwd != "" {
		return "cd " + shellQuote(s.Cwd) + " && codex resume " + s.ID
	}
	return "codex resume " + s.ID
}
