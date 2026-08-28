package bridge

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
)

// Defaults for a listing. The window is a browser over everything this machine
// can see, so it starts wider than `clauderig recent` (24h) — but not unbounded:
// without a lower bound every transcript on the machine is opened to be dated,
// and every surviving one is opened again for its title.
const (
	DefaultLibrarySince = "30d"
	DefaultLibraryLimit = 200
)

// LibrarySession is one session as the manager lists it.
type LibrarySession struct {
	ID    string `json:"id"`
	Short string `json:"short"`
	// When is the session's own date and Approx marks one that did not come
	// from a transcript record. The window renders the age from When rather
	// than being handed a pre-formatted string, so a list left open overnight
	// does not quietly go stale.
	When   time.Time `json:"when"`
	Approx bool      `json:"approx"`
	Cwd    string    `json:"cwd"`
	// Account is the accountUuid and AccountLabel the alias or email for it.
	// Both travel: the label is what a person reads, the uuid is what is
	// actually recorded, and a session with no attribution has neither.
	Account      string `json:"account"`
	AccountLabel string `json:"accountLabel"`
	Title        string `json:"title"`
	LastPrompt   string `json:"lastPrompt"`
	// Matches and Snippet are populated only by a deep search.
	Matches int      `json:"matches,omitempty"`
	Snippet string   `json:"snippet,omitempty"`
	Client  string   `json:"client"`
	Branch  string   `json:"branch"`
	Sources []string `json:"sources"`
	// Paths is the transcript each store holds, keyed by store. A store in
	// Sources with no entry here holds only a Desktop sidecar — it knows about
	// the session, it does not have the conversation.
	Paths map[string]string `json:"paths"`
	// CLILive says a transcript is in the live ~/.claude — the copy
	// `claude --resume` opens, so the only one resuming here can act on.
	CLILive bool   `json:"cliLive"`
	InRepo  bool   `json:"inRepo"`
	Present bool   `json:"present"`
	Profile string `json:"profile,omitempty"`
}

// LibraryView is the whole sessions window.
type LibraryView struct {
	Sessions []LibrarySession `json:"sessions"`
	Machine  string           `json:"machine"`
	// Accounts are every account the listing could offer to filter by, built
	// before the account filter runs so choosing one does not collapse the
	// choices to the one already chosen.
	Accounts []AccountOption `json:"accounts"`
	// Total is how many sessions matched before Limit trimmed the list, so the
	// window can say there is more rather than implying it showed everything.
	Total   int `json:"total"`
	Read    int `json:"read"`
	Skipped int `json:"skipped"`
	// Undated and Unattributed count sessions left out for lacking what a
	// filter needed, as opposed to failing it. They are surfaced because a
	// shrunken list otherwise reads as "there is nothing there", which is the
	// one wrong answer a session finder can give.
	Undated      int `json:"undated"`
	Unattributed int `json:"unattributed"`
	Approx       int `json:"approx"`
	// Stale names machines whose sessions this listing cannot claim to cover,
	// for the same reason: absence is only evidence when the store is complete.
	Stale []string `json:"stale"`
	// DevicesUnavailable distinguishes "no other machines" from "coverage could
	// not be established" — they look identical otherwise and mean opposite
	// things.
	DevicesUnavailable bool   `json:"devicesUnavailable"`
	Error              string `json:"error,omitempty"`
}

// handOff is the session the popup was looking at when it asked for the full
// window, so the full window can open on it rather than dropping you at the top
// of a list you have already scrolled past.
//
// Package-level rather than a field: the two windows are served by separate
// Library values, so an instance field would never be seen by the other side.
// It is a single slot — the last hand-off wins — and it is cleared when taken,
// so a later reveal of the same window does not reopen a stale session.
var handOff struct {
	mu sync.Mutex
	id string
}

// HandOff records the session the full window should open on.
func (l *Library) HandOff(ctx context.Context, id string) error {
	if !idRule.MatchString(id) {
		return errors.New("invalid session id")
	}
	handOff.mu.Lock()
	defer handOff.mu.Unlock()
	handOff.id = id
	return nil
}

// TakeHandOff returns the pending session and clears it. Empty when there is
// none, which is the normal case — the full window asks on every reveal.
func (l *Library) TakeHandOff(ctx context.Context) (string, error) {
	handOff.mu.Lock()
	defer handOff.mu.Unlock()
	id := handOff.id
	handOff.id = ""
	return id, nil
}

// Library is the read side of the sessions manager: every session this machine
// can see, from the live ~/.claude, each Claude Desktop install, and the synced
// staging repo, merged into one row apiece.
//
// Distinct from [Sessions], which browses only what the REMOTE holds and exists
// to read another machine's chat without merging it. This one answers "what
// sessions do I have"; that one answers "what is on the other Mac".
//
// Read-only by construction. Deleting a session and resuming one are both
// writes and will shell out to the CLI when they land — see the window's own
// notes on why neither is wired yet.
type Library struct{}

// NewLibrary builds the sessions service.
func NewLibrary() *Library { return &Library{} }

// ListRequest is what the window is asking for. A struct rather than a growing
// list of positional arguments: the filters are additive, and a sixth bare
// parameter is where call sites start passing them in the wrong order.
type ListRequest struct {
	// Since accepts what the CLI accepts — a day, a timestamp, or an age like
	// 7d — and "all" removes the lower bound.
	Since string `json:"since"`
	// Text narrows to sessions matching it anywhere a row shows — title, last
	// prompt, project, branch, id, client.
	Text string
	// Deep sends Text to the transcript bodies instead of the row fields. Far
	// more expensive — it reads every transcript the other filters kept — so the
	// window makes it an explicit button rather than doing it as you type.
	Deep bool `json:"cwd"`
	// Stores keeps only sessions held by every one named, and Unsynced only
	// those on this Mac that the sync does not have.
	Stores   []string `json:"stores"`
	Unsynced bool     `json:"unsynced"`
	// Account is an accountUuid.
	Account string `json:"account"`
	Limit   int    `json:"limit"`
}

// AccountOption is one account the window can filter by.
type AccountOption struct {
	Value string `json:"value"` // accountUuid
	Label string `json:"label"` // alias or email, else the uuid
}

// List returns the sessions matching the request, newest first.
func (l *Library) List(ctx context.Context, req ListRequest) (LibraryView, error) {
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return LibraryView{Error: err.Error()}, nil // a broken config is a state to render, not a failure
	}
	me := config.DetectFor(cfg)

	scope, err := libraryScope(req.Since, me.Name, time.Now())
	if err != nil {
		return LibraryView{Machine: me.Name, Error: err.Error()}, nil
	}
	scope.Devices, _ = sessions.LoadDevices()
	scope.DevicesUnavailable = scope.Devices == nil
	scope.Ledger = sessions.LoadLedger()

	// One box, two meanings: deep search sends it to the bodies, plain search to
	// the row fields. Never both — a term that matches a row but not its body
	// would otherwise vanish the moment deep search was switched on.
	text, content := req.Text, ""
	if req.Deep {
		text, content = "", req.Text
	}

	limit := req.Limit
	if limit <= 0 {
		limit = DefaultLibraryLimit
	}
	rows, rep := sessions.List(sessions.Options{
		Machine:  me,
		Roots:    sessions.Roots(cfg, me, false, false),
		Targets:  sessions.Targets(cfg, me, false, false),
		Scope:    scope,
		Text:     text,
		Content:  content,
		Stores:   req.Stores,
		Unsynced: req.Unsynced,
		Account:  req.Account,
		Limit:    limit,
	})
	return libraryView(rows, rep, me.Name, scope), nil
}

// libraryScope turns the window's time filter into a scope, applying the
// default that makes an unconfigured window useful rather than empty.
func libraryScope(since, me string, now time.Time) (sessions.Scope, error) {
	sc := sessions.Scope{Now: now, Me: me, LiveInScope: true}
	s := strings.TrimSpace(since)
	if strings.EqualFold(s, "all") {
		return sc, nil // no lower bound: the user asked for everything
	}
	if s == "" {
		s = DefaultLibrarySince
	}
	var err error
	sc.Since, err = sessions.ParseWhen(s, now, false)
	return sc, err
}

// shortSessionID is the leading segment of a session uuid — enough to recognise
// one, short enough to sit in a column.
func shortSessionID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return id
}

// toLibrarySession flattens one row into what the window holds. Shared with the
// detail panel so a session cannot describe itself differently depending on
// whether you are looking at the list or at one row of it.
func toLibrarySession(r sessions.Row) LibrarySession {
	return LibrarySession{
		ID: r.ID, Short: shortSessionID(r.ID), When: r.When, Approx: r.Approx,
		Cwd: r.Cwd, Account: r.Account, AccountLabel: r.AccountLabel,
		Title: r.Title, LastPrompt: r.LastPrompt, Client: r.Client,
		Matches: r.Matches, Snippet: r.Snippet,
		Branch: r.Branch, Sources: r.Sources, Paths: r.Paths, CLILive: r.CLILive,
		InRepo: r.InRepo, Present: r.Present, Profile: r.Profile,
	}
}

// accountOptions labels the accounts a listing saw. A uuid with no stored
// account still gets an entry under its own uuid — it is filterable either way,
// and dropping it would hide sessions belonging to a login this machine has
// simply never captured.
func accountOptions(uuids []string) []AccountOption {
	if len(uuids) == 0 {
		return nil
	}
	labels := sessions.AccountLabels()
	out := make([]AccountOption, 0, len(uuids))
	for _, u := range uuids {
		label := labels[strings.ToLower(u)]
		if label == "" {
			label = u
		}
		out = append(out, AccountOption{Value: u, Label: label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// libraryView flattens a listing into what the window holds. Split from List so
// the mapping is testable without a real ~/.claude behind it.
func libraryView(rows []sessions.Row, rep sessions.Report, me string, scope sessions.Scope) LibraryView {
	v := LibraryView{Machine: me}
	v.Sessions = make([]LibrarySession, 0, len(rows))
	for _, r := range rows {
		v.Sessions = append(v.Sessions, toLibrarySession(r))
	}
	v.Total, v.Read, v.Skipped = rep.Total, rep.Read, rep.Skipped
	v.Undated, v.Unattributed, v.Approx = rep.Undated, rep.Unattributed, rep.Approx
	v.Accounts = accountOptions(rep.Accounts)
	v.DevicesUnavailable = scope.DevicesUnavailable
	for _, d := range scope.StaleDevices() {
		v.Stale = append(v.Stale, d.Name)
	}
	return v
}
