package sessions

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/ledger"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// Row is one session, assembled from every place it leaves a trace. Fields are
// empty rather than guessed: a session whose transcript this machine cannot
// read genuinely has no branch, and saying so beats inventing one.
type Row struct {
	ID string
	// When is the recency key: the newest timestamp inside the transcript
	// itself, else the Desktop sidecar's lastActivityAt, else the ledger, else
	// the file mtime. Zero when nothing could date it.
	When time.Time
	// Approx marks a When that did NOT come from a transcript record — a
	// sidecar, a ledger row or an mtime. Shown with a marker rather than
	// dropped: still visible, just not quietly believed, because a restore or a
	// checkout re-dates every file it touches to the same instant.
	Approx bool
	// Cwd is the project directory, resolved onto this machine's paths when the
	// session ran elsewhere.
	Cwd string
	// Account is the accountUuid that owns the session, and AccountLabel is the
	// alias or email for it. The uuid is the only thing ever recorded — CLI
	// transcripts carry no account field — so a session synced before
	// attribution existed has neither, and that is reported rather than filled
	// in with a plausible guess.
	Account      string
	AccountLabel string
	// Title is the Desktop sidecar's title, else the session's first prompt,
	// else the ledger's copy of it. Empty means genuinely untitled; the caller
	// decides how to say so.
	Title string
	// LastPrompt is the most recent thing typed in the session — what it was
	// last asked to do, as opposed to Title, which is what it was opened to do.
	LastPrompt string
	// Client is what ran it: "cli", "vscode", "desktop", an "sdk-*", each with
	// an "@profile" suffix when a clauderig-managed Desktop filed it.
	Client string
	// Branch is the git branch the session ended on, which often identifies the
	// work when the project path does not (a session one directory up, or one
	// driving a worktree).
	Branch string
	// Sources names the stores holding ANYTHING for this session — a transcript,
	// or the Desktop sidecar that files it in an install's session list.
	//
	// Sidecars count deliberately. A Claude Desktop Code-tab session writes its
	// transcript into the shared ~/.claude/projects tree and leaves only a
	// sidecar in the Desktop tree, so a transcript-only reading reported
	// "Desktop has nothing" for the very sessions Desktop is listing. It also
	// keeps this in step with Delete, which removes a store's sidecars along
	// with its transcript.
	//
	// Use Paths for "where is the conversation" and CLILive/InRepo for what can
	// be resumed or restored; those stay strictly about transcripts.
	Sources []string
	// CLILive reports a transcript in the live CLI root — the copy
	// `claude --resume` opens, and so the thing that decides whether resuming
	// here is even possible.
	CLILive bool
	// InRepo reports a transcript in the synced staging repo, which is what
	// makes "restore it" a meaningful offer rather than a dead end.
	InRepo bool
	// Present reports a readable transcript SOMEWHERE — the difference between
	// "here" and "remembered but gone".
	Present bool
	// Profile is the clauderig-managed Desktop install that filed the session,
	// empty for the machine-wide one. The only thing that says which Desktop to
	// reopen it in — the transcript lands in the shared tree either way.
	Profile string
	// Path is the transcript this row was read from, preferring the live copy.
	// Empty when no transcript survives anywhere.
	Path string
	// Paths is every transcript, keyed by the store holding it. Path names only
	// the one that was read; deleting a session has to reach all of them, and
	// "which stores is this in" is a question the window asks directly.
	Paths map[string]string
	Model string
	// Archived is the Desktop sidecar's own flag.
	Archived bool
	// Matches counts hits inside the transcript when a content search ran, and
	// Snippet is the first of them. Zero and empty otherwise — a plain listing
	// never opens a transcript body.
	// Duplicates are the OTHER transcripts on this machine carrying this same
	// session id — a session filed under two project slugs, which happens when
	// work continues in a different directory. Path is the newest of them; these
	// are the ones not chosen, and they are carried rather than dropped because
	// a copy silently discarded is how a session appears to lose a week.
	Duplicates []string

	Matches int
	Snippet string
	// Meta is the Desktop sidecar record behind Title, Cwd, Model and Profile;
	// Ledger is the permanent row that answers for a session whose transcript
	// has aged out. Both are carried rather than folded away because a caller
	// rendering a session in full wants fields the summary above does not have,
	// and re-deriving them would mean a second scan of every sidecar tree.
	Meta      session.Meta
	HasMeta   bool
	Ledger    ledger.Entry
	HasLedger bool
}

// Report counts what a listing did and, more importantly, what it left out.
// A filtered list that silently shrinks reads as "there is nothing there",
// which is the one wrong answer a session finder can give.
type Report struct {
	// Read is how many transcripts were opened.
	Read int
	// Skipped is retained at zero. Transcripts used to be excluded on mtime
	// alone when it predated the window, on the reasoning that a write can push
	// mtime forward but never back. That does not hold here: a restore rewrites
	// whole trees and the mtimes it leaves say when the restore ran, not when
	// the conversation happened — one machine's tree had 541 transcripts
	// stamped within the same minute. A session hidden from the listing is
	// exactly the failure the listing exists to diagnose, and the read it saved
	// is a bounded tail read the Stat had already half paid for.
	Skipped int
	// Hidden is sessions the filters excluded, of which Undated and
	// Unattributed are the ones excluded for lacking the information the filter
	// needed rather than for failing it.
	Hidden       int
	Undated      int
	Unattributed int
	// Approx counts returned rows whose date is not transcript-derived.
	Approx int
	// Total is how many rows survived the filters, before Limit trimmed them.
	Total int
	// Accounts are the accountUuids seen before any account filter applied, so
	// a caller can offer the choice without the list collapsing to whatever is
	// already selected.
	Accounts []string
}

// Options selects what to list. Roots and Targets are passed in rather than
// derived so tests can drive a listing over fixture directories; [Roots] and
// [Targets] build the real ones from config.
type Options struct {
	Machine config.Machine
	Roots   []session.Root
	Targets []search.Target
	Scope   Scope
	// Limit caps the returned rows, newest first. Zero means no cap.
	Limit int
	// Stores keeps only sessions held by EVERY named store, and Unsynced keeps
	// only those on this machine that the synced repo does not have — the state
	// that loses work, and the one worth being able to ask for directly.
	Stores   []string
	Unsynced bool
	// Content searches transcript BODIES for this text, keeping only sessions
	// that contain it and annotating each with its hit count and first snippet.
	//
	// Far more expensive than Text, which matches the row's own fields — this
	// opens and reads every surviving transcript — so it is a separate opt-in
	// rather than something a listing does by default. It runs last, over the
	// rows the cheap filters already kept, so a time window or a store filter
	// bounds how much gets read.
	Content string
	// CaseSensitive applies to Content.
	CaseSensitive bool
	// Text keeps only sessions matching this text, case-insensitively, across
	// everything a person can see in a row: title, last prompt, project
	// directory, branch and id. One box rather than a field per column —
	// you rarely know which of those you remember.
	//
	// It matches the row's own facts, not transcript bodies. Searching what was
	// SAID in a session is `clauderig search`, which scans every line of every
	// transcript and ranks the hits; this is a filter over an assembled list.
	Text string
	// Search matches a row's own fields OR the transcript's body — one box, the
	// whole session. Text and Content are the halves, and they AND together:
	// passing a word to both asks for sessions whose title AND body contain it,
	// which is not what typing a word into a search box means.
	//
	// The cheap half runs first and the body is only opened for rows it missed,
	// so a term that matches a title costs nothing extra.
	Search string
	// Account keeps only sessions belonging to that accountUuid. Distinct from
	// Scope.Account, which answers the CLI's --account against the ledger alone
	// and reports what it could not attribute; this one matches whatever
	// provenance the row ended up with, ledger or sidecar.
	Account string
	// OnlyID narrows the whole listing to one session. It exists so opening a
	// session's detail costs one transcript read rather than a rescan of the
	// machine, and it deliberately ignores Scope's time window — you asked for
	// that session by name, so whether it falls inside the current filters is
	// not the question.
	OnlyID string
}

// List assembles every session the given roots and targets can see, newest
// first.
//
// Its cost is one tail read per transcript with a date to establish, which is
// why the mtime pre-filter below matters: with a time window set, most
// transcripts never need opening at all.
func List(opts Options) ([]Row, Report) {
	var rep Report
	sc := opts.Scope

	idx := session.Build(opts.Roots)
	byAcct, acctComplete := ProfileByAccount()
	Reprofile(idx, byAcct, acctComplete)
	labels := AccountLabels()

	livePaths, liveDupes := TranscriptPathsAll(opts.Targets, CLISource)
	deskPaths := TranscriptPaths(opts.Targets, DesktopSource)
	repoPaths := TranscriptPaths(opts.Targets, RepoSource)

	ids := map[string]bool{}
	for _, m := range []map[string]string{livePaths, deskPaths, repoPaths} {
		for id := range m {
			ids[id] = true
		}
	}
	for id := range idx {
		ids[id] = true
	}
	for id := range sc.Ledger {
		ids[id] = true
	}

	if opts.OnlyID != "" {
		want := session.CanonicalID(opts.OnlyID)
		for id := range ids {
			if session.CanonicalID(id) != want {
				delete(ids, id)
			}
		}
	}

	rows := make([]Row, 0, len(ids))
	for id := range ids {
		meta, hasMeta := idx[session.CanonicalID(id)]
		led, hasLed := sc.Ledger[id]

		row := Row{
			ID: id, Model: meta.Model, Archived: meta.Archived, Profile: meta.Profile,
			Meta: meta, HasMeta: hasMeta, Ledger: led, HasLedger: hasLed,
		}
		row.CLILive = livePaths[id] != ""
		row.Duplicates = liveDupes[id]
		row.InRepo = repoPaths[id] != ""
		row.Paths = map[string]string{}
		held := map[string]bool{}
		for _, sc := range meta.Sidecars {
			held[sc.Label] = true
		}
		for _, src := range []struct {
			label string
			paths map[string]string
		}{{CLISource, livePaths}, {DesktopSource, deskPaths}, {RepoSource, repoPaths}} {
			if p := src.paths[id]; p != "" {
				row.Paths[src.label] = p
				held[src.label] = true
			}
		}
		for _, label := range []string{CLISource, DesktopSource, RepoSource} {
			if held[label] {
				row.Sources = append(row.Sources, label)
			}
		}
		// Live first: it is the copy `claude --resume` opens.
		switch {
		case livePaths[id] != "":
			row.Path = livePaths[id]
		case deskPaths[id] != "":
			row.Path = deskPaths[id]
		default:
			row.Path = repoPaths[id]
		}
		row.Present = row.Path != ""

		var act session.Activity
		if row.Path != "" {
			rep.Read++
			act, _ = session.LastActivity(row.Path)
			row.LastPrompt = act.LastPrompt
			row.Client = ClientWithProfile(ClientLabel(act.Entrypoint), meta.Profile)
			row.Branch = Branch(act)
			if !act.At.IsZero() {
				row.When = act.At
				row.Cwd = ResolveCwd(opts.Machine, meta, row.Path)
			}
		}
		if row.When.IsZero() {
			row.When = SessionTime(act, meta, row.Path)
			if row.Cwd == "" {
				row.Cwd = ResolveCwd(opts.Machine, meta, row.Path)
			}
		}
		if row.Client == "" {
			row.Client = ClientWithProfile("", meta.Profile)
		}
		if hasLed {
			if row.When.IsZero() {
				row.When = led.End
			}
			if row.Cwd == "" && led.Cwd != "" {
				row.Cwd = ResolvePath(opts.Machine, led.Cwd)
			}
			row.Account = led.Account
			row.AccountLabel = labels[strings.ToLower(led.Account)]
		}
		if row.Account == "" && meta.Account != "" {
			// The sidecar's own directory is account provenance too, and it is
			// the only one a Desktop-only session ever has.
			row.Account = meta.Account
			row.AccountLabel = labels[strings.ToLower(meta.Account)]
		}
		// Covers ledger-dated rows too: one written before End became
		// content-derived still holds an mtime and is never rewritten, so it
		// cannot self-correct. Over-warning is the safe direction here.
		row.Approx = !row.When.IsZero() && act.At.IsZero()

		if row.Account != "" {
			rep.Accounts = appendOnce(rep.Accounts, row.Account)
		}
		if opts.OnlyID == "" && !matchesStores(row, opts) {
			rep.Hidden++
			continue
		}
		if opts.OnlyID == "" && opts.Account != "" && !strings.EqualFold(row.Account, opts.Account) {
			rep.Hidden++
			continue
		}
		if keep, why := sc.Keep(row.When, row.Cwd, led.Account); !keep && opts.OnlyID == "" {
			rep.Hidden++
			switch why {
			case DroppedUndated:
				rep.Undated++
			case DroppedUnattributed:
				rep.Unattributed++
			}
			continue
		}
		row.Title = Title(meta, hasMeta, led, hasLed, row.Path)
		// After the title is resolved, since the title is usually what someone
		// is searching for.
		if opts.OnlyID == "" && !matchesText(row, opts.Text) {
			rep.Hidden++
			continue
		}
		if row.Approx {
			rep.Approx++
		}
		rows = append(rows, row)
	}

	if opts.Search != "" && opts.OnlyID == "" {
		kept := rows[:0]
		for _, row := range rows {
			if matchesText(row, opts.Search) {
				kept = append(kept, row)
				continue
			}
			hits, snippet := contentHits(row.Path, opts.Search, opts.CaseSensitive)
			if hits == 0 {
				rep.Hidden++
				continue
			}
			// The hit is why the row is here, so it replaces the last prompt in
			// the display — the caller shows Snippet when Matches is non-zero.
			row.Matches, row.Snippet = hits, snippet
			kept = append(kept, row)
		}
		rows = kept
	}

	if opts.Content != "" && opts.OnlyID == "" {
		kept := rows[:0]
		for _, row := range rows {
			hits, snippet := contentHits(row.Path, opts.Content, opts.CaseSensitive)
			if hits == 0 {
				rep.Hidden++
				continue
			}
			row.Matches, row.Snippet = hits, snippet
			kept = append(kept, row)
		}
		rows = kept
	}

	// The id tiebreaks so two sessions closed in the same second keep a stable
	// order between runs.
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].When.Equal(rows[j].When) {
			return rows[i].When.After(rows[j].When)
		}
		return rows[i].ID < rows[j].ID
	})
	rep.Total = len(rows)
	if opts.Limit > 0 && len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
	}
	return rows, rep
}

// contentHits counts occurrences of query inside one transcript and returns the
// first matching snippet.
//
// IsConversationLine keeps a word appearing in an injected skill catalogue or an
// attachment record from matching every session on the machine — the same filter
// `clauderig search` applies, so the two agree on what counts as a hit.
func contentHits(path, query string, caseSensitive bool) (int, string) {
	if path == "" {
		return 0, ""
	}
	var first string
	n, err := search.ScanFile(path, search.Options{
		Query: query, CaseSensitive: caseSensitive, Accept: session.IsConversationLine,
	}, func(m search.Match) {
		if first == "" {
			first = m.Snippet
		}
	})
	if err != nil {
		return 0, ""
	}
	return n, first
}

// matchesText reports whether a row matches the search text. Empty text matches
// everything, so a caller can pass the box's contents through unconditionally.
func matchesText(row Row, text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return true
	}
	// Separators normalised on both sides. A project directory is rendered in
	// this machine's own form — `C:\work\api` on Windows — and nobody types a
	// path search with backslashes.
	text = filepath.ToSlash(text)
	for _, field := range []string{row.Title, row.LastPrompt, row.Cwd, row.Branch, row.ID, row.Client} {
		if field != "" && strings.Contains(filepath.ToSlash(strings.ToLower(field)), text) {
			return true
		}
	}
	return false
}

// matchesStores applies the where filter: present in every named store, and —
// separately — missing from the synced repo while present here.
func matchesStores(row Row, opts Options) bool {
	for _, want := range opts.Stores {
		if row.Paths[want] == "" {
			return false
		}
	}
	if opts.Unsynced && (row.Paths[RepoSource] != "" || row.Paths[CLISource] == "") {
		return false
	}
	return true
}

// sessionTime ranks three answers by trust: the transcript's own last record,
// then the Desktop sidecar, then mtime. The transcript leads because it is the
// only one a copy cannot move; the sidecar still beats mtime for a transcript
// that cannot answer at all.
func SessionTime(act session.Activity, meta session.Meta, path string) time.Time {
	if !act.At.IsZero() {
		return act.At
	}
	if !meta.LastActivity.IsZero() {
		return meta.LastActivity
	}
	if path != "" {
		if info, err := os.Stat(path); err == nil {
			return info.ModTime().UTC()
		}
	}
	return time.Time{}
}

// resolveCwd prefers the sidecar's project directory, which survives a
// transcript this machine cannot read, and falls back to reading one out of the
// transcript itself.
func ResolveCwd(me config.Machine, meta session.Meta, path string) string {
	if meta.Cwd != "" {
		return ResolvePath(me, meta.Cwd)
	}
	if path != "" {
		if cwd, ok, _ := project.CwdFromTranscript(path); ok {
			return cwd
		}
	}
	return ""
}

// ClientWithProfile suffixes the client with the Desktop profile that filed the
// session. Several Desktop installs can share one machine and are otherwise
// indistinguishable.
//
// With no readable transcript there is no client at all, but the sidecar still
// says which Desktop filed the session — the useful half, and better than an
// empty column.
func ClientWithProfile(client, profile string) string {
	if profile == "" {
		return client
	}
	if client == "" {
		return DesktopSource + "@" + profile
	}
	return client + "@" + profile
}

// Branch is the git branch a session ended on. "HEAD" is a detached checkout,
// which names nothing, so it reads as no branch at all.
func Branch(act session.Activity) string {
	if b := act.GitBranch; b != "HEAD" {
		return b
	}
	return ""
}

// title walks the same ladder the CLI does: the Desktop sidecar's own title,
// then the session's first prompt, then the ledger's copy of that prompt — which
// is the only one left once a transcript ages out of the synced window.
func Title(meta session.Meta, hasMeta bool, led ledger.Entry, hasLed bool, path string) string {
	if hasMeta && meta.Title != "" {
		return strings.Join(strings.Fields(meta.Title), " ")
	}
	if path != "" {
		if t := session.FirstPrompt(path); t != "" {
			return strings.Join(strings.Fields(t), " ")
		}
	}
	if hasLed && led.Title != "" {
		return strings.Join(strings.Fields(led.Title), " ")
	}
	return ""
}
