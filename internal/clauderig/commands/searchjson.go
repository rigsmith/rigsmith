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
