// Package session turns Claude Code's opaque transcript UUIDs into recognisable
// sessions. Claude Desktop writes one sidecar per session carrying a human
// title, the project cwd, the model, and the cliSessionId that names the
// transcript file — under claude-code-sessions/…/local_<id>.json for Code-tab
// sessions and local-agent-mode-sessions/…/local_<id>.json for cowork/agent
// sessions (same shape, different subtree). This package indexes those sidecars
// by cliSessionId so `search` can label the raw .jsonl files it finds — and
// match on the title too — instead of showing bare UUIDs. It also derives a
// fallback title (the first human prompt) for CLI-only sessions that never got a
// Desktop sidecar.
package session

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Meta is what we know about one session. Title/Cwd/Model/LastActivity come from
// the Desktop sidecar and are empty/zero for a CLI-only session (no sidecar).
type Meta struct {
	ID           string    // cliSessionId — matches the transcript file stem
	Title        string    // human title from the sidecar ("" if none)
	Cwd          string    // sidecar cwd (may be a $HOME/… template from the synced repo)
	Model        string    // e.g. "claude-opus-4-8"
	LastActivity time.Time // sidecar lastActivityAt (zero if unknown)
	Archived     bool
	Sources      []string // sidecar source labels it was found in (e.g. "desktop", "repo")
}

// Index maps cliSessionId → Meta.
type Index map[string]Meta

// Root is a place to scan for sidecars: a Label (for provenance) and the Base dir
// that CONTAINS a claude-code-sessions/ tree — the live Desktop dir, or
// <staging-repo>/desktop.
type Root struct {
	Label string
	Base  string
}

// sidecar is the slice of a claude-code-sessions/*.json we read.
type sidecar struct {
	CliSessionID   string `json:"cliSessionId"`
	Title          string `json:"title"`
	Cwd            string `json:"cwd"`
	Model          string `json:"model"`
	LastActivityAt int64  `json:"lastActivityAt"` // epoch millis
	IsArchived     bool   `json:"isArchived"`
}

// sessionTrees are the Desktop sidecar directories Build scans under each root.
// claude-code-sessions holds Code-tab sessions; local-agent-mode-sessions holds
// cowork/agent sessions — same sidecar shape, a different subtree. Both carry the
// human title we want to surface, so a search hit in either gets a real name.
var sessionTrees = []string{"claude-code-sessions", "local-agent-mode-sessions"}

// Build scans each root's sidecar trees and returns the merged index keyed by
// cliSessionId. When a session has sidecars in more than one root, the entry with
// the newer LastActivity supplies the display fields and every source label is
// recorded. Sidecars with no cliSessionId (e.g. storage placeholders) are
// ignored. Missing/unreadable trees are skipped, not errors.
func Build(roots []Root) Index {
	idx := Index{}
	for _, r := range roots {
		for _, tree := range sessionTrees {
			scanSidecars(idx, filepath.Join(r.Base, tree), r.Label)
		}
	}
	return idx
}

// scanSidecars folds every local_<id>.json under dir into idx (see Build for the
// merge rule). It does not descend into a session's own working directory (the
// local_<id>/ dir a cowork session keeps beside its sidecar) — that subtree holds
// the session's outputs and a nested .claude/, which can be large and carries no
// sidecars. The sidecar is the sibling local_<id>.json file, read separately.
func scanSidecars(idx Index, dir, label string) {
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && strings.HasPrefix(name, "local_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(name, "local_") || !strings.HasSuffix(name, ".json") {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		var sc sidecar
		if json.Unmarshal(b, &sc) != nil || sc.CliSessionID == "" {
			return nil
		}
		m := Meta{
			ID: sc.CliSessionID, Title: sc.Title, Cwd: sc.Cwd,
			Model: sc.Model, Archived: sc.IsArchived,
		}
		if sc.LastActivityAt > 0 {
			m.LastActivity = time.UnixMilli(sc.LastActivityAt).UTC()
		}
		if prev, ok := idx[m.ID]; ok {
			sources := appendUnique(prev.Sources, label)
			// Keep the fresher sidecar's display fields; union the sources.
			if prev.LastActivity.After(m.LastActivity) {
				m = prev
			}
			m.Sources = sources
		} else {
			m.Sources = []string{label}
		}
		idx[m.ID] = m
		return nil
	})
}

// IDFromTranscriptRel extracts the session id from a transcript's root-relative
// (slash) path. It handles the live layout (projects/<slug>/<id>.jsonl and the
// sub-agent form projects/<slug>/<id>/subagents/…) and the synced-repo layout
// (cli/projects/…). Returns "" when the path isn't a transcript under projects/.
func IDFromTranscriptRel(rel string) string {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "projects" && i+2 < len(parts) {
			return strings.TrimSuffix(parts[i+2], ".jsonl")
		}
	}
	return ""
}

// IsConversationLine reports whether a transcript JSONL record is genuine session
// content — a user or assistant message, tool results included — as opposed to
// records the harness injects. Claude Code interleaves the transcript with
// skill-listing attachments, system notes, and mode/queue bookkeeping; a topic
// word often appears only in the injected skill catalog (e.g. a skill name),
// matching sessions that have nothing to do with the topic. Dropping those
// records removes that noise.
//
// Tool results (encoded as user records, tool_use as assistant records) are
// deliberately KEPT: a session's real substance often lives in tool output — a
// spreadsheet dumped by a script, a file a command read — and searching it is a
// primary reason this exists (finding a chat by a value it processed, not just by
// what was typed). The cost is that a match solely in a record's own bookkeeping
// fields (uuid, cwd, gitBranch) still counts; that's a rare, low-harm false
// positive we accept to keep tool content searchable. Unparseable lines are kept
// (never silently hide a hit).
func IsConversationLine(line string) bool {
	var rec struct {
		Type       string          `json:"type"`
		Attachment json.RawMessage `json:"attachment"`
	}
	if json.Unmarshal([]byte(line), &rec) != nil {
		return true
	}
	if len(rec.Attachment) > 0 {
		return false // skill listings, file dumps — injected, not written or read
	}
	return rec.Type == "user" || rec.Type == "assistant"
}

// maxHeaderLines bounds the fallback-title scan: the first human prompt sits at
// the very top of a transcript, so we never read the multi-MB body.
const maxHeaderLines = 60

// FirstPrompt derives a short title from a transcript's first genuine human
// message, for a session with no Desktop sidecar. It skips tool/DOM/system noise
// and returns "" if nothing suitable is found in the header region.
func FirstPrompt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	return FirstPromptFrom(f)
}

// FirstPromptFrom is FirstPrompt over an already-open transcript. `peek` reads
// transcripts out of git blobs rather than the filesystem — they belong to
// another machine and were never written here — so it needs the reader form.
func FirstPromptFrom(r io.Reader) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20) // headers are small; cap guards a pathological line
	for line := 0; line < maxHeaderLines && sc.Scan(); line++ {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "user" {
			continue
		}
		text := strings.TrimSpace(textOf(rec.Message.Content))
		if text == "" || strings.HasPrefix(text, "<") || strings.HasPrefix(text, "Caveat:") ||
			strings.Contains(text, "DOM Probe") || strings.HasPrefix(text, "[Request interrupted") {
			continue
		}
		text = strings.ReplaceAll(text, "\n", " ")
		// Truncate by runes, not bytes — a byte cut can split a multibyte character
		// and yield an invalid-UTF-8 title.
		if utf8.RuneCountInString(text) > 70 {
			text = string([]rune(text)[:70]) + "…"
		}
		return text
	}
	return ""
}

// MessageText decodes one transcript line into its speaker and plain text.
// ok is false for records that carry no readable message — tool plumbing,
// harness bookkeeping, anything unparseable — so a caller rendering a
// conversation can simply skip them.
//
// `peek show` uses this to print a readable conversation instead of raw JSONL.
func MessageText(line string) (role, text string, ok bool) {
	var rec struct {
		Type    string `json:"type"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &rec) != nil {
		return "", "", false
	}
	if rec.Type != "user" && rec.Type != "assistant" {
		return "", "", false
	}
	role = rec.Message.Role
	if role == "" {
		role = rec.Type
	}
	text = strings.TrimSpace(textOf(rec.Message.Content))
	if text == "" {
		return "", "", false
	}
	return role, text, true
}

// textOf pulls the plain text out of a user message's content, which Claude Code
// records either as a bare string or an array of typed blocks.
func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if bl.Type == "text" {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(bl.Text)
			}
		}
		return b.String()
	}
	return ""
}

func appendUnique(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}
