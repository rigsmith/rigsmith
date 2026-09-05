package bridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
)

// SessionHealth is what is wrong with how sessions are filed, for the window.
//
// The detection is sessions.CheckHealth, the same call `clauderig doctor` and
// the dashboard make. Three implementations of "is this session filed
// correctly" would eventually disagree, and disagreeing about whether a
// conversation is intact is the worst possible thing for them to differ on.
type SessionHealth struct {
	sessions.Health
	Error string `json:"error,omitempty"`
}

// Filing is the read side of the session-filing checks.
type Filing struct{}

// NewFiling builds the filing service.
func NewFiling() *Filing { return &Filing{} }

// Get looks for split sessions and stale Desktop sidecars. Local only — it
// walks the transcript tree and reads sidecars, no network.
func (f *Filing) Get(ctx context.Context) (SessionHealth, error) {
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return SessionHealth{Error: err.Error()}, nil
	}
	me := config.DetectFor(cfg)
	home, _ := cfg.RootLocation("cli", me)
	if home == "" {
		return SessionHealth{}, nil // no ~/.claude here: nothing to be wrong
	}
	return SessionHealth{Health: sessions.CheckHealth(
		[]search.Target{{Label: sessions.CLISource, Dir: home}},
		sessions.Roots(cfg, me, false, false),
	)}, nil
}

// SplitView is one split session with both copies described, for the window.
type SplitView struct {
	sessions.SplitDetail
	// Title is the session's own title where one is known, so the row names a
	// conversation rather than a hex string.
	Title string `json:"title,omitempty"`
}

// Splits describes every split session: what each copy holds, and whether the
// older ones can be set aside without losing anything.
//
// Separate from Get because it reads both transcripts of every split to compare
// them by record id. Get is on the status refresh; this runs when someone asks
// to look.
func (f *Filing) Splits(ctx context.Context) ([]SplitView, error) {
	h, err := f.Get(ctx)
	if err != nil || len(h.Splits) == 0 {
		return nil, err
	}
	// Titled from the session list so a row names a conversation rather than a
	// hex string. Best-effort: an unreadable list costs the names, not the fix.
	titles := map[string]string{}
	if v, lerr := (&Library{}).List(ctx, ListRequest{Since: "3650d", Limit: 5000}); lerr == nil {
		for _, r := range v.Sessions {
			titles[r.ID] = r.Title
		}
	}
	out := make([]SplitView, 0, len(h.Splits))
	for _, s := range h.Splits {
		out = append(out, SplitView{SplitDetail: sessions.Describe(s), Title: titles[s.ID]})
	}
	return out, nil
}

// Consolidate parks every copy of one split session except the newest.
//
// Moved into ~/.clauderig/parked, never deleted, and deliberately NOT inside
// ~/.claude: a file parked there would be picked up by the next sync and handed
// back out by the next restore, which is how the copy came back the first time.
//
// Refuses when the copies have diverged. That check is in the sessions package
// and there is no flag to override it here — "this copy holds three turns the
// other lacks" has no safe automatic answer, and offering one in a window is
// how somebody loses a conversation by clicking.
func (f *Filing) Consolidate(ctx context.Context, id string) (Parked, error) {
	var out Parked
	h, err := f.Get(ctx)
	if err != nil {
		return out, err
	}
	for _, s := range h.Splits {
		if s.ID != id {
			continue
		}
		home, herr := os.UserHomeDir()
		if herr != nil {
			return out, herr
		}
		park := filepath.Join(home, ".clauderig", "parked", time.Now().Format("20060102-150405"))
		files, cerr := sessions.Consolidate(s, park)
		if cerr != nil {
			return out, cerr
		}
		return Parked{Files: files, Dir: park, Keep: s.Keep}, nil
	}
	return out, fmt.Errorf("no split session with id %q — it may already be resolved", id)
}

// Parked is what a consolidate moved, and where.
type Parked struct {
	Dir   string   `json:"dir"`
	Files []string `json:"files"`
	Keep  string   `json:"keep"`
}
