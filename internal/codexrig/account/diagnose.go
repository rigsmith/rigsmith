package account

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
)

// Observation is a snapshot of the machine's Codex identity and codexrig's view
// of it.
//
// It is much thinner than clauderig's equivalent, and the reason is worth
// recording so nobody "restores" the missing half. Claude Code stores identity
// in two places that can disagree, and clauderig's Observation exists to catch
// that. Codex stores it once, inside the credential, so the only things that can
// be wrong here are: the credential is missing, it cannot be read, it cannot
// authenticate, or codexrig's pointer names a different account than the
// credential does. Those are the four problems below, and there is no fifth.
type Observation struct {
	At string `json:"at"`

	LiveEmail     string `json:"liveEmail,omitempty"`
	LiveAccountID string `json:"liveAccountId,omitempty"`
	LivePlan      string `json:"livePlan,omitempty"`
	LiveAuthMode  string `json:"liveAuthMode,omitempty"`
	LiveErr       string `json:"liveErr,omitempty"`
	LoggedOut     bool   `json:"loggedOut,omitempty"`
	NoTokens      bool   `json:"noTokens,omitempty"`

	// PointerID is the account codexrig recorded as live; PointerEmail is set
	// only when it disagrees with the credential, because agreeing is the
	// normal case and printing it every time buries the one that matters.
	PointerID    string `json:"pointerId,omitempty"`
	PointerEmail string `json:"pointerEmail,omitempty"`
	Untracked    bool   `json:"untracked,omitempty"`

	// EnvHome is set when CODEX_HOME points somewhere other than the machine's
	// own home. Not a fault — it is exactly what `account run` does — but it
	// changes what every other line here means.
	EnvHome string `json:"envHome,omitempty"`

	InSync bool `json:"inSync"`

	Accounts []StoredStatus `json:"accounts,omitempty"`
}

// Diagnose reads both halves of the picture and never fails: an unreadable
// credential is recorded as a finding rather than returned as an error, because
// a doctor that cannot run is worse than one that reports a problem.
func (s *Store) Diagnose() Observation {
	o := Observation{At: time.Now().UTC().Format(time.RFC3339Nano)}

	machineHome, err := codexhome.Default()
	if err != nil {
		o.LiveErr = err.Error()
		return o
	}
	if env, set := codexhome.Env(); set && !codexhome.SameDir(env, machineHome) {
		o.EnvHome = env
	}

	live, err := ReadLive()
	switch {
	case errors.Is(err, ErrNoLive):
		o.LoggedOut = true
	case err != nil:
		o.LiveErr = err.Error()
	default:
		id := IdentityOf(live)
		o.LiveEmail, o.LiveAccountID = id.Email, id.AccountID
		o.LivePlan, o.LiveAuthMode = id.PlanType, id.AuthMode
		o.NoTokens = !HasTokens(live)
		if got, ok := s.matchLive(live); ok {
			o.PointerID = got
		} else {
			o.Untracked = true
		}
	}

	ptr, _ := s.Active()
	if ptr != "" {
		if o.PointerID == "" {
			o.PointerID = ptr
		} else if ptr != o.PointerID {
			// The recorded pointer names an account the credential does not.
			if a, err := s.load(ptr); err == nil {
				o.PointerEmail = a.Title()
			} else {
				o.PointerEmail = ptr
			}
		}
	}

	o.Accounts, _ = s.StoredStatuses()
	o.InSync = len(o.Problems()) == 0
	return o
}

// Problems lists what is wrong, most serious first. An empty result means the
// halves agree; an ABSENT value is never treated as evidence of a conflict.
func (o Observation) Problems() []string {
	var out []string
	if o.LiveErr != "" {
		out = append(out, "the Codex credential could not be read: "+o.LiveErr)
	}
	if o.NoTokens {
		out = append(out, "~/.codex/auth.json holds no usable token — Codex is logged out in all but name; run `codex login`")
	}
	if o.PointerEmail != "" {
		out = append(out, fmt.Sprintf("codexrig's active account (%s) is not the login the credential authenticates as (%s)", o.PointerEmail, orNone(o.LiveEmail)))
	}
	if o.Untracked && !o.LoggedOut && o.LiveErr == "" {
		out = append(out, fmt.Sprintf("the live login (%s) is not tracked — run `codexrig account add` to keep it", orNone(o.LiveEmail)))
	}
	return out
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}
