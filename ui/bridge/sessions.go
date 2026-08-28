package bridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/core/gitrepo"
	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/peek"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// DefaultSessionLimit bounds a listing. Titles cost a blob read each, and a real
// repo holds hundreds of sessions.
const DefaultSessionLimit = 40

// RemoteSession is one session on the remote, as the browser lists it.
type RemoteSession struct {
	ID       string    `json:"id"`
	Short    string    `json:"short"`
	Title    string    `json:"title"`
	Machine  string    `json:"machine"`
	Slug     string    `json:"slug"`
	SyncedAt time.Time `json:"syncedAt"`
	// Local is true when a transcript with this id already exists on this
	// machine — the browser greys out "Bring to this Mac" rather than offering
	// an action that would refuse.
	Local bool `json:"local"`
}

// Turn is one message in a rendered transcript.
type Turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// Transcript is a session rendered for reading.
type Transcript struct {
	Session RemoteSession `json:"session"`
	Turns   []Turn        `json:"turns"`
	// Truncated reports that the transcript was longer than maxTurns. A viewer
	// that silently drops the end of a conversation is worse than one that says
	// it did.
	Truncated bool `json:"truncated"`
}

// maxTurns bounds what the viewer renders. Long sessions run to thousands of
// messages, and the window is a webview — handing it all of them stalls it.
const maxTurns = 400

// Sessions browses the remote's history without merging anything.
type Sessions struct{}

// NewSessions builds the sessions service.
func NewSessions() *Sessions { return &Sessions{} }

// Machines lists the machines that have synced sessions into the remote.
func (s *Sessions) Machines(ctx context.Context) ([]string, error) {
	_, sessions, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	return peek.Machines(sessions), nil
}

// List returns the newest sessions on the remote, optionally filtered to one
// machine. Titles are read for the returned window only.
func (s *Sessions) List(ctx context.Context, machine string, limit int) ([]RemoteSession, error) {
	if limit <= 0 {
		limit = DefaultSessionLimit
	}
	repo, sessions, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	sessions = peek.FilterMachine(sessions, machine)
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	sessions = peek.Titles(ctx, repo, peek.DefaultRef, sessions)

	local := localSessionIDs()
	out := make([]RemoteSession, 0, len(sessions))
	for _, ps := range sessions {
		out = append(out, toRemote(ps, local))
	}
	return out, nil
}

// Read renders one session for the viewer. Read-only: nothing is written and no
// merge happens, which is the whole point — during the divergence the other
// machine's chat was readable the entire time.
func (s *Sessions) Read(ctx context.Context, id string) (Transcript, error) {
	repo, sessions, err := s.load(ctx)
	if err != nil {
		return Transcript{}, err
	}
	found, err := peek.Find(sessions, id)
	if err != nil {
		return Transcript{}, err
	}
	blob, err := peek.Read(ctx, repo, peek.DefaultRef, found)
	if err != nil {
		return Transcript{}, err
	}

	t := Transcript{Session: toRemote(found, localSessionIDs())}
	t.Session.Title = session.FirstPromptFrom(strings.NewReader(string(blob)))
	for _, line := range strings.Split(string(blob), "\n") {
		if !session.IsConversationLine(line) {
			continue
		}
		role, text, ok := session.MessageText(line)
		if !ok {
			continue
		}
		if len(t.Turns) >= maxTurns {
			t.Truncated = true
			break
		}
		t.Turns = append(t.Turns, Turn{Role: role, Text: text})
	}
	return t, nil
}

// load opens the staging repo and lists what the remote holds.
func (s *Sessions) load(ctx context.Context) (*gitrepo.Repo, []peek.Session, error) {
	staging, err := config.StagingDir()
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(filepath.Join(staging, ".git")); err != nil {
		return nil, nil, fmt.Errorf("no staging repo yet — sync once first")
	}
	repo, err := gitrepo.Open(ctx, staging)
	if err != nil {
		return nil, nil, err
	}
	sessions, err := peek.List(ctx, repo, peek.DefaultRef)
	if err != nil {
		return nil, nil, fmt.Errorf("%w (nothing fetched yet? run Pull)", err)
	}
	return repo, sessions, nil
}

func toRemote(p peek.Session, local map[string]bool) RemoteSession {
	return RemoteSession{
		ID: p.ID, Short: shortSessionID(p.ID), Title: p.Title,
		Machine: p.Machine, Slug: p.Slug, SyncedAt: p.SyncedAt,
		Local: local[p.ID],
	}
}

func shortSessionID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return id
}

// localSessionIDs is the set of session ids already on this machine, so the
// browser can say which ones there is nothing to bring over. Best-effort: an
// unreadable projects dir just means nothing is marked local.
func localSessionIDs() map[string]bool {
	out := map[string]bool{}
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return out
	}
	me := config.DetectFor(cfg)
	cliLoc, st := cfg.RootLocation("cli", me)
	if st != pathmap.StatusResolved || cliLoc == "" {
		return out
	}
	projects := filepath.Join(cliLoc, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projects, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if name := f.Name(); strings.HasSuffix(name, ".jsonl") {
				out[strings.TrimSuffix(name, ".jsonl")] = true
			}
		}
	}
	return out
}
