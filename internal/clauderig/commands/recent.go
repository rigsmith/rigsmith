package commands

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/spf13/cobra"
)

// defaultRecentLimit caps the list so a wide --since cannot dump a thousand
// lines. What is dropped is always named in the footer — a silently truncated
// list is worse than a long one, because it answers "that session isn't here"
// when the truth is "you didn't ask for enough of them".
const defaultRecentLimit = 50

// NewRecentCmd builds the `recent` command: the sessions you actually worked on,
// newest first, with no search term.
//
// It exists because `search` answers the wrong question when you cannot remember
// a word from the chat. What you remember is that it was yesterday — and the
// lists that ought to answer that (the editor's session list, a directory sorted
// by mtime) are dated by the FILE rather than by the conversation, so a restore,
// a sync checkout, or any tool that walks ~/.claude re-dates hundreds of old
// chats to the same minute and buries the real ones.
func NewRecentCmd() *cobra.Command {
	var (
		since         string
		until         string
		cwdFilter     string
		accountFilter string
		limit         int
		liveOnly      bool
		repoOnly      bool
		long          bool
	)
	cmd := &cobra.Command{
		Use:     "recent [<text>]",
		Aliases: []string{"last"},
		Short:   "List sessions by when you last worked on them (newest first)",
		Long: "List your Claude Code sessions newest first — no search term needed.\n" +
			"Defaults to the last 24 hours.\n\n" +
			"Each session is dated by the timestamp its own last transcript record\n" +
			"carries, NOT by the file's mtime and not by Claude Desktop's session\n" +
			"list. Both of those are properties of the file rather than of the\n" +
			"conversation: restoring a backup, checking out the synced repo, or any\n" +
			"tool that rewrites ~/.claude re-dates every chat it touches to the same\n" +
			"instant, which is what makes thirty ancient sessions look like today's.\n" +
			"The record timestamps are content, so they survive all of it.\n\n" +
			"  --since/--until  bound the window (24h, 7d, 2026-08-17, or an RFC3339\n" +
			"          timestamp). `--since all` removes the lower bound.\n" +
			"  --cwd   narrow to sessions whose project directory contains this text\n" +
			"  --account  narrow to one account's sessions (alias, email, or an\n" +
			"          accountUuid prefix); reads the synced ledger, so it cannot be\n" +
			"          combined with --live\n" +
			"  --limit how many to show (default 50; 0 for no cap)\n" +
			"  --long  full detail per session, including a ready-to-run resume\n" +
			"          command — the compact list shows shortened ids\n\n" +
			"Each line shows when, a short id, the client that ran it (vscode,\n" +
			"desktop@<profile>, cli, sdk-*), the title, the git branch it ended on,\n" +
			"and the project. Several Claude Desktop installs can share one machine —\n" +
			"the machine-wide one plus each `clauderig desktop` profile — and they are\n" +
			"indistinguishable except by that profile suffix.\n\n" +
			"The branch is worth reading too: a session started one directory up,\n" +
			"or one driving a worktree, has a project path that identifies nothing.\n\n" +
			"Times are shown in local time. To find a session by what was SAID in it,\n" +
			"use `clauderig search <text>` instead.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var query string
			if len(args) == 1 {
				// An explicitly empty term would "match" every session (contains "")
				// and quietly look like a filter that did nothing.
				if query = strings.TrimSpace(args[0]); query == "" {
					return fmt.Errorf("empty search term — pass text to narrow by, or no argument to list everything in the window")
				}
			}
			if liveOnly && repoOnly {
				return fmt.Errorf("--live and --repo are mutually exclusive")
			}
			if limit < 0 {
				return fmt.Errorf("--limit cannot be negative")
			}
			sc := sessionScope{now: time.Now()}
			// "all"/"any" spell an absent lower bound, because the value that
			// actually means it — the empty string — is awkward to type past a
			// shell and reads like a mistake in a scrollback.
			if s := strings.ToLower(strings.TrimSpace(since)); s == "all" || s == "any" {
				since = ""
			}
			var err error
			if sc.since, err = parseWhen(since, sc.now, false); err != nil {
				return err
			}
			if sc.until, err = parseWhen(until, sc.now, true); err != nil {
				return err
			}
			if !sc.since.IsZero() && !sc.until.IsZero() && sc.until.Before(sc.since) {
				return fmt.Errorf("--until is before --since — nothing can match that window")
			}
			sc.cwd = strings.ToLower(strings.TrimSpace(cwdFilter))
			accountFilter = strings.TrimSpace(accountFilter)

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			targets := buildTargets(cfg, me, liveOnly, repoOnly)
			roots := sessionRoots(cfg, me, liveOnly, repoOnly)
			sc.me = me.Name
			sc.liveInScope = !repoOnly
			if !liveOnly {
				var ok bool
				sc.devices, ok = loadDevices()
				sc.devicesUnavailable = !ok
				sc.ledger = loadLedger()
			}
			if accountFilter != "" {
				if liveOnly {
					return fmt.Errorf("--account reads the synced ledger, which --live takes out of scope")
				}
				staging, serr := config.StagingDir()
				if serr != nil {
					return serr
				}
				sc.account, err = resolveAccountFilter(accountFilter, staging, sc.ledger)
				if err != nil {
					return err
				}
			}
			return listRecent(cmd.OutOrStdout(), cmd.ErrOrStderr(), me, targets, roots, sc, query, limit, long)
		},
	}
	cmd.Flags().StringVar(&since, "since", "24h", "only sessions used since this time (24h, 7d, a date, or `all`)")
	cmd.Flags().StringVar(&until, "until", "", "only sessions used before this time")
	cmd.Flags().StringVar(&cwdFilter, "cwd", "", "only sessions whose project directory contains this text")
	cmd.Flags().StringVar(&accountFilter, "account", "", "only sessions belonging to this account (alias, email, or id prefix)")
	// No -n shorthand: that letter is reserved fleet-wide for --dry-run.
	cmd.Flags().IntVar(&limit, "limit", defaultRecentLimit, "how many sessions to show (0 = no cap)")
	cmd.Flags().BoolVar(&liveOnly, "live", false, "only this machine's live ~/.claude")
	cmd.Flags().BoolVar(&repoOnly, "repo", false, "only the synced staging repo")
	cmd.Flags().BoolVarP(&long, "long", "l", false, "full detail per session, with resume commands")
	return cmd
}

// recentRow is one listed session: the shared sessResult the search path already
// knows how to render, plus the two things read straight out of the transcript
// tail — the branch it ended on, and the title to show.
type recentRow struct {
	*sessResult
	branch string
	client string
	title  string
	// approx marks a row whose date did NOT come from a transcript record — the
	// sidecar or, worst case, the file's mtime answered instead. Those are the
	// dates this command exists to distrust, so they are shown with a marker
	// rather than dropped: a stub file with no conversation in it still deserves
	// to be visible, just not to be quietly believed.
	approx bool
}

// listRecent gathers, filters, orders and prints the sessions. It is split from
// the cobra wiring so a test can drive it with explicit roots.
func listRecent(out, errw io.Writer, me config.Machine, targets []search.Target, roots []session.Root, sc sessionScope, query string, limit int, long bool) error {
	idx := session.Build(roots)
	reprofile(idx, profileByAccount())
	livePaths := transcriptPaths(targets, cliTarget)
	deskPaths := transcriptPaths(targets, desktopTarget)
	repoPaths := transcriptPaths(targets, repoTarget)

	ids := map[string]bool{}
	for _, m := range []map[string]string{livePaths, deskPaths, repoPaths} {
		for id := range m {
			ids[id] = true
		}
	}
	for id := range idx {
		ids[id] = true
	}
	for id := range sc.ledger {
		ids[id] = true
	}

	var rows []recentRow
	var read, skipped, hidden, undated, unattributed, approx, unmatched int
	for id := range ids {
		r := &sessResult{id: id, hitTargets: map[string]bool{}}
		r.meta, r.hasMeta = idx[id]
		// Live first: it is the copy `claude --resume` opens, and its mtime is the
		// one that was never rewritten by a checkout.
		switch {
		case livePaths[id] != "":
			r.path = livePaths[id]
		case deskPaths[id] != "":
			r.path = deskPaths[id]
			r.hitTargets[desktopTarget] = true
		default:
			r.path = repoPaths[id]
		}
		r.cliLive = livePaths[id] != ""
		r.inRepo = repoPaths[id] != ""
		r.present = r.path != ""
		r.led, r.hasLed = sc.ledger[id]

		row := recentRow{sessResult: r}
		if r.path != "" {
			// The cheap prefilter. A write is what sets mtime, so mtime can only
			// ever be at or AFTER the last record — never before it. A file whose
			// mtime already predates the window therefore cannot hold a record
			// inside it, and can be dropped without opening it. That is what keeps
			// a 24-hour listing from reading the tail of a thousand transcripts;
			// the direction of the inequality is what keeps it from hiding
			// anything, since the untrustworthy direction of mtime drift (a copy
			// pushing it forward) only ever costs us a needless read.
			if !sc.since.IsZero() {
				if info, serr := os.Stat(r.path); serr == nil && info.ModTime().Before(sc.since) {
					skipped++
					continue
				}
			}
			read++
			a := r.activity()
			if a.GitBranch != "HEAD" {
				// "HEAD" is what a detached checkout or a cwd outside any repo
				// records. It names nothing, so it is worse than an empty column.
				row.branch = a.GitBranch
			}
			row.client = clientWithProfile(r)
			if !a.At.IsZero() {
				r.when = a.At
				r.cwd = resolveCwd(me, r)
				if r.cwd == "" && a.Cwd != "" {
					r.cwd = resolvePath(me, a.Cwd)
				}
			}
		}
		if r.when.IsZero() {
			// No readable transcript, or one with no timestamped record. sessionTime
			// applies the rest of the ladder (sidecar, then mtime); the ledger row
			// answers for a session whose body has aged out of the synced window.
			r.when = sessionTime(r)
			if r.cwd == "" {
				r.cwd = resolveCwd(me, r)
			}
		}
		if r.hasLed {
			if r.when.IsZero() {
				r.when = r.led.End
			}
			if r.cwd == "" && r.led.Cwd != "" {
				r.cwd = resolvePath(me, r.led.Cwd)
			}
		}
		// Marked whenever the date did not come from a record we read here. That
		// covers the ledger too, deliberately: a row written before End became
		// content-derived still holds an mtime, and an aged-out session's row is
		// never rewritten, so those never self-correct. Over-warning on a handful
		// of rows is the safe direction — the failure this whole command exists to
		// prevent is a months-old chat presenting itself as yesterday's.
		row.approx = !r.when.IsZero() && r.activity().At.IsZero()
		if keep, why := sc.keep(r); !keep {
			hidden++
			switch why {
			case droppedUndated:
				undated++
			case droppedUnattributed:
				unattributed++
			}
			continue
		}
		row.title = recentTitle(r)
		if query != "" && !matchSession(r, row.title, query, sc.caseSensitive) {
			unmatched++
			continue
		}
		if row.approx {
			approx++
		}
		rows = append(rows, row)
	}

	// Newest first, with the id as a tiebreaker so two sessions closed in the same
	// second do not swap places between runs.
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].when.Equal(rows[j].when) {
			return rows[i].when.After(rows[j].when)
		}
		return rows[i].id < rows[j].id
	})

	shown := rows
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	if len(shown) == 0 {
		if query != "" {
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
				"nothing matching %q in that window (%d session(s) searched)", query, unmatched)))
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"(widen with --since, or use `clauderig search` to rank the whole store)"))
			renderCoverage(out, sc)
			return nil
		}
		fmt.Fprintln(out, DimStyle.Render("no sessions in that window"))
		if hidden > 0 {
			fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
				"(%d excluded by %s — widen them, e.g. --since 7d)", hidden, strings.Join(sc.activeFilters(), "/"))))
		}
		renderCoverage(out, sc)
		return nil
	}

	if long {
		for _, row := range shown {
			why := ""
			if query != "" {
				why = "title match"
				if row.matches > 0 {
					why = fmt.Sprintf("%d match(es)", row.matches)
				}
			}
			renderSessionAs(out, me, row.sessResult, why)
		}
	} else {
		// The client column is sized to what is actually in it rather than to a
		// fixed guess: "cli" and "desktop@<profile>" differ by more than a dozen
		// characters, and a fixed width either truncates the profile — the half
		// that says which app to open — or wastes the space on every listing that
		// has no profiles at all.
		clientW := 0
		for _, row := range shown {
			if n := len([]rune(row.client)); n > clientW {
				clientW = n
			}
		}
		// sc.now, not time.Now(): the window was computed against it, so the
		// "today"/"yesterday" labels must be read off the same clock or a listing
		// taken seconds before midnight can label its own results inconsistently.
		now := sc.now
		if now.IsZero() {
			now = time.Now()
		}
		for _, row := range shown {
			renderRecentLine(out, row, now, clientW)
		}
	}

	fmt.Fprintln(out)
	if query != "" {
		fmt.Fprintf(out, "%s\n", OkStyle.Render(fmt.Sprintf(
			"%d of %d session(s) in the window match %q", len(shown), len(rows)+unmatched, query)))
	} else {
		fmt.Fprintf(out, "%s\n", OkStyle.Render(fmt.Sprintf("%d session(s)", len(shown))))
	}
	if len(rows) > len(shown) {
		fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
			"%d more in this window — raise --limit, or narrow with --cwd", len(rows)-len(shown))))
	}
	if !long {
		fmt.Fprintf(out, "%s\n", DimStyle.Render("ids are shortened — use -l for full ids and resume commands"))
	}
	fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
		"dated by each transcript's own last record (read %d, skipped %d as too old to qualify)", read, skipped)))
	if approx > 0 {
		fmt.Fprintf(out, "%s\n", WarnStyle.Render(fmt.Sprintf(
			"%d marked ~ could not be dated from a transcript record here — those dates come from the file or from an older ledger row, so they may be when it was last copied rather than when you used it", approx)))
	}
	if undated > 0 {
		fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
			"%d session(s) had no readable date and were left out rather than assumed into the window", undated)))
	}
	if unattributed > 0 {
		fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
			"%d session(s) have no recorded account; only sessions synced since attribution was added carry one", unattributed)))
	}
	renderCoverage(out, sc)
	return nil
}

// matchSession reports whether a session answers the search term, by title or by
// what was said in it. Title first because it is free; the body is read only when
// the title misses, and only for sessions already inside the time window.
//
// Content hits are counted through session.IsConversationLine, the same filter
// `search` uses, so a word that appears in an injected skill catalog or an
// attachment record does not "match" every session on the machine.
func matchSession(r *sessResult, title, query string, caseSensitive bool) bool {
	hay, needle := title, query
	if !caseSensitive {
		hay, needle = strings.ToLower(hay), strings.ToLower(needle)
	}
	if hay != "" && strings.Contains(hay, needle) {
		r.titleMatch = true
		return true
	}
	if r.path == "" {
		return false
	}
	n, _ := search.ScanFile(r.path, search.Options{
		Query: query, CaseSensitive: caseSensitive, Accept: session.IsConversationLine,
	}, func(m search.Match) {
		if r.first.Snippet == "" {
			r.first = m
		}
	})
	r.matches = n
	return n > 0
}

// renderRecentLine prints one session as a single scannable line: when, a short
// id, the client that ran it, the title, the branch it ended on, and the project.
//
// The client column (vscode / desktop@profile / cli / sdk-*) is there because
// every one of them writes into the same ~/.claude/projects tree, so nothing about
// where a transcript SITS tells you which app to reopen it in. It is qualified by
// Desktop profile where there is one, since a machine can carry several Desktop
// installs that are indistinguishable by entrypoint alone.
//
// The branch earns its column because the project directory frequently is not
// where the work happened — a session started in a parent directory, or one
// driving a worktree, reports a cwd that identifies nothing. The branch is read
// from the last record, so it names what the session was actually on.
func renderRecentLine(out io.Writer, row recentRow, now time.Time, clientW int) {
	when := recentWhen(row.when, now)
	if row.approx {
		when = "~" + when
	}
	line := fmt.Sprintf("  %s  %s  %s  %s",
		DimStyle.Render(fmt.Sprintf("%-13s", when)),
		DimStyle.Render(shortID(row.id)),
		DimStyle.Render(padRunes(row.client, clientW)),
		lipgloss.NewStyle().Bold(true).Render(padRunes(clip(row.title, 46), 46)))
	if row.branch != "" {
		line += "  " + DimStyle.Render(clip(row.branch, 24))
	}
	if row.cwd != "" {
		line += "  " + DimStyle.Render(tildePath(row.cwd))
	}
	// A session with no branch and no project would otherwise ship its empty
	// columns as trailing spaces, which show up in diffs and copied output.
	fmt.Fprintln(out, strings.TrimRight(line, " "))
}

// recentWhen formats an instant for a human scanning a list: the clock time for
// today and yesterday (which is the whole point of a 24-hour view), the month and
// day within the current year, and the full date beyond it. Local time, because
// "was that this morning?" is a local-time question.
func recentWhen(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	l := t.Local()
	y, m, d := l.Date()
	ny, nm, nd := now.Date()
	switch {
	case y == ny && m == nm && d == nd:
		return "today " + l.Format("15:04")
	case l.After(now.AddDate(0, 0, -2)):
		// Compared against an instant rather than a calendar day so a session two
		// clock-hours ago is never labelled "yesterday" across a midnight boundary.
		if yy, ym, yd := now.AddDate(0, 0, -1).Date(); yy == y && ym == m && yd == d {
			return "yest. " + l.Format("15:04")
		}
		return l.Format("Jan 02 15:04")
	case y == ny:
		return l.Format("Jan 02 15:04")
	default:
		return l.Format("2006-01-02")
	}
}

// recentTitle is the best name we have for a session: the Desktop title, else the
// first prompt out of the transcript, else the title the ledger recorded before
// the body aged out.
func recentTitle(r *sessResult) string {
	title := r.meta.Title
	if title == "" && r.path != "" {
		title = session.FirstPrompt(r.path)
	}
	if title == "" && r.hasLed {
		title = r.led.Title
	}
	if title == "" {
		return "(untitled session)"
	}
	// A first prompt can be multi-line; a list is one line per session.
	return strings.Join(strings.Fields(title), " ")
}

// tildePath shortens a path under the home directory for display only — never
// for a command we hand back, since not every shell expands ~ the same way.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// clip truncates to n runes (not bytes, so a multi-byte title is not cut mid
// character), marking the cut with an ellipsis.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// padRunes right-pads to n runes so columns line up for titles containing
// multi-byte characters, where %-*s would pad by byte count and misalign.
func padRunes(s string, n int) string {
	if d := n - len([]rune(s)); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
