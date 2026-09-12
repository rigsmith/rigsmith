package commands

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
	"github.com/rigsmith/rigsmith/internal/codexrig/ledger"
	"github.com/rigsmith/rigsmith/internal/codexrig/sessions"
	"github.com/spf13/cobra"
)

// NewSearchCmd builds `codexrig search`.
func NewSearchCmd() *cobra.Command {
	var caseSensitive, live, repo, asJSON bool
	var since, until, cwd string
	var limit int
	cmd := &cobra.Command{
		Use:     "search <text>",
		Aliases: []string{"grep"},
		Short:   "Find a Codex session by what was said in it",
		Long: "Searches your sessions — this machine's, and every machine's if the repo\n" +
			"carries them — by title first and then by what the conversation contains.\n\n" +
			"A session found only in the repo cannot be resumed until it is restored:\n" +
			"`codex resume` reads your own Codex home, not the backup.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if live && repo {
				return errors.New("--live and --repo ask for different things; pick one")
			}
			query := strings.TrimSpace(args[0])
			if query == "" {
				return errors.New("give something to search for")
			}
			opts, err := listOptions(live, repo, since, until, cwd, limit)
			if err != nil {
				return err
			}
			opts.Content = query
			opts.Text = query
			opts.CaseSensitive = caseSensitive
			// Text and Content together mean "match a field OR the body", which
			// is what somebody typing one word expects; List opens a file only
			// where the cheap match missed.
			opts.Text = ""
			rows, rep := sessions.List(opts)

			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{
					"query": query, "sessions": rows, "read": rep.Read, "skipped": rep.Skipped, "total": rep.Total,
				})
			}
			if len(rows) == 0 {
				fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf("nothing matched %q in %d session(s)", query, rep.Read)))
				return nil
			}
			for _, r := range rows {
				renderSession(out, r, true)
			}
			if rep.Total > len(rows) {
				fmt.Fprintf(out, "\n%s\n", DimStyle.Render(fmt.Sprintf("showing %d of %d — use --limit", len(rows), rep.Total)))
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&caseSensitive, "case-sensitive", "s", false, "match case exactly")
	cmd.Flags().BoolVar(&live, "live", false, "only this machine's sessions")
	cmd.Flags().BoolVar(&repo, "repo", false, "only the synced repo's")
	cmd.Flags().StringVar(&since, "since", "", "only sessions after this (a date, or an age like 7d)")
	cmd.Flags().StringVar(&until, "until", "", "only sessions before this")
	cmd.Flags().StringVar(&cwd, "cwd", "", "only sessions whose working directory contains this")
	cmd.Flags().IntVar(&limit, "limit", 25, "show at most this many (0 = all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the results as JSON")
	return cmd
}

// NewRecentCmd builds `codexrig recent`.
func NewRecentCmd() *cobra.Command {
	var live, repo, long, asJSON bool
	var since, until, cwd string
	var limit int
	cmd := &cobra.Command{
		Use:     "recent [<text>]",
		Aliases: []string{"last"},
		Short:   "What you were working on lately",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if live && repo {
				return errors.New("--live and --repo ask for different things; pick one")
			}
			opts, err := listOptions(live, repo, since, until, cwd, limit)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				opts.Text = strings.TrimSpace(args[0])
			}
			rows, rep := sessions.List(opts)

			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, map[string]any{"sessions": rows, "read": rep.Read, "total": rep.Total})
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, DimStyle.Render("no sessions in that window"))
				return nil
			}
			for _, r := range rows {
				if long {
					renderSession(out, r, false)
					continue
				}
				fmt.Fprintf(out, "%-13s %-8s %-46s %s\n",
					recentWhen(r.When), shortID(r.ID), clip(r.Title, 46), DimStyle.Render(tildeHome(r.Cwd)))
			}
			if rep.Total > len(rows) {
				fmt.Fprintf(out, "\n%s\n", DimStyle.Render(fmt.Sprintf("showing %d of %d — use --limit", len(rows), rep.Total)))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "only this machine's sessions")
	cmd.Flags().BoolVar(&repo, "repo", false, "only the synced repo's")
	cmd.Flags().BoolVarP(&long, "long", "l", false, "full detail, with the command to resume each one")
	cmd.Flags().StringVar(&since, "since", "7d", "only sessions after this (a date, or an age like 7d; `all` for no bound)")
	cmd.Flags().StringVar(&until, "until", "", "only sessions before this")
	cmd.Flags().StringVar(&cwd, "cwd", "", "only sessions whose working directory contains this")
	cmd.Flags().IntVar(&limit, "limit", 25, "show at most this many (0 = all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the listing as JSON")
	return cmd
}

func listOptions(live, repo bool, since, until, cwd string, limit int) (sessions.Options, error) {
	var opts sessions.Options
	if limit < 0 {
		return opts, errors.New("--limit cannot be negative")
	}
	opts.Limit, opts.Cwd = limit, cwd

	now := time.Now()
	var err error
	if opts.Since, err = parseWhen(since, now, false); err != nil {
		return opts, fmt.Errorf("--since: %w", err)
	}
	if opts.Until, err = parseWhen(until, now, true); err != nil {
		return opts, fmt.Errorf("--until: %w", err)
	}
	if !opts.Since.IsZero() && !opts.Until.IsZero() && opts.Until.Before(opts.Since) {
		return opts, errors.New("--until is before --since")
	}

	home, err := codexhome.Default()
	if err != nil {
		return opts, err
	}
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return opts, err
	}
	staging, err := config.StagingDir()
	if err != nil {
		return opts, err
	}
	if !repo {
		opts.Targets = append(opts.Targets, sessions.Target{Label: sessions.Live, Dir: home})
	}
	if !live {
		opts.Targets = append(opts.Targets, sessions.Target{
			Label: sessions.Repo, Dir: filepath.Join(staging, config.RootCLI),
		})
		// The permanent index, so a session whose rollout aged out of the
		// window is still findable. Only alongside the repo: a --live listing
		// is asking what is on this machine, and a remembered session is not.
		opts.Ledger = ledger.LoadAll(staging)
	}
	_ = cfg
	return opts, nil
}

// parseWhen accepts a date, an RFC3339 timestamp, or an age like 7d/36h/90m.
func parseWhen(s string, now time.Time, endOfDay bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "all", "any":
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if endOfDay {
			return t.Add(24*time.Hour - time.Nanosecond), nil
		}
		return t, nil
	}
	d, err := parseAge(s)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-d), nil
}

func parseAge(s string) (time.Duration, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("expected a date or an age like 7d, got %q", s)
	}
	switch unit {
	case 'd':
		// Guarded: a day count large enough to overflow would silently become a
		// cutoff in the FUTURE, which hides everything instead of showing it.
		const maxDays = int(1<<62) / int(24*time.Hour)
		if n > maxDays {
			return 0, fmt.Errorf("%q is too far back to mean anything", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'm':
		return time.Duration(n) * time.Minute, nil
	}
	return 0, fmt.Errorf("expected a date or an age like 7d, got %q", s)
}

func renderSession(out io.Writer, r sessions.Row, showMatches bool) {
	title := r.Title
	if title == "" {
		title = DimStyle.Render("(untitled session)")
	}
	fmt.Fprintf(out, "%s %s\n", AccentStyle.Render("●"), HeaderStyle.Render(clip(title, 96)))

	bits := []string{shortID(r.ID), r.When.Local().Format("2006-01-02 15:04")}
	for _, v := range []string{r.Source, r.Branch, r.Model, tildeHome(r.Cwd), strings.Join(r.Stores, "+")} {
		if v != "" {
			bits = append(bits, v)
		}
	}
	fmt.Fprintf(out, "  %s\n", DimStyle.Render(strings.Join(bits, " · ")))

	if showMatches && r.Matches > 0 {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render(fmt.Sprintf("%d match(es)", r.Matches)))
	}
	if r.Snippet != "" {
		fmt.Fprintf(out, "  %s\n", r.Snippet)
	}
	fmt.Fprintf(out, "  %s\n", DimStyle.Render(resumeHint(r)))
}

// resumeHint says what can actually be done with this session, which is a
// different answer depending on where the only copy is.
func resumeHint(r sessions.Row) string {
	if r.Remembered {
		// Naming the directory is the difference between a fact and something
		// actionable: it is the pathspec to hand `git log`.
		if r.Shard != "" {
			return "aged out of the sync window — recover it from git history: " +
				"git -C ~/.codexrig/repo log --diff-filter=D --name-only -- 'cli/" + r.Shard + "/*" + r.ID + "*'"
		}
		return "aged out of the sync window — the body is in the repo's git history"
	}
	if r.Resumable {
		if r.Cwd != "" {
			return "resume: cd " + shellQuote(r.Cwd) + " && codex resume " + r.ID
		}
		return "resume: codex resume " + r.ID
	}
	return "in the backup only — `codexrig restore` brings it to this machine before `codex resume` can see it"
}

func shellQuote(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"'$`\\|&;<>()*?[]#~") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// shortID is the first dash-group of a uuid, which is enough to tell sessions
// apart by eye and short enough to sit in a column.
func shortID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func recentWhen(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	local := t.Local()
	now := time.Now()
	switch {
	case sameDay(local, now):
		return "today " + local.Format("15:04")
	case sameDay(local, now.AddDate(0, 0, -1)):
		return "yest. " + local.Format("15:04")
	case local.Year() == now.Year():
		return local.Format("Jan 02 15:04")
	default:
		return local.Format("2006-01-02")
	}
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// tildeHome shortens a path under the user's home, so a listing's last column
// stays readable.
//
// The home is symlink-resolved before comparing, because the paths this is given
// have been: a directory binding is stored resolved, so on a machine whose home
// sits behind a link the raw $HOME would never be a prefix of it and every path
// would print in full.
func tildeHome(p string) string {
	if p == "" {
		return ""
	}
	home, err := homeDir()
	if err != nil || home == "" {
		return p
	}
	for _, h := range []string{home, resolved(home)} {
		if h != "" && strings.HasPrefix(p, h) {
			return "~" + p[len(h):]
		}
	}
	return p
}

// resolved is filepath.EvalSymlinks, or "" when the path cannot be resolved.
func resolved(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return r
}
