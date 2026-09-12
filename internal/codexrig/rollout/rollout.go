// Package rollout reads Codex's session files — what Codex calls a rollout, and
// what clauderig would call a transcript.
//
// The format is JSONL, but it is NOT Claude Code's JSONL with different field
// names, and treating it as such is the mistake this package exists to avoid.
// Claude writes one record per message with a top-level `type` of "user" or
// "assistant". Codex writes an envelope — {timestamp, ordinal, type, payload} —
// whose `type` names a STREAM (session_meta, turn_context, response_item,
// event_msg, token_usage_record, world_state, compacted) and whose payload has a
// type of its own. The role lives two levels down, and the same message can
// appear both as a response_item and as an event_msg.
//
// Everything here is bounded. A rollout on the machine this was written against
// runs to 49,294 lines and 215 MB sits in the tree; a reader that loads one to
// answer "what was this about" is a reader that cannot be used in a listing.
// The header is read from the front, the last activity from the tail, and
// neither reads the middle.
package rollout

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

// Envelope is the outer record every line carries.
type Envelope struct {
	Timestamp string          `json:"timestamp"`
	Ordinal   int64           `json:"ordinal"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// The stream names a line's Type can take.
const (
	TypeSessionMeta  = "session_meta"
	TypeTurnContext  = "turn_context"
	TypeResponseItem = "response_item"
	TypeEventMsg     = "event_msg"
	TypeTokenUsage   = "token_usage_record"
	TypeWorldState   = "world_state"
	TypeCompacted    = "compacted"
)

// Meta is a rollout's header: everything the first record says about the session.
type Meta struct {
	SessionID     string    `json:"sessionId"`
	At            time.Time `json:"at"`
	Cwd           string    `json:"cwd,omitempty"`
	Originator    string    `json:"originator,omitempty"`
	CLIVersion    string    `json:"cliVersion,omitempty"`
	Source        string    `json:"source,omitempty"`
	ModelProvider string    `json:"modelProvider,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	CommitHash    string    `json:"commitHash,omitempty"`
	RepositoryURL string    `json:"repositoryUrl,omitempty"`
}

type metaPayload struct {
	SessionID     string `json:"session_id"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	Originator    string `json:"originator"`
	CLIVersion    string `json:"cli_version"`
	Source        string `json:"source"`
	ModelProvider string `json:"model_provider"`
	Git           *struct {
		CommitHash    string `json:"commit_hash"`
		Branch        string `json:"branch"`
		RepositoryURL string `json:"repository_url"`
	} `json:"git"`
}

// maxHeaderLines bounds the search for the header. session_meta is the FIRST
// record by construction, so one line is normally enough; the budget exists for
// a file whose head was truncated or migrated, not as an expectation.
const maxHeaderLines = 200

// maxLineBytes caps a single line. One rollout with a huge newline-free line
// would otherwise set the process's memory ceiling, and a listing reads every
// rollout it can see.
const maxLineBytes = 4 << 20

// ReadMeta reads a rollout's header without reading its body.
func ReadMeta(path string) (Meta, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, false, err
	}
	defer f.Close()
	sc := newScanner(f)
	for n := 0; n < maxHeaderLines && sc.Scan(); n++ {
		var env Envelope
		if json.Unmarshal(sc.Bytes(), &env) != nil || env.Type != TypeSessionMeta {
			continue
		}
		var p metaPayload
		if json.Unmarshal(env.Payload, &p) != nil {
			continue
		}
		m := Meta{
			SessionID:     firstNonEmpty(p.SessionID, p.ID),
			Cwd:           p.Cwd,
			Originator:    p.Originator,
			CLIVersion:    p.CLIVersion,
			Source:        p.Source,
			ModelProvider: p.ModelProvider,
			At:            parseTime(firstNonEmpty(p.Timestamp, env.Timestamp)),
		}
		if p.Git != nil {
			m.Branch, m.CommitHash, m.RepositoryURL = p.Git.Branch, p.Git.CommitHash, p.Git.RepositoryURL
		}
		if m.Branch == "HEAD" {
			// Detached: a branch name nobody typed, so reporting it as one is
			// worse than reporting none.
			m.Branch = ""
		}
		return m, true, nil
	}
	return Meta{}, false, sc.Err()
}

// Activity is what the end of a rollout says: when it last moved, and the most
// recent things worth showing in a listing.
type Activity struct {
	At         time.Time
	Model      string
	LastPrompt string
	LastReply  string
}

// tailBytes is how much of the end of a rollout LastActivity reads, doubling to
// maxTailBytes when the window held no usable record.
const (
	tailBytes    = 128 << 10
	maxTailBytes = 2 << 20
)

// LastActivity reads the tail of a rollout for its most recent timestamp and the
// last thing said in either direction.
func LastActivity(p string) (Activity, bool) {
	f, err := os.Open(p)
	if err != nil {
		return Activity{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Activity{}, false
	}
	for window := int64(tailBytes); ; window *= 2 {
		if window > maxTailBytes {
			window = maxTailBytes
		}
		off := st.Size() - window
		if off < 0 {
			off = 0
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return Activity{}, false
		}
		r := bufio.NewReaderSize(f, 64<<10)
		if off > 0 {
			// The window almost certainly starts mid-line; drop the partial
			// first record rather than trying to parse it.
			if _, err := r.ReadString('\n'); err != nil {
				return Activity{}, false
			}
		}
		if act, ok := latestIn(r); ok {
			return act, true
		}
		if off == 0 || window >= maxTailBytes {
			return Activity{}, false
		}
	}
}

// latestIn scans a window and keeps the record with the NEWEST timestamp, which
// is not necessarily the last line: Codex interleaves streams, and the final
// lines of a rollout are usually token accounting rather than anything said.
// The last prompt and reply are tracked separately for the same reason — the
// newest record is rarely either one.
func latestIn(r *bufio.Reader) (Activity, bool) {
	var act Activity
	found := false
	sc := newScanner(r)
	for sc.Scan() {
		var env Envelope
		if json.Unmarshal(sc.Bytes(), &env) != nil {
			continue
		}
		if at := parseTime(env.Timestamp); !at.IsZero() && at.After(act.At) {
			act.At = at
			found = true
		}
		switch env.Type {
		case TypeTurnContext:
			var p struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(env.Payload, &p) == nil && p.Model != "" {
				act.Model = p.Model
				found = true
			}
		case TypeResponseItem, TypeEventMsg:
			role, text, ok := textOf(env)
			if !ok || text == "" {
				continue
			}
			switch role {
			case RoleUser:
				act.LastPrompt = text
				found = true
			case RoleAssistant:
				act.LastReply = text
				found = true
			}
		}
	}
	return act, found
}

// The roles a rollout distinguishes. "developer" is Codex's own injected
// context — permissions prose, AGENTS.md — and is deliberately not a prompt
// anybody typed.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleDeveloper = "developer"
)

// textOf pulls the role and prose out of a record, from either stream.
//
// Both streams carry the same message in different shapes, which is the single
// most error-prone thing about this format: a reader that handles only
// response_item misses a session whose text arrived as an event, and one that
// handles only event_msg misses most of the history.
func textOf(env Envelope) (role, text string, ok bool) {
	switch env.Type {
	case TypeResponseItem:
		var p struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(env.Payload, &p) != nil || p.Type != "message" {
			return "", "", false
		}
		var parts []string
		for _, c := range p.Content {
			// Only prose. An image or a tool payload has no text to show, and
			// stringifying its type would put "input_image" in a listing.
			if strings.HasSuffix(c.Type, "_text") && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, c.Text)
			}
		}
		if len(parts) == 0 {
			return "", "", false
		}
		return p.Role, strings.Join(parts, " "), true
	case TypeEventMsg:
		var p struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Text    string `json:"text"`
		}
		if json.Unmarshal(env.Payload, &p) != nil {
			return "", "", false
		}
		switch p.Type {
		case "user_message":
			return RoleUser, firstNonEmpty(p.Message, p.Text), true
		case "agent_message":
			return RoleAssistant, firstNonEmpty(p.Message, p.Text), true
		}
		return "", "", false
	}
	return "", "", false
}

// promptMax is how much of a prompt a listing shows.
const promptMax = 70

// FirstPrompt returns the first thing the person typed, tidied for one line.
//
// It skips the developer role entirely. Codex opens a session by injecting the
// sandbox rules and the project's AGENTS.md as messages, and a title taken from
// those would read the same for every session in a repo.
func FirstPrompt(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	return FirstPromptFrom(f)
}

// FirstPromptFrom is FirstPrompt over an open reader.
func FirstPromptFrom(r io.Reader) string {
	const maxScanLines = 4000
	sc := newScanner(r)
	for n := 0; n < maxScanLines && sc.Scan(); n++ {
		var env Envelope
		if json.Unmarshal(sc.Bytes(), &env) != nil {
			continue
		}
		role, text, ok := textOf(env)
		if !ok || role != RoleUser {
			continue
		}
		if !IsHumanPrompt(text) {
			continue
		}
		return Tidy(text)
	}
	return ""
}

// injectedPrefixes are the openings of text Codex puts in the user's mouth. A
// session whose "first prompt" is the sandbox policy is one whose title tells
// the reader nothing, and every session in a repo would share it.
var injectedPrefixes = []string{
	"<",           // tagged context blocks
	"# AGENTS.md", // the project's instructions, injected as a user turn
	"<permissions instructions>",
	"[Request interrupted",
	"## Environment context",
}

// IsHumanPrompt reports whether text reads as something a person typed.
func IsHumanPrompt(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	for _, p := range injectedPrefixes {
		if strings.HasPrefix(t, p) {
			return false
		}
	}
	return true
}

// Tidy collapses a prompt to one short line.
func Tidy(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	runes := []rune(t)
	if len(runes) > promptMax {
		return string(runes[:promptMax]) + "…"
	}
	return t
}

// IsConversationLine reports whether a line could carry something a search
// should match.
//
// An unparseable line answers TRUE. A search that silently skips what it cannot
// understand is a search that reports "no such conversation" for a session it
// simply failed to read, which is indistinguishable from the chat never having
// existed.
func IsConversationLine(line string) bool {
	var env Envelope
	if json.Unmarshal([]byte(line), &env) != nil {
		return true
	}
	switch env.Type {
	case TypeResponseItem, TypeEventMsg, TypeCompacted:
		return true
	default:
		// Token accounting and world state are bookkeeping: matching a topic
		// word inside them would put every session in the results.
		return false
	}
}

// MessageText pulls one line's role and prose, for rendering a transcript.
func MessageText(line string) (role, text string, ok bool) {
	var env Envelope
	if json.Unmarshal([]byte(line), &env) != nil {
		return "", "", false
	}
	return textOf(env)
}

// Usage is a rollout's token accounting, as of its most recent record.
type Usage struct {
	Input       int64 `json:"input"`
	CachedInput int64 `json:"cachedInput"`
	Output      int64 `json:"output"`
	Reasoning   int64 `json:"reasoning"`
	Total       int64 `json:"total"`
	// ContextWindow is the model's window at the time, which is what makes the
	// totals mean anything.
	ContextWindow int64 `json:"contextWindow,omitempty"`
}

// LastUsage reads the newest token_count event from the tail of a rollout.
func LastUsage(p string) (Usage, bool) {
	f, err := os.Open(p)
	if err != nil {
		return Usage{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Usage{}, false
	}
	off := st.Size() - maxTailBytes
	if off < 0 {
		off = 0
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return Usage{}, false
	}
	r := bufio.NewReaderSize(f, 64<<10)
	if off > 0 {
		if _, err := r.ReadString('\n'); err != nil {
			return Usage{}, false
		}
	}
	var out Usage
	found := false
	sc := newScanner(r)
	for sc.Scan() {
		var env Envelope
		if json.Unmarshal(sc.Bytes(), &env) != nil || env.Type != TypeEventMsg {
			continue
		}
		var p struct {
			Type string `json:"type"`
			Info *struct {
				Total *struct {
					Input     int64 `json:"input_tokens"`
					Cached    int64 `json:"cached_input_tokens"`
					Output    int64 `json:"output_tokens"`
					Reasoning int64 `json:"reasoning_output_tokens"`
					Total     int64 `json:"total_tokens"`
				} `json:"total_token_usage"`
				Window int64 `json:"model_context_window"`
			} `json:"info"`
		}
		if json.Unmarshal(env.Payload, &p) != nil || p.Type != "token_count" || p.Info == nil || p.Info.Total == nil {
			continue
		}
		out = Usage{
			Input: p.Info.Total.Input, CachedInput: p.Info.Total.Cached,
			Output: p.Info.Total.Output, Reasoning: p.Info.Total.Reasoning,
			Total: p.Info.Total.Total, ContextWindow: p.Info.Window,
		}
		found = true
	}
	return out, found
}

// rolloutName matches a rollout's filename and captures its session uuid.
// The stamp between the prefix and the uuid is Codex's, and the uuid is what
// every other surface names a session by.
var rolloutName = regexp.MustCompile(`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-([0-9a-fA-F-]{36})\.jsonl$`)

// datedDir matches the sessions/YYYY/MM/DD shard a rollout lives in.
var datedDir = regexp.MustCompile(`^(sessions|archived_sessions)(/\d{4}/\d{2}/\d{2})?$`)

// IsRolloutRel reports whether a '/'-separated relative path names a session
// rollout: sessions/YYYY/MM/DD/rollout-<stamp>-<uuid>.jsonl, or the flat
// archived_sessions/ form.
//
// The date shard is checked rather than a depth count, because a depth count is
// what lets some other .jsonl at the same depth be mistaken for a session.
func IsRolloutRel(rel string) bool {
	dir, base := path.Split(rel)
	dir = strings.TrimSuffix(dir, "/")
	if !rolloutName.MatchString(base) {
		return false
	}
	return datedDir.MatchString(dir)
}

// IDFromRolloutRel returns the session uuid a rollout's filename carries, or ""
// when the path does not name one. Cheap: no file is opened.
func IDFromRolloutRel(rel string) string {
	m := rolloutName.FindStringSubmatch(path.Base(rel))
	if m == nil {
		return ""
	}
	return CanonicalID(m[1])
}

// CanonicalID is a session id as every index keys it. Codex writes lowercase
// uuids; the app-server and a thread name may not.
func CanonicalID(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// DateOf returns the shard date a rollout path implies, and whether it had one.
//
// The shard is when the session STARTED, and is not a substitute for the record
// timestamps: a session resumed weeks later keeps its original directory. It is
// useful for one thing only, and it is a good one — a time-windowed listing can
// skip whole directories before opening a file.
func DateOf(rel string) (time.Time, bool) {
	parts := strings.Split(rel, "/")
	if len(parts) < 4 {
		return time.Time{}, false
	}
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] != "sessions" {
			continue
		}
		t, err := time.Parse("2006/01/02", strings.Join(parts[i+1:i+4], "/"))
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func newScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	return sc
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
