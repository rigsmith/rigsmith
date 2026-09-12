// Package peek reads sessions straight out of the sync repo's git object store,
// without restoring anything onto this machine.
//
// The case it is for: another computer synced a conversation you want to look
// at, and you do not want its whole setup written over yours to see it. A fetch
// has already brought the objects down; reading them is one git command, and
// restoring is the heavy answer to a light question.
//
// Read-only except for Get, which is strictly additive and refuses to overwrite.
package peek

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/internal/codexrig/rollout"
)

// DefaultRef is what a peek reads: the remote's tip, not this machine's, because
// the point is to see what another machine put there.
const DefaultRef = "origin/main"

// Session is one rollout in the repo.
type Session struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// Machine is taken from the commit subject that last touched the file —
	// "codexrig sync: <machine>". Empty when the subject does not say, which is
	// better than guessing.
	Machine  string    `json:"machine,omitempty"`
	SyncedAt time.Time `json:"syncedAt,omitempty"`
	Title    string    `json:"title,omitempty"`
	Cwd      string    `json:"cwd,omitempty"`
}

// commitMarker and fieldSep delimit the log format. Record separators rather
// than anything printable, because a commit subject can contain any character a
// person can type.
const (
	commitMarker = "\x1e"
	fieldSep     = "\x1f"
	logFormat    = commitMarker + "%s" + fieldSep + "%cI"
)

// List reports every session in the repo at ref.
//
// It walks the log ONCE and cross-checks against the tree, because a log walk
// alone reports paths that were later pruned by retention — files that are
// remembered but no longer there. Those belong to the ledger, not here: this
// command's promise is that what it lists can be read.
func List(ctx context.Context, repo *gitrepo.Repo, ref string) ([]Session, error) {
	if ref == "" {
		ref = DefaultRef
	}
	present, err := repo.TreePaths(ctx, ref, "")
	if err != nil {
		return nil, err
	}
	alive := make(map[string]bool, len(present))
	for _, p := range present {
		if isSessionPath(p) {
			alive[p] = true
		}
	}
	if len(alive) == 0 {
		return nil, nil
	}

	out, err := repo.LogNameOnly(ctx, ref, logFormat, "")
	if err != nil {
		return nil, err
	}

	var sessions []Session
	seenPath := map[string]bool{}
	seenID := map[string]bool{}
	var machine string
	var when time.Time

	for _, block := range strings.Split(out, commitMarker) {
		if strings.TrimSpace(block) == "" {
			continue
		}
		header, body, _ := strings.Cut(block, "\n")
		subject, stamp, _ := strings.Cut(header, fieldSep)
		machine = machineFrom(subject)
		when, _ = time.Parse(time.RFC3339, strings.TrimSpace(stamp))

		for _, p := range strings.Split(body, "\n") {
			p = strings.TrimSpace(p)
			if p == "" || !alive[p] || seenPath[p] {
				continue
			}
			id := rollout.IDFromRolloutRel(path.Base(p))
			if id == "" || seenID[id] {
				// The same session can appear at more than one path across
				// machines. One row each; the newest commit that touched it
				// wins, and the log is newest-first.
				seenPath[p] = true
				continue
			}
			seenPath[p], seenID[id] = true, true
			sessions = append(sessions, Session{ID: id, Path: p, Machine: machine, SyncedAt: when})
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if !sessions[i].SyncedAt.Equal(sessions[j].SyncedAt) {
			return sessions[i].SyncedAt.After(sessions[j].SyncedAt)
		}
		return sessions[i].Path < sessions[j].Path
	})
	return sessions, nil
}

// isSessionPath reports whether a repo-relative path names a rollout. The first
// segment is the sync root's id, which is not part of a rollout's own path.
func isSessionPath(p string) bool {
	_, rest, ok := strings.Cut(p, "/")
	return ok && rollout.IsRolloutRel(rest)
}

// machineFrom reads the machine out of a commit subject. Anything that is not
// one of our own sync commits yields "" rather than a guess.
func machineFrom(subject string) string {
	const prefix = "codexrig sync: "
	s := strings.TrimSpace(subject)
	if !strings.HasPrefix(s, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(s, prefix))
}

// headBytes bounds the prefix read for a title. A rollout's header is its first
// record, and a listing that pulled whole conversations out of git to print one
// line each would be unusable.
const headBytes = 1 << 20

// Titles fills in what each session was about, reading a bounded prefix of each.
func Titles(ctx context.Context, repo *gitrepo.Repo, ref string, sessions []Session) []Session {
	if ref == "" {
		ref = DefaultRef
	}
	for i := range sessions {
		head, err := repo.ShowPrefix(ctx, ref, sessions[i].Path, headBytes)
		if err != nil {
			continue
		}
		sessions[i].Title = rollout.FirstPromptFrom(bytes.NewReader(head))
		sessions[i].Cwd = cwdFrom(head)
	}
	return sessions
}

// cwdFrom reads the working directory out of a rollout's header record.
func cwdFrom(head []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(head))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var env rollout.Envelope
		if err := jsonUnmarshal(sc.Bytes(), &env); err != nil || env.Type != rollout.TypeSessionMeta {
			continue
		}
		var p struct {
			Cwd string `json:"cwd"`
		}
		if jsonUnmarshal(env.Payload, &p) == nil {
			return p.Cwd
		}
	}
	return ""
}

// Machines lists the machines that contributed, for a filter.
func Machines(sessions []Session) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sessions {
		if s.Machine != "" && !seen[s.Machine] {
			seen[s.Machine] = true
			out = append(out, s.Machine)
		}
	}
	sort.Strings(out)
	return out
}

// FilterMachine keeps one machine's sessions.
func FilterMachine(sessions []Session, machine string) []Session {
	var out []Session
	for _, s := range sessions {
		if strings.EqualFold(s.Machine, machine) {
			out = append(out, s)
		}
	}
	return out
}

// ErrAmbiguous means a prefix matched more than one session.
var ErrAmbiguous = errors.New("more than one session matches")

// Find resolves an id or a unique prefix of one.
func Find(sessions []Session, idOrPrefix string) (Session, error) {
	needle := strings.ToLower(strings.TrimSpace(idOrPrefix))
	if needle == "" {
		return Session{}, errors.New("give a session id")
	}
	var hits []Session
	for _, s := range sessions {
		if s.ID == needle {
			return s, nil
		}
		if strings.HasPrefix(s.ID, needle) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return Session{}, fmt.Errorf("no session here matches %q", idOrPrefix)
	case 1:
		return hits[0], nil
	default:
		return Session{}, fmt.Errorf("%w: %q matches %d", ErrAmbiguous, idOrPrefix, len(hits))
	}
}

// Read returns a session's bytes from the object store.
func Read(ctx context.Context, repo *gitrepo.Repo, ref string, s Session) ([]byte, error) {
	if ref == "" {
		ref = DefaultRef
	}
	return repo.ShowFile(ctx, ref, s.Path)
}

// ErrExists means this machine already has that session.
var ErrExists = errors.New("that session is already on this machine")

// Got is what Get wrote.
type Got struct {
	Path  string
	Bytes int
}

// Get writes a session into this machine's Codex home.
//
// Strictly additive: it refuses rather than overwrite. A rollout already here is
// at least as complete — it may be the file a live session is writing into — and
// replacing it with a snapshot from the repo would be a silent truncation.
//
// The relative path is kept as it was. Codex shards by date, not by a slug
// derived from a machine's paths, so a rollout means the same thing in the same
// place on every machine — there is nothing to translate.
func Get(ctx context.Context, repo *gitrepo.Repo, ref string, s Session, codexHome string) (Got, error) {
	body, err := Read(ctx, repo, ref, s)
	if err != nil {
		return Got{}, err
	}
	_, rel, _ := strings.Cut(s.Path, "/")
	dst := filepath.Join(codexHome, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return Got{}, err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return Got{}, fmt.Errorf("%w: %s", ErrExists, dst)
		}
		return Got{}, err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		// A half-written rollout is worse than none: Codex would try to resume
		// it and find a truncated conversation.
		_ = os.Remove(dst)
		return Got{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dst)
		return Got{}, err
	}
	return Got{Path: dst, Bytes: len(body)}, nil
}
