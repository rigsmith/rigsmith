package bridge

import (
	"context"

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
