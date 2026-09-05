package sessions

import (
	"bufio"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// promptMax bounds one prompt's rendered length. Longer than the 70 runes a
// title gets — a detail view has room, and the point of showing the opening and
// closing prompts is to recognise the conversation, which a seven-word stub
// often cannot do. Still bounded: people paste stack traces and whole files.
const promptMax = 400

// Prompt is one thing a person typed, with when they typed it.
type Prompt struct {
	Text string
	At   time.Time
}

// Conversation is the shape of a session: how it opened, how it ended, and how
// many turns a person actually took.
type Conversation struct {
	First []Prompt
	Last  []Prompt
	// Total is every human prompt in the transcript, so a caller can say how
	// much sits between the two ends rather than implying First+Last is all of it.
	Total int
}

// Prompts reads a transcript and returns its first n and last n human prompts.
//
// Only what a person typed: tool results, IDE-state injections, the
// resumed-session caveat and interrupt markers are all filed as "user" records
// and all excluded, through the same predicate that decides a session's title.
//
// When a session has n or fewer prompts, Last is empty rather than repeating
// First — the two ends of a short conversation are the same thing, and showing
// it twice reads as a bug.
func Prompts(path string, n int) (Conversation, error) {
	var c Conversation
	if path == "" || n <= 0 {
		return c, nil
	}
	f, err := transcript.Open(path)
	if err != nil {
		return c, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20) // cap guards a pathological line
	// tail is a ring of the last n, so a session with thousands of turns costs
	// the same memory as one with ten.
	tail := make([]Prompt, 0, n)
	for sc.Scan() {
		var rec struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "user" {
			continue
		}
		text, hasText := session.PromptCandidate(rec.Message.Content)
		if !hasText || !session.IsHumanPrompt(text) {
			continue
		}
		p := Prompt{Text: trimPrompt(text)}
		if t, terr := time.Parse(time.RFC3339, rec.Timestamp); terr == nil {
			p.At = t.UTC()
		}
		c.Total++
		if len(c.First) < n {
			c.First = append(c.First, p)
			continue
		}
		if len(tail) == n {
			tail = tail[1:]
		}
		tail = append(tail, p)
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	c.Last = tail
	return c, nil
}

// trimPrompt collapses a prompt to something a list can hold, bounding by runes
// so a cut cannot split a multi-byte character.
func trimPrompt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > promptMax {
		s = string([]rune(s)[:promptMax]) + "…"
	}
	return s
}

// Excerpt is one place a search term appears in a transcript, with enough of
// the line around it to be read.
type Excerpt struct {
	Line int    `json:"line"`
	Text string `json:"text"`
	// At and Len locate the term inside Text, so a caller can mark it without
	// searching again — and without guessing at case, which a second search
	// would get wrong for a case-insensitive match.
	At  int `json:"at"`
	Len int `json:"len"`
}

// Excerpts returns up to max places query appears in a transcript.
//
// The list already says a session matched and how often; this is the follow-up
// question, which is what it actually said. Bounded because a term can appear
// hundreds of times in a long conversation and nobody reads the hundredth.
func Excerpts(path, query string, caseSensitive bool, max int) []Excerpt {
	if path == "" || strings.TrimSpace(query) == "" || max <= 0 {
		return nil
	}
	var out []Excerpt
	_, err := search.ScanFile(path, search.Options{
		Query: query, CaseSensitive: caseSensitive, Accept: session.IsConversationLine,
	}, func(m search.Match) {
		if len(out) >= max {
			return
		}
		out = append(out, Excerpt{Line: m.Line, Text: m.Snippet, At: m.MatchAt, Len: m.MatchLen})
	})
	if err != nil {
		return nil
	}
	return out
}
