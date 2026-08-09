package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
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
		asJSON        bool
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
			"  --raw   grep-style line output instead of grouped sessions\n" +
			"  --all   search EVERY file (config, skills, file-history, Desktop dir),\n" +
			"          not just transcripts; implies --raw (non-chat files aren't sessions)\n\n" +
			"Case-insensitive by default. Note: Desktop 'Chat' tab conversations live\n" +
			"server-side, not in these files — a miss here doesn't prove one is gone.",
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
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			targets := buildTargets(cfg, me, liveOnly, repoOnly)

			// Under --json, stdout carries the document and nothing else — a
			// banner ahead of it breaks every consumer that pipes this.
			if !asJSON {
				fmt.Fprintf(out, "%s %q\n", HeaderStyle.Render("clauderig search"), query)
			}

			// --all can't be session-grouped (config/file-history aren't sessions), so
			// it falls back to raw line output.
			if raw || all {
				return runRawSearch(cmd, targets, query, caseSensitive, all)
			}
			return runSessionSearch(cmd, cfg, me, targets, query, caseSensitive, liveOnly, repoOnly, asJSON)
		},
	}
	cmd.Flags().BoolVarP(&caseSensitive, "case-sensitive", "s", false, "match case exactly (default: case-insensitive)")
	cmd.Flags().BoolVar(&raw, "raw", false, "grep-style line output instead of grouped sessions")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "search every file, not just chat transcripts (implies --raw)")
	cmd.Flags().BoolVar(&liveOnly, "live", false, "search only this machine's live ~/.claude and Desktop dirs")
	cmd.Flags().BoolVar(&repoOnly, "repo", false, "search only the synced staging repo")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit matching sessions as JSON (implies grouped sessions, not --raw)")
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
	// when is the recency sort key: the sidecar's lastActivity, else the transcript
	// mtime, computed once so the sort is deterministic even for CLI-only sessions.
	when time.Time
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

// sessionTime is the session's recency: the sidecar's lastActivity, else the
// transcript's mtime, else zero. Used for both the sort key and the displayed
// date so CLI-only sessions (no sidecar) still order by recency.
func sessionTime(r *sessResult) time.Time {
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
func runSessionSearch(cmd *cobra.Command, cfg *config.Config, me config.Machine, targets []search.Target, query string, caseSensitive, liveOnly, repoOnly, asJSON bool) error {
	roots := sessionRoots(cfg, me, liveOnly, repoOnly)
	return searchSessions(cmd.OutOrStdout(), cmd.ErrOrStderr(), me, targets, roots, query, caseSensitive, asJSON)
}

// searchSessions is the grouped-session search, decoupled from cobra/config for
// testing: it takes explicit search targets and sidecar roots and writes to out
// (results) and errw (progress + warnings).
func searchSessions(out, errw io.Writer, me config.Machine, targets []search.Target, roots []session.Root, query string, caseSensitive, asJSON bool) error {
	idx := session.Build(roots)

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

	// A session is resumable here iff a transcript for it lives in the live CLI root
	// — even a title-only match (query not in the body) is resumable if its
	// transcript exists there, so track presence independently of content hits.
	liveIDs := liveCLITranscriptIDs(targets)

	results := make([]*sessResult, 0, len(hits))
	for _, r := range hits {
		r.when = sessionTime(r)
		r.cliLive = r.hitTargets[cliTarget] || liveIDs[r.id]
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

	if asJSON {
		return emitSearchJSON(out, me, query, results, stats.FilesScanned, stats.FilesSkipped)
	}

	for _, r := range results {
		renderSession(out, me, r)
	}

	fmt.Fprintln(out)
	if len(results) == 0 {
		fmt.Fprintln(out, DimStyle.Render("no matching sessions"))
		fmt.Fprintln(out, DimStyle.Render("(try --raw for line-level hits, or --all to include config/file-history)"))
		fmt.Fprintln(out, DimStyle.Render("(Desktop 'Chat' tab chats are server-side and never appear here — check claude.ai)"))
	} else {
		fmt.Fprintf(out, "%s\n", OkStyle.Render(fmt.Sprintf("%d session(s) match", len(results))))
	}
	fmt.Fprintf(out, "%s\n", DimStyle.Render(fmt.Sprintf(
		"scanned %d transcripts, skipped %d binary", stats.FilesScanned, stats.FilesSkipped)))
	if serr != nil {
		fmt.Fprintf(errw, "%s\n", WarnStyle.Render("some files could not be read: "+serr.Error()))
	}
	return nil
}

// renderSession prints one grouped session: title, id/date/model/project/source
// line, the resume command, and a preview snippet when there was a content hit.
func renderSession(out interface{ Write([]byte) (int, error) }, me config.Machine, r *sessResult) {
	title := r.meta.Title
	if title == "" && r.path != "" {
		title = session.FirstPrompt(r.path)
	}
	if title == "" {
		title = "(untitled session)"
	}
	cwd := resolveCwd(me, r)

	fmt.Fprintf(out, "\n%s %s\n", OkStyle.Render("●"), lipgloss.NewStyle().Bold(true).Render(title))

	meta := []string{shortID(r.id)}
	if d := sessionDate(r); d != "" {
		meta = append(meta, d)
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

	why := "title match"
	if r.matches > 0 {
		why = fmt.Sprintf("%d match(es)", r.matches)
	}
	fmt.Fprintf(out, "  %s   %s\n", DimStyle.Render(why), DimStyle.Render(resumeHint(r, cwd)))
	if r.matches > 0 {
		fmt.Fprintf(out, "    %s\n", highlight(r.first))
	}
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
	case r.hitTargets[desktopTarget]:
		// Cowork/Desktop transcript — `claude --resume` won't find it under ~/.claude.
		return "Desktop session — open it in Claude Desktop's Code tab"
	case r.matches > 0:
		// The transcript was found only in the synced repo — not readable by `claude`.
		return "synced copy only — restore on this machine to resume"
	default:
		// Title-only, no transcript here to resume from.
		return "matched by title — open it in Claude Desktop, or use --raw to search its text"
	}
}

// liveCLITranscriptIDs collects the session ids that have a transcript in the live
// CLI root (~/.claude) — the sessions `claude --resume` can actually open. Only the
// cli target qualifies; Desktop and repo transcripts aren't read by resume. It
// enumerates filenames only (no content read), so it's cheap on the small CLI
// tree, and lets a title-only match still offer resume when its body just didn't
// hit the query.
func liveCLITranscriptIDs(targets []search.Target) map[string]bool {
	ids := map[string]bool{}
	for _, t := range targets {
		if t.Label != cliTarget || t.Dir == "" {
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
			if !strings.HasSuffix(rel, ".jsonl") {
				return nil
			}
			if id := session.IDFromTranscriptRel(rel); id != "" {
				ids[id] = true
			}
			return nil
		})
	}
	return ids
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

// resolveCwd returns a usable working directory for the session: the sidecar cwd
// resolved for this machine, or the transcript's recorded cwd as a fallback.
func resolveCwd(me config.Machine, r *sessResult) string {
	if r.meta.Cwd != "" {
		if res := me.Resolver().Resolve(r.meta.Cwd); res.Path != "" {
			return res.Path
		}
		return r.meta.Cwd
	}
	if r.path != "" {
		if cwd, ok, _ := project.CwdFromTranscript(r.path); ok {
			return cwd
		}
	}
	return ""
}

// sessionDate is the session's last-used day, from the precomputed recency time
// (sidecar lastActivity, else transcript mtime).
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
	if len(labels) == 0 {
		return ""
	}
	sort.Strings(labels)
	return strings.Join(labels, "+")
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
func runRawSearch(cmd *cobra.Command, targets []search.Target, query string, caseSensitive, all bool) error {
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

// sessionRoots is where session.Build looks for Desktop sidecars: the live
// Desktop dir and/or the synced repo's desktop tree, matching the search scope.
func sessionRoots(cfg *config.Config, me config.Machine, liveOnly, repoOnly bool) []session.Root {
	var roots []session.Root
	if !repoOnly {
		if loc, _ := cfg.RootLocation("desktop", me); loc != "" {
			roots = append(roots, session.Root{Label: "desktop", Base: loc})
		}
	}
	if !liveOnly {
		if staging, err := config.StagingDir(); err == nil {
			roots = append(roots, session.Root{Label: "repo", Base: filepath.Join(staging, "desktop")})
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
