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
	// Account is the accountUuid directory the sidecar is filed under, which is
	// the login that owns the session. Ground truth in a way Profile is not: a
	// sidecar copied into another profile's tree keeps this path.
	Account string
	// Profile is the Desktop install whose session list holds this session, empty
	// for the machine-wide one. The only thing that says which Desktop to reopen
	// a session in — its transcript lands in the shared tree either way.
	Profile string
	// Sidecars are every sidecar file found for this session, with the store
	// each was found in. A session filed by two Desktop installs has two, and a
	// caller deleting it has to remove all of them — the display fields below
	// come from whichever was fresher, which says nothing about where the files
	// actually are.
	Sidecars []SidecarRef
}

// SidecarRef is one Desktop sidecar file and which store holds it.
type SidecarRef struct {
	Label string // the Root label it was scanned under: "desktop" or "repo"
	Path  string
}

// Index maps cliSessionId → Meta.
type Index map[string]Meta

// Root is a place to scan for sidecars: a Label (for provenance) and the Base dir
// that CONTAINS a claude-code-sessions/ tree — the live Desktop dir, or
// <staging-repo>/desktop.
//
// Profile is the clauderig-managed Desktop profile this root belongs to, empty
// for the machine-wide install. Kept apart from Label, which answers a different
// question: which store the sidecar came from, live or synced.
type Root struct {
	Label   string
	Base    string
	Profile string
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

// CanonicalID is the form an Index is keyed by, and the form every lookup must
// use. Session ids are uuids, which are case-insensitive by specification —
// Claude Code writes its transcript filenames lowercase, but a Desktop sidecar
// carries whatever `cliSessionId` it was given. Keyed by the raw value, an
// uppercase sidecar simply never matched the transcript beside it, so the
// session silently lost its title and project everywhere the index is consulted.
func CanonicalID(id string) string { return strings.ToLower(id) }

// Build scans each root's sidecar trees and returns the merged index keyed by
// cliSessionId. When a session has sidecars in more than one root, the entry with
// the newer LastActivity supplies the display fields and every source label is
// recorded. Sidecars with no cliSessionId (e.g. storage placeholders) are
// ignored. Missing/unreadable trees are skipped, not errors.
func Build(roots []Root) Index {
	idx := Index{}
	for _, r := range roots {
		for _, tree := range sessionTrees {
			scanSidecars(idx, filepath.Join(r.Base, tree), r.Label, r.Profile)
		}
	}
	return idx
}

// accountOf reads the accountUuid out of a sidecar's own path,
// <tree>/<accountUuid>/<organizationUuid>/local_<id>.json. Anything shallower is
// a layout we do not understand, where guessing beats nothing.
func accountOf(tree, path string) string {
	rel, err := filepath.Rel(tree, path)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[0]
}

// scanSidecars folds every local_<id>.json under dir into idx (see Build for the
// merge rule). It does not descend into a session's own working directory (the
// local_<id>/ dir a cowork session keeps beside its sidecar) — that subtree holds
// the session's outputs and a nested .claude/, which can be large and carries no
// sidecars. The sidecar is the sibling local_<id>.json file, read separately.
func scanSidecars(idx Index, dir, label, profile string) {
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
			ID: CanonicalID(sc.CliSessionID), Title: sc.Title, Cwd: sc.Cwd,
			Model: sc.Model, Archived: sc.IsArchived, Profile: profile,
			Account: accountOf(dir, p),
		}
		if sc.LastActivityAt > 0 {
			m.LastActivity = time.UnixMilli(sc.LastActivityAt).UTC()
		}
		m.Sidecars = []SidecarRef{{Label: label, Path: p}}
		if prev, ok := idx[m.ID]; ok {
			sources := appendUnique(prev.Sources, label)
			// Unioned for the same reason as Sources, and kept out of the
			// fresher-wins swap below: every copy's path has to survive, or a
			// delete silently leaves one behind.
			sidecars := appendSidecar(prev.Sidecars, SidecarRef{Label: label, Path: p})
			// Ownership is settled BEFORE the fresher-sidecar swap. Only one copy
			// can name a profile, and reading it off the survivor loses it whenever
			// the survivor is the machine-wide one — the common case, since that
			// install is usually the most recently touched.
			profile, acct := m.Profile, m.Account
			if profile == "" {
				profile = prev.Profile
			}
			switch {
			case acct == "":
				acct = prev.Account
			case prev.Account != "" && prev.Account != acct:
				// Two copies filing the same session under different accounts.
				// Whichever was read last would otherwise decide, and this is meant
				// to be ground truth — an arbitrary answer is worse than none, since
				// it drives which Desktop the user is sent to. Leave it unassigned,
				// as engine's sidecar scan does for the same conflict.
				acct, profile = "", ""
			}
			// Keep the fresher sidecar's display fields; union the sources.
			if prev.LastActivity.After(m.LastActivity) {
				m = prev
			}
			m.Sources = sources
			m.Sidecars = sidecars
			m.Profile, m.Account = profile, acct
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

// The fallback-title scan is bounded two ways, because the two things worth
// bounding are different.
//
// maxScanLines caps the work: a transcript body runs to megabytes and must
// never be parsed whole just to find a title.
//
// maxUserRecords is what actually decides when to give up, and it counts
// *candidates* — user records carrying real text. A line budget alone was too
// blunt: Claude Code's header carries queue-operations, IDE-state records,
// attachments and file-history snapshots, and a thick preamble burns 60 lines
// before the human says anything. Counting every "user" record was still wrong,
// because tool results are recorded as user records too and would spend the
// budget on plumbing. On real data these two changes took untitled sessions
// from 46 to 20 out of 410 — and the 20 that remain genuinely contain no typed
// human message at all.
const (
	maxScanLines   = 4000
	maxUserRecords = 25
)

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
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20) // cap guards a pathological line
	seenUser := 0
	for line := 0; line < maxScanLines && seenUser < maxUserRecords && sc.Scan(); line++ {
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "user" {
			continue
		}
		text, hasText := PromptCandidate(rec.Message.Content)
		// Only a record carrying real text is a candidate. Tool results are
		// also recorded as "user" records but hold tool_result blocks, so
		// textOf yields nothing for them — counting those against the budget
		// spent it on plumbing before reaching anything a person typed.
		if hasText {
			seenUser++
		}
		if !hasText || !IsHumanPrompt(text) {
			continue
		}
		return TidyPrompt(text)
	}
	return ""
}

// PromptCandidate pulls the trimmed text out of a user record's content and
// reports whether there was any. False means the record carries no text at all
// — a tool result, an attachment — as opposed to text that turned out to be
// machine-written, which is [IsHumanPrompt]'s question.
func PromptCandidate(raw json.RawMessage) (text string, hasText bool) {
	text = strings.TrimSpace(textOf(raw))
	return text, text != ""
}

// IsHumanPrompt reports whether text a "user" record carries was actually typed
// by a person. Claude Code files several kinds of injected message under the
// same record type: IDE state and DOM probes (angle-bracket tags), the caveat
// prepended to a resumed session, and the marker left when a turn is
// interrupted. None of them is something the user said, and any of them read as
// a session's title or its last words would be actively misleading.
//
// Shared by [FirstPromptFrom] and the tail scan behind [Activity.LastPrompt] so
// the first and last prompt of a session can never disagree about what counts.
func IsHumanPrompt(text string) bool {
	if text == "" {
		return false
	}
	return !strings.HasPrefix(text, "<") && !strings.HasPrefix(text, "Caveat:") &&
		!strings.Contains(text, "DOM Probe") && !strings.HasPrefix(text, "[Request interrupted")
}

// TidyPrompt flattens a prompt to one line and bounds its length, so a
// multi-line paste renders as a title rather than reflowing the whole list.
func TidyPrompt(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	// Truncate by runes, not bytes — a byte cut can split a multibyte character
	// and yield an invalid-UTF-8 title.
	if utf8.RuneCountInString(text) > 70 {
		text = string([]rune(text)[:70]) + "…"
	}
	return text
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

// appendSidecar adds a sidecar reference unless that exact file is already
// recorded — the same tree can be scanned under more than one root.
func appendSidecar(xs []SidecarRef, x SidecarRef) []SidecarRef {
	for _, e := range xs {
		if e.Path == x.Path {
			return xs
		}
	}
	return append(append([]SidecarRef(nil), xs...), x)
}

func appendUnique(xs []string, x string) []string {
	for _, e := range xs {
		if e == x {
			return xs
		}
	}
	return append(xs, x)
}
