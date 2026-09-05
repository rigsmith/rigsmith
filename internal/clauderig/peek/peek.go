// Package peek reads another machine's sessions straight out of the synced
// repo's object store, without merging anything.
//
// During the 2026-08-07 divergence the Air couldn't see any of the Pro's work
// for a full day — but the Pro's transcripts were sitting in the Air's own
// staging repo the whole time, fetched and unmerged. Reading them needed one
// git command nobody thought to run. That is the entire idea here: a fetch is
// enough to *read* a peer's history, and reading should never require taking on
// the risk of a merge.
//
// Everything below is read-only against a ref (origin/main by default).
// Materialising a session into ~/.claude is the one write, and it is strictly
// additive — it refuses to overwrite a transcript that already exists locally.
package peek

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	transcriptstore "github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// DefaultRef is the remote branch clauderig syncs against.
const DefaultRef = "origin/main"

// transcriptDir is where CLI transcripts live inside the synced repo.
const transcriptDir = "cli/projects"

// Session is one transcript found in the ref.
type Session struct {
	// ID is the session uuid — the transcript's filename stem.
	ID string
	// Path is the repo-relative transcript path.
	Path string
	// Slug is the flattened project directory it lives under.
	Slug string
	// Machine is the machine whose sync commit last touched it, taken from that
	// commit's subject ("clauderig sync: <name>"). The repo has no other record
	// of which machine a session came from — sessions are merged into one tree.
	Machine string
	// SyncedAt is when that commit landed.
	SyncedAt time.Time
	// Title is the first human prompt, filled in only when asked for: it costs a
	// blob read per session, which is not worth paying for a listing of hundreds.
	Title string
}

// List returns the transcripts present in ref, newest sync first.
//
// It attributes and orders every session from a single `git log` walk rather
// than one command per file — a real repo holds hundreds, and per-file
// attribution took long enough to be noticeable.
func List(ctx context.Context, repo *gitrepo.Repo, ref string) ([]Session, error) {
	if ref == "" {
		ref = DefaultRef
	}

	// What actually exists at the ref. The log walk below sees every path any
	// commit ever touched, including transcripts retention has since pruned —
	// listing those offers sessions that cannot be opened, because the blob is
	// gone from the tree even though history remembers it.
	live, err := repo.TreePaths(ctx, ref, transcriptDir)
	if err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(live))
	for _, p := range live {
		present[p] = true
	}

	// Newest-first log of every commit touching the transcript tree, each
	// followed by the files it changed. The first time a path appears is its
	// most recent sync, which is the one that attributes it.
	out, err := repo.LogNameOnly(ctx, ref, LogFormat, transcriptDir)
	if err != nil {
		return nil, err
	}

	var (
		sessions []Session
		seenPath = map[string]bool{}
		// Deduped by session id as well as by path: the same session can appear
		// at more than one path — a project directory renamed, or two machines
		// whose slugs differ for the same cwd — and it is still one session. On
		// real data this produced the same id four times in one listing.
		seenID  = map[string]bool{}
		machine string
		when    time.Time
	)
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, commitMarker); ok {
			machine, when = parseCommitHeader(rest)
			continue
		}
		rel := strings.TrimSpace(line)
		if rel == "" || !strings.HasSuffix(rel, ".jsonl") || seenPath[rel] {
			continue
		}
		seenPath[rel] = true
		if !present[rel] {
			continue // pruned since; history remembers it, the tree does not
		}

		if !isSessionTranscript(rel) {
			continue // sub-agent output or a stray file, not a session
		}
		id := session.IDFromTranscriptRel(rel)
		if id == "" {
			continue
		}
		if seenID[id] {
			continue // already have it from a newer sync
		}
		seenID[id] = true
		sessions = append(sessions, Session{
			ID: id, Path: rel, Slug: path.Base(path.Dir(rel)),
			Machine: machine, SyncedAt: when,
		})
	}

	// Deleted-then-listed paths can't happen (ls-tree isn't consulted), but a
	// stable order matters for output people compare between runs.
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].SyncedAt.After(sessions[j].SyncedAt)
	})
	return sessions, nil
}

// isSessionTranscript reports whether rel is a session's own transcript, as
// opposed to something living underneath one.
//
// A session is exactly projects/<slug>/<id>.jsonl. Claude Code also writes
// projects/<slug>/<id>/subagents/agent-*.jsonl — a session's internal sub-agent
// output, of which this repo holds over a thousand. Those are not sessions:
// listing them adds rows nobody asked for, and because IDFromTranscriptRel maps
// them to their *parent* session id, one could shadow the real transcript and
// make the viewer render an agent's output in place of the conversation.
func isSessionTranscript(rel string) bool {
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "projects" {
			// slug + file, and nothing deeper.
			return len(parts) == i+3 && strings.HasSuffix(parts[i+2], ".jsonl")
		}
	}
	return false
}

// commitMarker prefixes a commit header line in the log output so headers are
// distinguishable from file paths with no parsing ambiguity, and fieldSep splits
// the header's own fields.
//
// ASCII record/unit separators rather than NUL: NUL cannot appear in a process
// argument at all (argv strings are NUL-terminated), so a --format carrying one
// fails to exec. These two are just as impossible in a real commit subject and
// survive the trip.
const (
	commitMarker = "\x1e"
	fieldSep     = "\x1f"
)

// LogFormat is the --format string List expects. Kept next to the parser so the
// two can't drift apart.
const LogFormat = commitMarker + "%s" + fieldSep + "%cI"

func parseCommitHeader(rest string) (machine string, when time.Time) {
	subject, iso, _ := strings.Cut(rest, fieldSep)
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(iso)); err == nil {
		when = t
	}
	return machineFromSubject(subject), when
}

// machineFromSubject pulls the machine name out of a sync commit subject.
// clauderig writes "clauderig sync: <machine>"; anything else (a merge commit,
// a hand-made commit) yields "" rather than a guess.
func machineFromSubject(subject string) string {
	const prefix = "clauderig sync:"
	subject = strings.TrimSpace(subject)
	if !strings.HasPrefix(subject, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(subject, prefix))
}

// Machines returns the distinct machines that have synced sessions into ref,
// most-recently-synced first — the answer to "whose history can I read?".
func Machines(sessions []Session) []string {
	var order []string
	seen := map[string]bool{}
	for _, s := range sessions {
		if s.Machine == "" || seen[s.Machine] {
			continue
		}
		seen[s.Machine] = true
		order = append(order, s.Machine)
	}
	return order
}

// FilterMachine keeps only sessions attributed to machine. An empty machine
// returns everything.
func FilterMachine(sessions []Session, machine string) []Session {
	if machine == "" {
		return sessions
	}
	var out []Session
	for _, s := range sessions {
		if strings.EqualFold(s.Machine, machine) {
			out = append(out, s)
		}
	}
	return out
}

// Find locates a session by id, or by unambiguous id prefix — uuids are
// unpleasant to retype, and a prefix is how people actually refer to them.
func Find(sessions []Session, idOrPrefix string) (Session, error) {
	if idOrPrefix == "" {
		return Session{}, fmt.Errorf("peek: no session id given")
	}
	var matches []Session
	for _, s := range sessions {
		if s.ID == idOrPrefix {
			return s, nil // exact wins outright
		}
		if strings.HasPrefix(s.ID, idOrPrefix) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return Session{}, fmt.Errorf("no session in the remote matching %q", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches[:min(len(matches), 5)] {
			ids = append(ids, m.ID)
		}
		return Session{}, fmt.Errorf("%q matches %d sessions (%s…) — use more of the id",
			idOrPrefix, len(matches), strings.Join(ids, ", "))
	}
}

// Read returns a session's transcript bytes straight from the ref's object
// store. Nothing is written and no merge happens.
func Read(ctx context.Context, repo *gitrepo.Repo, ref string, s Session) ([]byte, error) {
	if ref == "" {
		ref = DefaultRef
	}
	b, err := repo.ShowFile(ctx, ref, s.Path)
	if err != nil {
		return nil, err
	}
	return transcriptstore.ReadStored(s.Path, b, func(p string) ([]byte, error) { return repo.ShowFile(ctx, ref, p) }, 0)
}

// Titles fills in Title for the given sessions by reading each blob's header.
// Callers pass only the slice they're about to display — this costs one blob
// read per session.
func Titles(ctx context.Context, repo *gitrepo.Repo, ref string, sessions []Session) []Session {
	out := make([]Session, len(sessions))
	copy(out, sessions)
	for i := range out {
		blob, err := Read(ctx, repo, ref, out[i])
		if err != nil {
			continue
		}
		out[i].Title = session.FirstPromptFrom(strings.NewReader(string(blob)))
	}
	return out
}
