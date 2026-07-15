package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
			cfg, err := config.LoadOrDefault()
			if err != nil {
				return err
			}
			me := config.Detect(machineName(cfg))
			targets := buildTargets(cfg, me, liveOnly, repoOnly)

			fmt.Fprintf(out, "%s %q\n", HeaderStyle.Render("clauderig search"), args[0])

			// --all can't be session-grouped (config/file-history aren't sessions), so
			// it falls back to raw line output.
			if raw || all {
				return runRawSearch(cmd, targets, args[0], caseSensitive, all)
			}
			return runSessionSearch(cmd, cfg, me, targets, args[0], caseSensitive, liveOnly, repoOnly)
		},
	}
	cmd.Flags().BoolVarP(&caseSensitive, "case-sensitive", "s", false, "match case exactly (default: case-insensitive)")
	cmd.Flags().BoolVar(&raw, "raw", false, "grep-style line output instead of grouped sessions")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "search every file, not just chat transcripts (implies --raw)")
	cmd.Flags().BoolVar(&liveOnly, "live", false, "search only this machine's live ~/.claude and Desktop dirs")
	cmd.Flags().BoolVar(&repoOnly, "repo", false, "search only the synced staging repo")
	return cmd
}

// sessResult is one session in the grouped output: its metadata (from the Desktop
// sidecar when available) plus how the query hit it.
type sessResult struct {
	id         string
	meta       session.Meta
	hasMeta    bool
	matches    int          // content-line hits
	titleMatch bool         // the query is in the session title
	first      search.Match // first content hit, for the preview snippet
	path       string       // a transcript path, for fallback title/cwd
}

// runSessionSearch is the default mode: content + title search grouped into named
// sessions, ranked by relevance (title hit, then match count, then recency), each
// with a resume command.
func runSessionSearch(cmd *cobra.Command, cfg *config.Config, me config.Machine, targets []search.Target, query string, caseSensitive, liveOnly, repoOnly bool) error {
	out := cmd.OutOrStdout()
	idx := session.Build(sessionRoots(cfg, me, liveOnly, repoOnly))

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

	report, clearProgress := progressReporter(cmd.ErrOrStderr())
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
		r := get(id)
		r.matches++
		if r.first.Snippet == "" {
			r.first, r.path = m, m.Path
		}
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

	results := make([]*sessResult, 0, len(hits))
	for _, r := range hits {
		results = append(results, r)
	}
	// Rank by relevance for "find my session": a title hit is the strongest signal,
	// then more content matches, then most-recently used as a tiebreaker.
	sort.Slice(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.titleMatch != b.titleMatch {
			return a.titleMatch
		}
		if a.matches != b.matches {
			return a.matches > b.matches
		}
		return a.meta.LastActivity.After(b.meta.LastActivity)
	})

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
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", WarnStyle.Render("some files could not be read: "+serr.Error()))
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
	if cwd != "" {
		fmt.Fprintf(out, "  %s   %s\n", DimStyle.Render(why),
			DimStyle.Render(fmt.Sprintf("resume: cd %s && claude --resume %s", cwd, r.id)))
	} else {
		fmt.Fprintf(out, "  %s   %s\n", DimStyle.Render(why),
			DimStyle.Render("resume: claude --resume "+r.id))
	}
	if r.matches > 0 {
		fmt.Fprintf(out, "    %s\n", highlight(r.first))
	}
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

// sessionDate is the session's last-used day: the sidecar time, or the
// transcript's mtime as a fallback.
func sessionDate(r *sessResult) string {
	if !r.meta.LastActivity.IsZero() {
		return r.meta.LastActivity.Format("2006-01-02")
	}
	if r.path != "" {
		if info, err := os.Stat(r.path); err == nil {
			return info.ModTime().UTC().Format("2006-01-02")
		}
	}
	return ""
}

func sourceLabel(r *sessResult) string {
	if len(r.meta.Sources) > 0 {
		s := append([]string(nil), r.meta.Sources...)
		sort.Strings(s)
		return strings.Join(s, "+")
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
func runRawSearch(cmd *cobra.Command, targets []search.Target, query string, caseSensitive, all bool) error {
	out := cmd.OutOrStdout()
	lastFile := ""
	emit := func(m search.Match) {
		fileKey := m.Target + "\x00" + m.Path
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
		fmt.Fprintf(out, "%s\n", OkStyle.Render(fmt.Sprintf(
			"%d match(es) across %d file(s)", stats.Matches, stats.FilesScanned)))
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
