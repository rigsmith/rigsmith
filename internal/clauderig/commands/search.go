package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/spf13/cobra"
)

// matchStyle highlights the hit inside a result line — the brand accent, bold, so
// it stands out against dimmed context on either side.
var matchStyle = lipgloss.NewStyle().Foreground(brand.AccentClaude).Bold(true)

// NewSearchCmd builds the `search` command (alias `grep`): find the Claude Code
// session you're looking for. By default it searches your chat transcripts
// (projects/**.jsonl) across this machine's live ~/.claude and the synced staging
// repo, then groups the hits into named SESSIONS — each with its Desktop title,
// project, last-used date, and a ready-to-run `claude --resume` command — and
// matches session titles too, not just content. --raw gives grep-style lines;
// --all widens the scan to every file (config, file-history, Desktop dir).
func NewSearchCmd() *cobra.Command {
	var (
		caseSensitive bool
		liveOnly      bool
		repoOnly      bool
		all           bool
		raw           bool
		since         string
		until         string
		cwdFilter     string
		accountFilter string
	)
	cmd := &cobra.Command{
		Use:     "search <text>",
		Aliases: []string{"grep"},
		Short:   "Find a Claude session by title or content (chats across live + synced)",
		Long: "Find the session you're looking for. `search` scans your Claude Code\n" +
			"transcripts (projects/**.jsonl) in this machine's live ~/.claude and in\n" +
			"the synced staging repo (~/.clauderig/repo — may hold other machines' or\n" +
			"older sessions), then groups the hits into named sessions: each shows its\n" +
			"Desktop title, project, last-used date, and a `claude --resume` command.\n" +
			"Session titles are matched too, so a topic word finds the chat even when\n" +
			"the word isn't in the body.\n\n" +
			"  --since/--until  narrow to when the session was last used (2026-08-17,\n" +
			"          an RFC3339 timestamp, or an age like 7d/36h)\n" +
			"  --cwd   narrow to sessions whose project directory contains this text\n" +
			"  --account  narrow to one account's sessions (alias, email, or an\n" +
			"          accountUuid prefix). Reads the synced ledger, so it cannot be\n" +
			"          combined with --live, and only sessions synced since attribution\n" +
			"          was recorded carry one\n" +
			"  --raw   grep-style line output instead of grouped sessions\n" +
			"  --all   search EVERY file (config, skills, file-history, Desktop dir),\n" +
			"          not just transcripts; implies --raw (non-chat files aren't sessions)\n\n" +
			"Case-insensitive by default. Note: Desktop 'Chat' tab conversations live\n" +
			"server-side, not in these files — a miss here doesn't prove one is gone.\n" +
			"Neither does a miss for a session on a machine that hasn't synced: the\n" +
			"footer names every device and flags the ones this search couldn't see.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if liveOnly && repoOnly {
				return fmt.Errorf("--live and --repo are mutually exclusive")
			}
			// ExactArgs(1) still admits an explicitly empty/whitespace arg, which would
			// "match" every title (contains "") and dump the whole store — reject it.
			query := strings.TrimSpace(args[0])
			if query == "" {
				return fmt.Errorf("empty search term — pass text to find, e.g. `clauderig search billing`")
			}
			// Flag validation runs before any config or filesystem read, so a bad
			// --since is one immediate error rather than an error after a scan.
			sc := sessionScope{caseSensitive: caseSensitive, now: time.Now()}
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
			// Trimmed here, once, so the raw/all guard and the resolver agree.
			// resolveAccountFilter trims internally, so `--account "  "` used to
			// resolve to nothing while the guard still saw a non-empty flag —
			// grouped search then returned every session, unfiltered, with no
			// sign the flag had been ignored.
			accountFilter = strings.TrimSpace(accountFilter)
			// The filters narrow SESSIONS — a date and a project directory are
			// properties of a session, not of a grep line — so refuse rather than
			// silently ignore them.
			// accountFilter, not sc.account: the flag is resolved further down, so
			// sc.filtering() is still false here when --account is the only one
			// set — and --account --raw would then sail past this check and
			// return matches the account filter never touched.
			if (raw || all) && (sc.filtering() || accountFilter != "") {
				return fmt.Errorf("--since/--until/--cwd/--account narrow grouped sessions and can't be combined with --raw/--all")
			}

			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			targets := buildTargets(cfg, me, liveOnly, repoOnly)
			sc.me = me.Name
			sc.liveInScope = !repoOnly
			// --live takes the synced repo out of scope, and with it any claim about
			// other machines: no registry read, no footer.
			if !liveOnly {
				var ok bool
				sc.devices, ok = loadDevices()
				sc.devicesUnavailable = !ok
				sc.ledger = loadLedger()
			}
			// Resolved after the ledger loads, because the ledger is what says
			// which accounts exist to be named — and --account is meaningless
			// under --live, where no ledger is in scope at all.
			if accountFilter != "" {
				if liveOnly {
					return fmt.Errorf("--account reads the synced ledger, which --live takes out of scope")
				}
				staging, serr := config.StagingDir()
				if serr != nil {
					return serr
				}
				if sc.account, err = resolveAccountFilter(accountFilter, staging, sc.ledger); err != nil {
					return err
				}
			}

			fmt.Fprintf(out, "%s %q\n", HeaderStyle.Render("clauderig search"), query)

			// --all can't be session-grouped (config/file-history aren't sessions), so
			// it falls back to raw line output.
			if raw || all {
				return runRawSearch(cmd, targets, query, caseSensitive, all, sc)
			}
			return runSessionSearch(cmd, cfg, me, targets, query, sc, liveOnly, repoOnly)
		},
	}
	cmd.Flags().BoolVarP(&caseSensitive, "case-sensitive", "s", false, "match case exactly (default: case-insensitive)")
	cmd.Flags().BoolVar(&raw, "raw", false, "grep-style line output instead of grouped sessions")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "search every file, not just chat transcripts (implies --raw)")
	cmd.Flags().BoolVar(&liveOnly, "live", false, "search only this machine's live ~/.claude and Desktop dirs")
	cmd.Flags().BoolVar(&repoOnly, "repo", false, "search only the synced staging repo")
	cmd.Flags().StringVar(&since, "since", "", "only sessions last used on/after this day, timestamp, or age (7d)")
	cmd.Flags().StringVar(&until, "until", "", "only sessions last used on/before this day, timestamp, or age (7d)")
	cmd.Flags().StringVar(&cwdFilter, "cwd", "", "only sessions whose project directory contains this text")
	cmd.Flags().StringVar(&accountFilter, "account", "", "only sessions belonging to this account (alias, email, or accountUuid prefix); reads the synced ledger, so not with --live")
	return cmd
}

// sessResult is one session in the grouped output: its metadata (from the Desktop
// sidecar when available) plus how the query hit it.
type sessResult struct {
	id         string
	meta       session.Meta
	hasMeta    bool
	matches    int          // distinct content-line hits (deduped across source copies)
	titleMatch bool         // the query is in the session title
	first      search.Match // first content hit, for the preview snippet
	path       string       // a transcript path, for fallback title/cwd
	// seen dedups the same logical transcript line found in more than one copy of
	// the session (live + synced repo), keyed by projects-relative path + line, so
	// the hit count isn't doubled.
	seen map[string]bool
	// hitTargets is the set of search targets a content hit actually came from
	// ("cli", "desktop", "repo") — the transcript's real provenance, used for the
	// source label.
	hitTargets map[string]bool
	// cliLive is set when a transcript for this session exists in the live CLI root
	// (~/.claude) — the one `claude --resume` reads — whether via a content hit or a
	// title-only session whose transcript simply didn't match. It, not mere
	// this-machine presence, gates the resume command.
	cliLive bool
	// when is the recency sort key: the transcript's own last record timestamp,
	// else the sidecar's lastActivity, else the transcript mtime — computed once
	// so the sort is deterministic even for CLI-only sessions. See sessionTime.
	when time.Time
	// cwd is the session's resolved project directory, computed once because both
	// the --cwd filter and the rendered line need it.
	cwd string
	// act caches the one read of the transcript's tail — the session's real date,
	// its ending cwd/branch, and the client that ran it. Cached because three
	// different callers want pieces of it and the read is the expensive part.
	act      session.Activity
	actTried bool
	// led is the permanent ledger row for this session, when one exists. It is
	// what lets a session whose transcript has aged out of the synced window still
	// answer with a title, a project and a date instead of with silence.
	led    ledger.Entry
	hasLed bool
	// present reports that a transcript for this session exists SOMEWHERE we can
	// still read (live root or synced repo) — the distinction between "here" and
	// "remembered but gone".
	present bool
	// inRepo reports a transcript in the synced staging repo. Tracked apart from a
	// content hit because a title-only match on a body that IS staged should still
	// say "restore it", not fall through to the generic no-transcript note.
	inRepo bool
}

// record folds one content hit into the session, deduping the same logical line
// seen in another copy. Returns false when the hit was a duplicate.
func (r *sessResult) record(m search.Match) bool {
	if r.seen == nil {
		r.seen = map[string]bool{}
		r.hitTargets = map[string]bool{}
	}
	// Provenance covers every copy the line was found in, even the duplicate.
	r.hitTargets[m.Target] = true
	key := chatHitKey(m.Rel, m.Line)
	if r.seen[key] {
		return false
	}
	r.seen[key] = true
	r.matches++
	// First hit wins the preview; targets are searched live-first, so this prefers a
	// live transcript path for the resume command / cwd fallback.
	if r.first.Snippet == "" {
		r.first, r.path = m, m.Path
	}
	return true
}

// activity caches the one tail read three callers want pieces of. A missing or
// unparseable transcript yields the zero Activity rather than an error.
func (r *sessResult) activity() session.Activity {
	if !r.actTried && r.path != "" {
		r.actTried = true
		r.act, _ = session.LastActivity(r.path)
	}
	return r.act
}

// sessionTime ranks three answers by trust: the transcript's own last record,
// then the Desktop sidecar, then mtime. The transcript leads because it is the
// only one a copy cannot move; the sidecar still beats mtime for a transcript
// that cannot answer at all.
func sessionTime(r *sessResult) time.Time {
	if a := r.activity(); !a.At.IsZero() {
		return a.At
	}
	if !r.meta.LastActivity.IsZero() {
		return r.meta.LastActivity
	}
	if r.path != "" {
		if info, err := os.Stat(r.path); err == nil {
			return info.ModTime().UTC()
		}
	}
	return time.Time{}
}

// Target labels. cli is the live ~/.claude root (the only one `claude --resume`
// reads); desktop is the Claude Desktop app-support tree (cowork transcripts);
// repo is the synced staging copy.
const (
	cliTarget     = "cli"
	desktopTarget = "desktop"
	repoTarget    = "repo"
)

// chatHitKey identifies a transcript line independent of which copy it was found
// in. It drops everything through "projects/<slug>/" and keys on the session id
// (transcript stem, plus any subagent suffix) and line — so a session's live and
// synced copies collapse even when the project slug was rewritten for another
// machine's paths (the same reason we can't key on the slug).
func chatHitKey(rel string, line int) string {
	if i := strings.Index(rel, "projects/"); i >= 0 {
		rest := rel[i+len("projects/"):]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[j+1:] // drop the <slug>/ segment, keep <id>.jsonl (+ subagents/…)
		}
		rel = rest
	}
	return rel + "\x00" + strconv.Itoa(line)
}

// runSessionSearch is the default mode: content + title search grouped into named
// sessions, ranked by relevance (title hit, then match count, then recency), each
// with a resume command.
func runSessionSearch(cmd *cobra.Command, cfg *config.Config, me config.Machine, targets []search.Target, query string, sc sessionScope, liveOnly, repoOnly bool) error {
	roots := sessionRoots(cfg, me, liveOnly, repoOnly)
	return searchSessions(cmd.OutOrStdout(), cmd.ErrOrStderr(), me, targets, roots, query, sc)
}

// searchSessions is the grouped-session search, decoupled from cobra/config for
// testing: it takes explicit search targets and sidecar roots and writes to out
// (results) and errw (progress + warnings).
func searchSessions(out, errw io.Writer, me config.Machine, targets []search.Target, roots []session.Root, query string, sc sessionScope) error {
	caseSensitive := sc.caseSensitive
	idx := session.Build(roots)
	byAcct, acctComplete := profileByAccount()
	reprofile(idx, byAcct, acctComplete)

	hits := map[string]*sessResult{}
	get := func(id string) *sessResult {
		r := hits[id]
		if r == nil {
			m, ok := idx[id]
			r = &sessResult{id: id, meta: m, hasMeta: ok}
			hits[id] = r
		}
		return r
	}

	report, clearProgress := progressReporter(errw)
	stats, serr := search.Search(targets, search.Options{
		Query: query, CaseSensitive: caseSensitive, ChatsOnly: true,
		// Only count hits in real conversation, not injected skill-listing/system
		// records — otherwise a topic word in the skill catalog matches every session.
		Accept:   session.IsConversationLine,
		Progress: report,
	}, func(m search.Match) {
		id := session.IDFromTranscriptRel(m.Rel)
		if id == "" {
			return
		}
		get(id).record(m)
	})
	clearProgress()

	// Title matches: a session whose title contains the query, even with no body hit.
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(needle)
	}
	for id, m := range idx {
		hay := m.Title
		if !caseSensitive {
			hay = strings.ToLower(hay)
		}
		if m.Title != "" && strings.Contains(hay, needle) {
			get(id).titleMatch = true
		}
	}

	// Ledger title matches: a session sync recorded, whose transcript may since have
	// aged out of the window. Only the title is searchable for those — the body
	// isn't here to scan — so this cannot be folded into the content pass above.
	for id, e := range sc.ledger {
		r := hits[id]
		if r == nil {
			hay := e.Title
			if !caseSensitive {
				hay = strings.ToLower(hay)
			}
			if e.Title == "" || !strings.Contains(hay, needle) {
				continue
			}
			r = get(id)
			r.titleMatch = true
		}
		r.led, r.hasLed = e, true
	}

	// A session is resumable here iff a transcript for it lives in the live CLI root
	// — even a title-only match (query not in the body) is resumable if its
	// transcript exists there, so track presence independently of content hits.
	livePaths := transcriptPaths(targets, cliTarget)
	// Repo paths matter for the ledger too: a session whose body sits unmatched in
	// the synced repo is present, not aged out, and must not be told it is gone.
	repoPaths := transcriptPaths(targets, repoTarget)

	results := make([]*sessResult, 0, len(hits))
	var hidden, undated, unattributed int
	for _, r := range hits {
		// A title-only match has no recorded hit and therefore no path. Give it one
		// before anything reads a date, a cwd or a fallback title off it — live
		// first, since that is also the copy `claude --resume` would open.
		if r.path == "" {
			if p, ok := livePaths[r.id]; ok {
				r.path = p
			} else if p, ok := repoPaths[r.id]; ok {
				r.path = p
			}
		}
		r.when = sessionTime(r)
		r.cwd = resolveCwd(me, r)
		_, inLive := livePaths[r.id]
		_, inRepo := repoPaths[r.id]
		r.cliLive = r.hitTargets[cliTarget] || inLive
		r.inRepo = r.hitTargets[repoTarget] || inRepo
		r.present = r.path != "" || r.cliLive || r.inRepo
		// A ledger-only session has no sidecar and no transcript to stat, so its
		// date and project come from the row sync wrote before the body aged out.
		if r.hasLed {
			if r.when.IsZero() {
				r.when = r.led.End
			}
			if r.cwd == "" && r.led.Cwd != "" {
				r.cwd = resolvePath(me, r.led.Cwd)
			}
		}
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
		results = append(results, r)
	}
	// Rank by relevance for "find my session": a title hit is the strongest signal,
	// then more content matches, then most-recently used as a tiebreaker (precomputed
	// so CLI-only sessions still order deterministically by transcript mtime).
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.titleMatch != b.titleMatch {
			return a.titleMatch
		}
		if a.matches != b.matches {
			return a.matches > b.matches
		}
		return a.when.After(b.when)
	})

	for _, r := range results {
		renderSession(out, me, r)
	}

	fmt.Fprintln(out)
	if len(results) == 0 {
		fmt.Fprintln(out, DimStyle.Render("no matching sessions"))
		if hidden > 0 {
			// Name the filters actually in play. Listing --since/--until/--cwd
			// when --account did the excluding sends the user to widen the wrong
			// flag, and --account is the one whose exclusions are least visible.
			fmt.Fprintf(out, "%s\n", DimStyle.Render(
				"(every match was excluded by "+strings.Join(sc.activeFilters(), "/")+" — widen them)"))
		}
		fmt.Fprintln(out, DimStyle.Render("(try --raw for line-level hits, or --all to include config/file-history)"))
		fmt.Fprintln(out, DimStyle.Render("(Desktop 'Chat' tab chats are server-side and never appear here — check claude.ai)"))
	} else {
		fmt.Fprintf(out, "%s\n", OkStyle.Render(fmt.Sprintf("%d session(s) match", len(results))))
	}
	if hidden > 0 {
		msg := fmt.Sprintf("%d session(s) hidden by filters", hidden)
		if undated > 0 {
			// Undated sessions are dropped by a time filter rather than assumed into
			// range; say so, or the count reads as a bug.
			msg += fmt.Sprintf(" (%d had no date to place)", undated)
		}
		if unattributed > 0 {
			// Attribution is recorded at sync time and cannot be backfilled, so
			// these are permanently unmatchable by --account, not merely missing
			// from this run. Saying "no recorded account" rather than letting them
			// vanish is what keeps --account from reading as "you have no such
			// sessions" when it means "I cannot tell which are yours".
			msg += fmt.Sprintf(" (%d have no recorded account; only sessions synced since attribution was added carry one)", unattributed)
		}
		fmt.Fprintf(out, "%s\n", DimStyle.Render(msg))
	}
	fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
		"scanned %d transcripts, skipped %d binary", stats.FilesScanned, stats.FilesSkipped)))
	renderCoverage(out, sc)
	if serr != nil {
		fmt.Fprintf(errw, "%s\n", WarnStyle.Render("some files could not be read: "+serr.Error()))
	}
	return nil
}

// renderSession prints one grouped SEARCH result: title, id/date/model/project/
// source line, why it matched, the resume command, and a preview snippet when
// there was a content hit.
func renderSession(out interface{ Write([]byte) (int, error) }, me config.Machine, r *sessResult) {
	why := "title match"
	if r.matches > 0 {
		why = fmt.Sprintf("%d match(es)", r.matches)
	}
	renderSessionAs(out, me, r, why)
}

// renderSessionAs is renderSession with the match explanation supplied by the
// caller. An empty why omits that column, for a listing that ran no query.
func renderSessionAs(out interface{ Write([]byte) (int, error) }, me config.Machine, r *sessResult, why string) {
	title := r.meta.Title
	if title == "" && r.path != "" {
		title = session.FirstPrompt(r.path)
	}
	if title == "" && r.hasLed {
		title = r.led.Title
	}
	if title == "" {
		title = "(untitled session)"
	}
	cwd := r.cwd

	fmt.Fprintf(out, "\n%s %s\n", OkStyle.Render("●"), lipgloss.NewStyle().Bold(true).Render(title))

	meta := []string{shortID(r.id)}
	if d := sessionDate(r); d != "" {
		meta = append(meta, d)
	}
	if c := clientWithProfile(r); c != "" {
		meta = append(meta, c)
	}
	if b := sessionBranch(r); b != "" {
		meta = append(meta, b)
	}
	if r.meta.Model != "" {
		meta = append(meta, strings.TrimPrefix(r.meta.Model, "claude-"))
	}
	if cwd != "" {
		meta = append(meta, cwd)
	}
	if src := sourceLabel(r); src != "" {
		meta = append(meta, src)
	}
	fmt.Fprintf(out, "  %s\n", DimStyle.Render(strings.Join(meta, " · ")))

	if why != "" {
		fmt.Fprintf(out, "  %s   %s\n", DimStyle.Render(why), DimStyle.Render(resumeHint(r, cwd)))
	} else {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render(resumeHint(r, cwd)))
	}
	// Its own line rather than appended to the resume hint: that line already
	// carries a cd and a full uuid, and a second command on the end of it wraps
	// on any normal terminal — which is exactly where a copy/paste breaks.
	if h := desktopHint(r); h != "" {
		fmt.Fprintf(out, "  %s\n", DimStyle.Render(h))
	}
	if r.matches > 0 {
		fmt.Fprintf(out, "    %s\n", highlight(r.first))
	}
}

// desktopInstalled is memoised: renderSessionAs runs once per result, and
// whether Claude Desktop exists is a filesystem probe that cannot change
// part-way through a listing.
var desktopInstalled = sync.OnceValue(func() bool {
	_, ok := desktop.New().Installed()
	return ok
})

// desktopHint offers the Claude Desktop equivalent of the resume command.
//
// Only for a session whose transcript is in the live CLI root. `desktop open
// --session` hands Desktop a claude://resume link and Desktop imports the
// transcript from ~/.claude/projects itself, so a Desktop-only session, a
// synced-repo-only copy and a title-only match each have nothing for it to
// read — the same precondition resumeHint tests, for the same reason.
//
// Suppressed where Claude Desktop is not installed: on Linux there is no such
// app at all, and offering a command that cannot work is worse than silence.
func desktopHint(r *sessResult) string {
	if !r.cliLive || !desktopInstalled() {
		return ""
	}
	return "desktop: clauderig desktop open --session " + shQuote(r.id)
}

// resumeHint renders the action for a session. `claude --resume` reads this
// machine's ~/.claude (the CLI root), so a runnable command is offered only when a
// transcript for the session exists there. A Desktop-only session (its transcript
// lives in the app-support tree, not ~/.claude), a synced-repo-only copy, and a
// title-only match each get a note instead of a command that would fail. The path
// and id are shell-quoted for copy/paste.
func resumeHint(r *sessResult, cwd string) string {
	switch {
	case r.cliLive && cwd != "":
		return "resume: cd " + shQuote(cwd) + " && claude --resume " + shQuote(r.id)
	case r.cliLive:
		return "resume: claude --resume " + shQuote(r.id)
	case r.meta.Profile != "":
		// Claude Desktop lists only sessions filed under the account it is signed
		// in as, so this one will never appear in any install but its own.
		return "Desktop session in the " + r.meta.Profile +
			" profile — no other Desktop will list it: clauderig desktop open " + shQuote(r.meta.Profile)
	case r.hitTargets[desktopTarget]:
		// Cowork/Desktop transcript — `claude --resume` won't find it under ~/.claude.
		return "Desktop session — open it in Claude Desktop's Code tab"
	case r.inRepo:
		// The transcript is in the synced repo but not in ~/.claude — not readable
		// by `claude` until it is restored here.
		return "synced copy only — restore on this machine to resume"
	case r.hasLed && !r.present:
		// Remembered by the ledger, body no longer in the window. Saying "gone"
		// would be wrong — the blob is usually still in the sync repo's git
		// history. Only usually, though: sync squashes that history once the repo
		// passes its size floor, and the squash prunes unreachable blobs. Hedged
		// deliberately rather than promising a recovery that may not be there.
		return "aged out of the synced window — the body may still be in the sync repo's git history"
	default:
		// Title-only, no transcript here to resume from.
		return "matched by title — open it in Claude Desktop, or use --raw to search its text"
	}
}

// transcriptPaths maps session id to a transcript path under the target with this
// label, enumerating filenames only (no content read) — cheap even on the whole
// synced tree.
//
// The path matters beyond mere presence. A title-only match never records a hit,
// so without a path such a session has no file to take its date, cwd or fallback
// title from: a time filter would drop it as undated, and `--cwd` as pathless,
// with its transcript sitting right there.
func transcriptPaths(targets []search.Target, label string) map[string]string {
	paths := map[string]string{}
	for _, t := range targets {
		if t.Label != label || t.Dir == "" {
			continue
		}
		filepath.WalkDir(t.Dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(t.Dir, p)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if !strings.HasSuffix(rel, ".jsonl") || !isSessionTranscriptRel(rel) {
				return nil
			}
			if id := session.IDFromTranscriptRel(rel); id != "" {
				if _, seen := paths[id]; !seen {
					paths[id] = p
				}
			}
			return nil
		})
	}
	return paths
}

// isSessionTranscriptRel reports whether rel names a session's OWN transcript,
// projects/<slug>/<id>.jsonl, rather than a subagent file nested under it — those
// resolve to the same session id and are not what a date or a fallback title
// should come from.
//
// Measured from the "projects/" segment, never by counting slashes from the
// target root: the live root's paths start at projects/, the synced repo's start
// at cli/projects/, and a fixed depth silently matches nothing in the repo.
func isSessionTranscriptRel(rel string) bool {
	i := strings.Index(rel, "projects/")
	if i < 0 {
		return false
	}
	return strings.Count(rel[i+len("projects/"):], "/") == 1
}

// shQuote renders s as a single POSIX shell word (bash/zsh — the mac/Linux shells
// this resume line targets), single-quoting anything with whitespace or shell
// metacharacters so paths with spaces and any injected characters stay literal.
func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\r'\"\\$`&|;<>()*?[]{}#~!=") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// resolvePath maps a path recorded on some machine onto this one, falling back
// to the literal path when it maps nowhere (a project this machine has never
// had). Shared by the sidecar cwd and the ledger's.
func resolvePath(me config.Machine, p string) string {
	if res := me.Resolver().Resolve(p); res.Path != "" {
		return res.Path
	}
	return p
}

// resolveCwd returns a usable working directory for the session: the sidecar cwd
// resolved for this machine, or the transcript's recorded cwd as a fallback.
func resolveCwd(me config.Machine, r *sessResult) string {
	if r.meta.Cwd != "" {
		return resolvePath(me, r.meta.Cwd)
	}
	if r.path != "" {
		if cwd, ok, _ := project.CwdFromTranscript(r.path); ok {
			return cwd
		}
	}
	return ""
}

// sessionDate is the session's last-used day, from the precomputed recency time
// (see sessionTime).
func sessionDate(r *sessResult) string {
	if r.when.IsZero() {
		return ""
	}
	return r.when.Format("2006-01-02")
}

// sourceLabel reports where the query actually hit. For a content match that's
// the transcript's real provenance (the targets a hit came from); for a
// title-only match there's no transcript hit, so it falls back to where the
// Desktop sidecar was found. The two are distinct — a sidecar synced into the
// repo doesn't mean the transcript is there — so content hits never borrow the
// sidecar's label.
func sourceLabel(r *sessResult) string {
	var labels []string
	if len(r.hitTargets) > 0 {
		for t := range r.hitTargets {
			labels = append(labels, t)
		}
	} else {
		labels = append(labels, r.meta.Sources...)
	}
	if r.hasLed && !r.present {
		labels = append(labels, "ledger")
	}
	if len(labels) == 0 {
		return ""
	}
	sort.Strings(labels)
	return strings.Join(labels, "+")
}

// clientLabel renders an entrypoint as the app a person would name: vscode,
// desktop, cli, sdk-cs. The sdk-* variants keep their language, which is worth
// telling apart when hunting a session.
//
// Distinct from sourceLabel despite both saying "desktop": that reports which
// STORE a transcript was found in, this which CLIENT wrote it.
func clientLabel(entrypoint string) string {
	return strings.TrimPrefix(entrypoint, "claude-")
}

// profileByAccount maps accountUuid to Desktop profile name via the clauderig
// account each profile is linked to. An empty NAME marks an account two profiles
// both claim.
//
// complete reports that every profile resolved. Linking a profile to an account
// is optional, so without it an account missing from the map does not mean "no
// profile owns this" — and reprofile must not act as though it did.
func profileByAccount() (byAccount map[string]string, complete bool) {
	st, err := desktop.DefaultStore()
	if err != nil {
		return nil, false
	}
	profiles, err := st.List()
	if err != nil || len(profiles) == 0 {
		return nil, false
	}
	as, err := account.DefaultStore()
	if err != nil {
		return nil, false
	}
	accounts, err := as.List()
	if err != nil {
		return nil, false
	}
	uuidByID := map[string]string{}
	for _, a := range accounts {
		uuid := strings.ToLower(a.AccountUUID)
		if uuid == "" {
			// meta.json carries the uuid only for accounts captured after that
			// field existed. Without this fallback the map comes back empty for
			// older accounts, and does so silently.
			if raw, oerr := as.OAuth(a.ID); oerr == nil {
				uuid = account.ProfileAccountUUID(raw)
			}
		}
		if uuid != "" {
			uuidByID[a.ID] = uuid
		}
	}
	out := map[string]string{}
	complete = true
	for _, p := range profiles {
		uuid := uuidByID[p.AccountID]
		if uuid == "" {
			complete = false // unlinked profile: its sessions are unresolvable here
			continue
		}
		// Two profiles on one account would make the label a coin flip.
		if prev, dup := out[uuid]; dup && prev != p.Name {
			out[uuid] = ""
			continue
		}
		out[uuid] = p.Name
	}
	return out, complete
}

// reprofile takes each session's owning profile from the account its sidecar is
// filed under rather than from the tree it was found in. A sidecar copied
// between installs keeps its account path but lands in the other profile's
// directory, so the tree is not ownership.
//
// complete gates the one case that removes information: an account absent from a
// COMPLETE map belongs to no profile, so a label on it came from a stray copy.
// Absent from a partial map means only that we cannot tell, and the tree stands.
func reprofile(idx session.Index, byAccount map[string]string, complete bool) {
	if len(byAccount) == 0 {
		return
	}
	for id, m := range idx {
		if m.Account == "" {
			continue
		}
		name, known := byAccount[strings.ToLower(m.Account)]
		switch {
		case known:
			// Includes the empty name two profiles share: a label nobody can
			// justify is worse than none, since it decides where the user is sent.
			m.Profile = name
		case complete:
			m.Profile = ""
		default:
			continue
		}
		idx[id] = m
	}
}

// clientWithProfile qualifies the client with the Desktop profile that owns the
// session: "desktop@work" rather than a bare "desktop". Several Desktop installs
// can share a machine and all write entrypoint "claude-desktop", so the
// entrypoint alone does not say which app to open.
func clientWithProfile(r *sessResult) string {
	c := clientLabel(r.activity().Entrypoint)
	if r.meta.Profile == "" {
		return c
	}
	if c == "" {
		// No readable transcript, but the sidecar still says which Desktop filed
		// it — the useful half.
		return desktopTarget + "@" + r.meta.Profile
	}
	return c + "@" + r.meta.Profile
}

// sessionBranch is the branch a session ended on, or "" when it names nothing.
// "HEAD" is what a detached checkout or a non-repo cwd records.
func sessionBranch(r *sessResult) string {
	if b := r.activity().GitBranch; b != "HEAD" {
		return b
	}
	return ""
}

func shortID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// runRawSearch is the grep-style path (--raw / --all): line hits grouped by file.
func runRawSearch(cmd *cobra.Command, targets []search.Target, query string, caseSensitive, all bool, sc sessionScope) error {
	out := cmd.OutOrStdout()
	lastFile := ""
	matchedFiles := map[string]bool{}
	emit := func(m search.Match) {
		fileKey := m.Target + "\x00" + m.Path
		matchedFiles[fileKey] = true
		if fileKey != lastFile {
			fmt.Fprintf(out, "\n%s  %s\n", OkStyle.Render(m.Target), m.Rel)
			lastFile = fileKey
		}
		fmt.Fprintf(out, "  %s %s\n", DimStyle.Render(fmt.Sprintf("%d", m.Line)), highlight(m))
	}
	stats, serr := search.Search(targets, search.Options{
		Query: query, CaseSensitive: caseSensitive, ChatsOnly: !all,
	}, emit)

	fmt.Fprintln(out)
	if stats.Matches == 0 {
		fmt.Fprintln(out, DimStyle.Render("no matches"))
		if !all {
			fmt.Fprintln(out, DimStyle.Render("(searched chats only — pass --all to include config, file-history, and every file)"))
		}
		fmt.Fprintln(out, DimStyle.Render("(Desktop 'Chat' tab chats are server-side and never appear here — check claude.ai)"))
	} else {
		// Count files that actually contained a match, not every file scanned.
		fmt.Fprintf(out, "%s\n", OkStyle.Render(fmt.Sprintf(
			"%d match(es) in %d file(s)", stats.Matches, len(matchedFiles))))
	}
	fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
		"scanned %d files, skipped %d binary", stats.FilesScanned, stats.FilesSkipped)))
	renderCoverage(out, sc)
	if serr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", WarnStyle.Render("some files could not be read: "+serr.Error()))
	}
	return nil
}

// buildTargets resolves which roots to scan. Default is live roots + the synced
// repo; --live and --repo narrow it. Absent roots are dropped by the search.
func buildTargets(cfg *config.Config, me config.Machine, liveOnly, repoOnly bool) []search.Target {
	var targets []search.Target
	if !repoOnly {
		for _, r := range cfg.Roots {
			if !r.Enabled {
				continue
			}
			if loc, _ := cfg.RootLocation(r.ID, me); loc != "" {
				targets = append(targets, search.Target{Label: r.ID, Dir: loc})
			}
		}
	}
	if !liveOnly {
		if staging, err := config.StagingDir(); err == nil {
			targets = append(targets, search.Target{Label: "repo", Dir: staging})
		}
	}
	return targets
}

// sessionRoots is where session.Build looks for Desktop sidecars.
//
// Plural because every clauderig-managed Desktop profile is a separate install
// with its own session list; only the machine-wide one lives at the configured
// `desktop` root. Scanning that alone leaves profile-run sessions with no title
// and no way to tell which install owns them. The staged side is enumerated the
// same way, so profiles that exist only on another machine are covered too.
func sessionRoots(cfg *config.Config, me config.Machine, liveOnly, repoOnly bool) []session.Root {
	var roots []session.Root
	if !repoOnly {
		if loc, _ := cfg.RootLocation("desktop", me); loc != "" {
			roots = append(roots, session.Root{Label: desktopTarget, Base: loc})
		}
		if store, err := desktop.DefaultStore(); err == nil {
			profiles, _ := store.List() // absent store → no profiles, not an error
			for _, p := range profiles {
				roots = append(roots, session.Root{
					Label: desktopTarget, Base: p.DataDir(), Profile: p.Name})
			}
		}
	}
	if !liveOnly {
		if staging, err := config.StagingDir(); err == nil {
			roots = append(roots, session.Root{Label: repoTarget, Base: filepath.Join(staging, "desktop")})
			// Shared with `restore` rather than re-scanned here: one definition
			// of which profiles are staged, and it validates the name before it
			// becomes a path.
			for _, name := range engine.StagedProfileNames(staging) {
				roots = append(roots, session.Root{
					Label: repoTarget, Base: engine.StagedProfileDataDir(staging, name), Profile: name})
			}
		}
	}
	return roots
}

// progressReporter returns a live status callback for search.Options.Progress and
// a clear func to erase the line when done. On a non-terminal stderr (piped,
// redirected, hook context) both are no-ops, so scripted output stays clean.
func progressReporter(w io.Writer) (report func(search.Stats), clear func()) {
	f, ok := w.(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		return func(search.Stats) {}, func() {}
	}
	report = func(s search.Stats) {
		fmt.Fprintf(f, "\r\033[K%s", DimStyle.Render(fmt.Sprintf(
			"scanning… %d transcripts, %d matches", s.FilesScanned+s.FilesSkipped, s.Matches)))
	}
	clear = func() { fmt.Fprint(f, "\r\033[K") }
	return report, clear
}

// highlight renders a match snippet with the hit accent-highlighted and the
// surrounding context dimmed.
func highlight(m search.Match) string {
	s := m.Snippet
	if m.MatchAt < 0 || m.MatchAt+m.MatchLen > len(s) {
		return s // defensive: offsets out of range, show raw
	}
	before := DimStyle.Render(s[:m.MatchAt])
	hit := matchStyle.Render(s[m.MatchAt : m.MatchAt+m.MatchLen])
	after := DimStyle.Render(s[m.MatchAt+m.MatchLen:])
	return before + hit + after
}
