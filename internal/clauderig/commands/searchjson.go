package commands

import (
	"encoding/json"
	"io"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
)

// SearchHit is one matching session in `search --json`.
//
// It carries what the styled output shows plus the two things a script actually
// needs and the terminal can't give it: the resume command, already shell-quoted
// (cwds contain spaces), and whether the transcript is present in the live CLI
// root — `claude --resume` only works for those.
type SearchHit struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Cwd   string `json:"cwd,omitempty"`
	Model string `json:"model,omitempty"`
	// Matches counts distinct content lines hit, deduped across copies of the
	// same session.
	Matches int `json:"matches"`
	// TitleMatch means the query hit the session's title, the strongest signal
	// and the reason this hit may rank above ones with more content matches.
	TitleMatch bool      `json:"titleMatch"`
	Source     string    `json:"source,omitempty"`
	LastUsed   time.Time `json:"lastUsed,omitempty"`
	Resumable  bool      `json:"resumable"`
	Resume     string    `json:"resume,omitempty"`
	Preview    string    `json:"preview,omitempty"`
}

// SearchJSON is the `search --json` document. Counts sit alongside the hits so a
// caller can tell "no matches" from "nothing was scanned" — a distinction the
// styled output makes in prose.
type SearchJSON struct {
	Query    string      `json:"query"`
	Sessions []SearchHit `json:"sessions"`
	Scanned  int         `json:"scanned"`
	Skipped  int         `json:"skipped"`
}

func emitSearchJSON(out io.Writer, me config.Machine, query string, results []*sessResult, scanned, skipped int) error {
	doc := SearchJSON{Query: query, Sessions: make([]SearchHit, 0, len(results)), Scanned: scanned, Skipped: skipped}

	for _, r := range results {
		title := sessionTitle(r)
		hit := SearchHit{
			// r.cwd, not a fresh resolve: it carries the ledger fallback for a
			// session whose transcript has aged out, and without it the resume
			// command below runs wherever the caller happens to be standing.
			ID: r.id, Title: title, Cwd: r.cwd, Model: r.meta.Model,
			Matches: r.matches, TitleMatch: r.titleMatch, Source: sourceLabel(r),
			LastUsed: r.when, Resumable: r.cliLive,
		}
		// Only a live-CLI session gets a runnable command; anything else would
		// be a command that fails. Same rule the styled output applies.
		if r.cliLive {
			hit.Resume = "claude --resume " + shQuote(r.id)
			if hit.Cwd != "" {
				hit.Resume = "cd " + shQuote(hit.Cwd) + " && " + hit.Resume
			}
		}
		hit.Preview = r.first.Snippet
		doc.Sessions = append(doc.Sessions, hit)
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// RecentHit is one session in `recent --json`.
//
// It is SearchHit minus the two fields that only mean something for a query —
// Matches and TitleMatch — plus the two this listing knows and search does not:
// the git branch, and whether the date is approximate.
//
// Deliberately a separate type rather than a reused one with empty fields. A
// caller reading `matches: 0` from a listing that never searched anything would
// reasonably conclude there were no matches, which is a different claim.
type RecentHit struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Cwd   string `json:"cwd,omitempty"`
	Model string `json:"model,omitempty"`
	// Branch and Client are what the compact listing shows beside the title.
	Branch string `json:"branch,omitempty"`
	Client string `json:"client,omitempty"`
	Source string `json:"source,omitempty"`
	// LastUsed is when the session last moved. Approx means that came from
	// something other than a transcript record — a sidecar, the ledger, a file
	// mtime — so it is shown rather than quietly believed.
	LastUsed  time.Time `json:"lastUsed,omitempty"`
	Approx    bool      `json:"approx,omitempty"`
	Resumable bool      `json:"resumable"`
	Resume    string    `json:"resume,omitempty"`
}

// RecentJSON is the `recent --json` document. The counts sit alongside the rows
// for the same reason search's do: so a caller can tell "nothing in the window"
// from "nothing could be read".
type RecentJSON struct {
	Query    string      `json:"query,omitempty"`
	Sessions []RecentHit `json:"sessions"`
	// Total is how many matched before --limit; Sessions is what fits.
	Total        int `json:"total"`
	Read         int `json:"read"`
	Skipped      int `json:"skipped"`
	Hidden       int `json:"hidden"`
	Undated      int `json:"undated"`
	Unattributed int `json:"unattributed"`
}

func emitRecentJSON(out io.Writer, query string, rows []recentRow, total, read, skipped, hidden, undated, unattributed int) error {
	doc := RecentJSON{
		Query: query, Sessions: make([]RecentHit, 0, len(rows)), Total: total,
		Read: read, Skipped: skipped, Hidden: hidden, Undated: undated, Unattributed: unattributed,
	}
	for _, row := range rows {
		hit := RecentHit{
			ID: row.id, Title: row.title, Cwd: row.cwd, Model: row.meta.Model,
			Branch: row.branch, Client: row.client, Source: sourceLabel(row.sessResult),
			LastUsed: row.when, Approx: row.approx, Resumable: row.cliLive,
		}
		// Only a live-CLI session gets a runnable command; anything else would
		// be a command that fails. The same rule the styled output applies.
		if row.cliLive {
			hit.Resume = "claude --resume " + shQuote(row.id)
			if hit.Cwd != "" {
				hit.Resume = "cd " + shQuote(hit.Cwd) + " && " + hit.Resume
			}
		}
		doc.Sessions = append(doc.Sessions, hit)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
