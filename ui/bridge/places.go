package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
)

// Places answers "where was it", which is a different question from the one the
// session list answers. The list is the right tool when you remember something
// ABOUT a session — a word in it, roughly when it was. It is the wrong tool when
// what you remember is where you were sitting: which Desktop, which profile,
// which project. Then the only useful move is to open that place and look.
//
// So this walks stores rather than sessions, and shows everything a store holds:
// the sidecars a Desktop keeps for sessions whose transcripts live elsewhere,
// the config each profile carries, the transcripts under a project. A sidecar
// with no transcript is exactly the thing a session-shaped listing cannot show
// you, and exactly the thing that explains where a session went.
type Places struct{}

func NewPlaces() *Places { return &Places{} }

// Where a store's bytes are: on this machine, or in the synced repo. The same
// Desktop appears under both, and which one you are looking at is the whole
// point when a session is in one and not the other.
const (
	WhereLive = "live"
	WhereRepo = "repo"
)

// Item kinds, in the order a store lists them.
const (
	ItemSidecar    = "sidecar"    // Desktop's record of a session
	ItemCowork     = "cowork"     // a local-agent-mode session
	ItemTranscript = "transcript" // a CLI conversation
	ItemConfig     = "config"     // settings the store carries
	ItemAux        = "aux"        // subagents, tool-results — a transcript's baggage
)

// PlaceStore is one place things are kept.
type PlaceStore struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Where string `json:"where"`
	// Kind is "cli", "desktop" or "profile" — what sort of thing this is, which
	// decides how its insides are grouped.
	Kind    string `json:"kind"`
	Profile string `json:"profile,omitempty"`
	Path    string `json:"path"`
	// Present distinguishes a store that is empty from one that is not there.
	// They render the same and mean opposite things: an empty profile is a
	// profile with nothing in it, an absent one has not been synced here.
	Present bool `json:"present"`
	// Counts summarise without opening. Groups is how many drill-downs are
	// inside; the rest are totals across them.
	Groups      int `json:"groups"`
	Sidecars    int `json:"sidecars"`
	Cowork      int `json:"cowork"`
	Transcripts int `json:"transcripts"`
	Configs     int `json:"configs"`
}

// PlaceGroup is one drill-down inside a store: a Desktop workspace, or a CLI
// project directory.
type PlaceGroup struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Note is the group's own detail — the account a workspace belongs to, the
	// working directory a project slug decodes to.
	Note  string `json:"note,omitempty"`
	Items int    `json:"items"`
	// Latest is the most recent thing in the group, so a list of eighty project
	// slugs can be ordered by when you were last in one. That ordering is what
	// makes this findable: you rarely remember the slug, you remember it was
	// last week.
	Latest time.Time `json:"latest"`
}

// PlaceItem is one thing a store holds.
type PlaceItem struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Session is the session id where the item has one, so a row here can hand
	// off to the session list rather than being a dead end.
	Session string `json:"session,omitempty"`
	// CLISession is the transcript a Desktop sidecar points at. It is the link
	// between "Desktop knows about this" and "the conversation is over there",
	// and it is the answer to most of the questions this window exists for.
	CLISession string    `json:"cliSession,omitempty"`
	Path       string    `json:"path"`
	Cwd        string    `json:"cwd,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	Bytes      int64     `json:"bytes"`
	When       time.Time `json:"when"`
	Archived   bool      `json:"archived,omitempty"`
}

// PlacesView is the store list.
type PlacesView struct {
	Stores []PlaceStore `json:"stores"`
	Error  string       `json:"error,omitempty"`
}

// GroupsView is one store's drill-downs.
type GroupsView struct {
	Store  PlaceStore   `json:"store"`
	Groups []PlaceGroup `json:"groups"`
	// Config sits beside the groups rather than inside one: it belongs to the
	// store itself, not to any workspace or project in it.
	Config []PlaceItem `json:"config"`
	Error  string      `json:"error,omitempty"`
}

// ItemsView is one group's contents.
type ItemsView struct {
	Items []PlaceItem `json:"items"`
	Error string      `json:"error,omitempty"`
}

// Stores lists every place, live and synced, without opening any of them. The
// counts come from a directory walk, which is cheap next to reading 4,000 files
// — and reading them is what the drill-down is for.
func (p *Places) Stores(ctx context.Context) (PlacesView, error) {
	locs, err := placeLocations()
	if err != nil {
		return PlacesView{Error: err.Error()}, nil
	}
	view := PlacesView{}
	for _, loc := range locs {
		view.Stores = append(view.Stores, describeStore(loc))
	}
	return view, nil
}

// Groups opens one store far enough to list what is inside it, still without
// reading any session bodies.
func (p *Places) Groups(ctx context.Context, storeID string) (GroupsView, error) {
	loc, err := findLocation(storeID)
	if err != nil {
		return GroupsView{Error: err.Error()}, nil
	}
	out := GroupsView{Store: describeStore(loc), Config: storeConfig(loc)}
	if loc.kind == "cli" {
		out.Groups = cliProjects(loc)
	} else {
		out.Groups = desktopWorkspaces(loc)
	}
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Latest.After(out.Groups[j].Latest) })
	return out, nil
}

// Items reads one group. This is the only call that opens files, and it opens
// only the sidecars of the one workspace or project asked for.
func (p *Places) Items(ctx context.Context, storeID, groupID string) (ItemsView, error) {
	loc, err := findLocation(storeID)
	if err != nil {
		return ItemsView{Error: err.Error()}, nil
	}
	dir, err := groupDir(loc, groupID)
	if err != nil {
		return ItemsView{Error: err.Error()}, nil
	}
	var items []PlaceItem
	if loc.kind == "cli" {
		items = cliItems(dir)
	} else {
		items = desktopItems(dir)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].When.After(items[j].When) })
	return ItemsView{Items: items}, nil
}

// location is one store's identity and where its bytes are.
type location struct {
	id      string
	label   string
	where   string
	kind    string
	profile string
	base    string
}

// placeLocations enumerates every store: the live roots on this machine, then
// the repo's copy of each. Deliberately not sessions.Roots — that answers "where
// might a transcript be", and drops the CLI root, which is most of what someone
// looking for a session wants to browse.
func placeLocations() ([]location, error) {
	cfg, err := config.LoadOrDefault()
	if err != nil {
		return nil, err
	}
	me := config.DetectFor(cfg)
	var out []location

	if loc, _ := cfg.RootLocation("cli", me); loc != "" {
		out = append(out, location{id: "live:cli", label: "~/.claude", where: WhereLive, kind: "cli", base: loc})
	}
	if loc, _ := cfg.RootLocation("desktop", me); loc != "" {
		out = append(out, location{id: "live:desktop", label: "Claude Desktop", where: WhereLive, kind: "desktop", base: loc})
	}
	if store, err := desktop.DefaultStore(); err == nil {
		profiles, _ := store.List()
		for _, pr := range profiles {
			out = append(out, location{
				id: "live:desktop@" + pr.Name, label: pr.Label(), where: WhereLive,
				kind: "profile", profile: pr.Name, base: pr.DataDir()})
		}
	}
	staging, err := config.StagingDir()
	if err != nil {
		return out, nil // no repo yet is a state, not a failure
	}
	out = append(out,
		location{id: "repo:cli", label: "~/.claude", where: WhereRepo, kind: "cli", base: filepath.Join(staging, "cli")},
		location{id: "repo:desktop", label: "Claude Desktop", where: WhereRepo, kind: "desktop", base: filepath.Join(staging, "desktop")})
	for _, name := range engine.StagedProfileNames(staging) {
		out = append(out, location{
			id: "repo:desktop@" + name, label: name, where: WhereRepo,
			kind: "profile", profile: name, base: engine.StagedProfileDataDir(staging, name)})
	}
	return out, nil
}

func findLocation(id string) (location, error) {
	locs, err := placeLocations()
	if err != nil {
		return location{}, err
	}
	for _, l := range locs {
		if l.id == id {
			return l, nil
		}
	}
	return location{}, os.ErrNotExist
}

// The two Desktop session trees, and the CLI's project tree.
const (
	codeSessions   = "claude-code-sessions"
	coworkSessions = "local-agent-mode-sessions"
	cliProjectsDir = "projects"
)

// describeStore counts what is in a store by walking its directories. No file is
// opened: the counts exist so the store list can say "51 sidecars" rather than
// making someone click into an empty place to find out it is empty.
func describeStore(loc location) PlaceStore {
	s := PlaceStore{
		ID: loc.id, Label: loc.label, Where: loc.where,
		Kind: loc.kind, Profile: loc.profile, Path: loc.base,
	}
	info, err := os.Stat(loc.base)
	s.Present = err == nil && info.IsDir()
	if !s.Present {
		return s
	}
	if loc.kind == "cli" {
		groups := cliProjects(loc)
		s.Groups = len(groups)
		for _, g := range groups {
			s.Transcripts += g.Items
		}
	} else {
		groups := desktopWorkspaces(loc)
		s.Groups = len(groups)
		for _, g := range groups {
			s.Sidecars += g.Items
		}
		s.Cowork = countFiles(filepath.Join(loc.base, coworkSessions), ".json")
	}
	s.Configs = len(storeConfig(loc))
	return s
}

// storeConfig lists the settings a store carries in its own right — the MCP
// server list, the worktree registry, a profile's own record of itself. These
// are the "items stored in each profile" that no session listing shows, and the
// reason a profile that looks empty of sessions may still be worth restoring.
func storeConfig(loc location) []PlaceItem {
	var items []PlaceItem
	add := func(dir, name string) {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			return
		}
		items = append(items, PlaceItem{
			Kind: ItemConfig, Label: name, Path: p,
			Bytes: info.Size(), When: info.ModTime(),
		})
	}
	if loc.kind == "cli" {
		for _, n := range []string{"settings.json", "settings.local.json", "CLAUDE.md"} {
			add(loc.base, n)
		}
		return items
	}
	for _, n := range []string{
		"claude_desktop_config.json", "config.json", "git-worktrees.json",
		"extensions-blocklist.json", "cowork-enabled-cli-ops.json",
	} {
		add(loc.base, n)
	}
	// A profile keeps its own record one level up from data/, so it survives
	// even when the app tree below it is empty.
	if loc.kind == "profile" {
		add(filepath.Dir(loc.base), "profile.json")
	}
	return items
}

// desktopWorkspaces lists the workspaces under a Desktop's session tree. The
// layout is <account>/<workspace>/local_<id>.json, and both levels are opaque
// uuids — so the group carries the account it belongs to as its note, and is
// ordered by recency, which is the only handle anyone actually has on them.
func desktopWorkspaces(loc location) []PlaceGroup {
	root := filepath.Join(loc.base, codeSessions)
	accounts, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var groups []PlaceGroup
	for _, acct := range accounts {
		if !acct.IsDir() {
			continue
		}
		spaces, err := os.ReadDir(filepath.Join(root, acct.Name()))
		if err != nil {
			continue
		}
		for _, sp := range spaces {
			if !sp.IsDir() {
				continue
			}
			dir := filepath.Join(root, acct.Name(), sp.Name())
			n, latest, newest := countLatestNewest(dir, ".json")
			if n == 0 {
				continue
			}
			// A uuid is not a handle anyone has on their own work. The one
			// thing that makes a workspace recognisable is what was being
			// worked on in it, so the most recent sidecar in it is read for
			// its working directory and that becomes the label. Costs one file
			// read per workspace, of which there are a handful.
			//
			// Both uuids stay on the row, because they are what the folder is
			// actually called if someone goes looking on disk — and because
			// two accounts here have workspaces with the same id, so neither
			// uuid identifies one on its own.
			g := PlaceGroup{
				ID:    acct.Name() + "/" + sp.Name(),
				Label: shortSessionID(acct.Name()) + " / " + shortSessionID(sp.Name()),
				Items: n, Latest: latest,
			}
			if cwd := sidecarCwd(newest); cwd != "" {
				g.Label = trimHome(cwd)
				g.Note = shortSessionID(acct.Name()) + " / " + shortSessionID(sp.Name())
			}
			groups = append(groups, g)
		}
	}
	return groups
}

// cliProjects lists the project directories under a CLI root. The slug is the
// working directory with its separators flattened, so it is turned back into
// something readable — a path is what someone remembers, not a slug.
func cliProjects(loc location) []PlaceGroup {
	root := filepath.Join(loc.base, cliProjectsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var groups []PlaceGroup
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, latest := countAndLatest(filepath.Join(root, e.Name()), ".jsonl")
		if n == 0 {
			continue
		}
		groups = append(groups, PlaceGroup{
			ID: e.Name(), Label: unslug(e.Name()), Note: e.Name(),
			Items: n, Latest: latest,
		})
	}
	return groups
}

// groupDir resolves a group id to a directory, refusing anything that climbs out
// of the store. The id reaches here from the window, so it is input.
func groupDir(loc location, groupID string) (string, error) {
	if groupID == "" || strings.Contains(groupID, "..") || filepath.IsAbs(groupID) {
		return "", os.ErrNotExist
	}
	base := filepath.Join(loc.base, cliProjectsDir)
	if loc.kind != "cli" {
		base = filepath.Join(loc.base, codeSessions)
	}
	dir := filepath.Join(base, filepath.FromSlash(groupID))
	if !strings.HasPrefix(dir, base+string(os.PathSeparator)) {
		return "", os.ErrNotExist
	}
	return dir, nil
}

// desktopItems reads the sidecars in one workspace. A sidecar is Desktop's
// record of a session: its title, where it was run, and — the useful part —
// the CLI session id whose transcript holds the actual conversation.
func desktopItems(dir string) []PlaceItem {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []PlaceItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		it := PlaceItem{
			Kind: ItemSidecar, Label: e.Name(), Path: filepath.Join(dir, e.Name()),
			Bytes: info.Size(), When: info.ModTime(),
		}
		// scheduled-tasks.json sits beside the sidecars and is not one.
		if !strings.HasPrefix(e.Name(), "local_") {
			it.Kind = ItemConfig
			items = append(items, it)
			continue
		}
		readSidecar(it.Path, &it)
		items = append(items, it)
	}
	return items
}

// readSidecar fills in what the file itself says. Best-effort throughout: a
// sidecar that will not parse still belongs in the listing, because its
// existence is the fact being looked for.
func readSidecar(path string, it *PlaceItem) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var s struct {
		SessionID      string `json:"sessionId"`
		CLISessionID   string `json:"cliSessionId"`
		Title          string `json:"title"`
		Cwd            string `json:"cwd"`
		Branch         string `json:"branch"`
		IsArchived     bool   `json:"isArchived"`
		LastActivityAt int64  `json:"lastActivityAt"`
	}
	if json.Unmarshal(b, &s) != nil {
		return
	}
	if s.Title != "" {
		it.Label = s.Title
	}
	it.Session, it.CLISession = s.SessionID, s.CLISessionID
	// Shortened here rather than in the window: Desktop writes some of these
	// already portablised to $HOME and others as real paths, which the window's
	// own tilde() does not know about — so leaving it to the frontend put two
	// different spellings of the same home directory in one list.
	it.Cwd, it.Branch, it.Archived = trimHome(s.Cwd), s.Branch, s.IsArchived
	// Desktop records milliseconds. Trusted only when it is there: the file's
	// own mtime is the fallback, and both are approximations of the same thing.
	if s.LastActivityAt > 0 {
		it.When = time.UnixMilli(s.LastActivityAt)
	}
}

// cliItems lists one project's transcripts, and the directories a transcript
// keeps beside it — subagent runs and saved tool output, which are easy to
// forget are there until a restore does not bring them back.
func cliItems(dir string) []PlaceItem {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []PlaceItem
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			n, latest := countAndLatest(p, "")
			if n == 0 {
				continue
			}
			items = append(items, PlaceItem{
				Kind: ItemAux, Label: e.Name(), Session: e.Name(), Path: p,
				When: latest, Bytes: int64(n),
			})
			continue
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		items = append(items, PlaceItem{
			Kind: ItemTranscript, Label: id, Session: id, Path: p,
			Bytes: info.Size(), When: info.ModTime(),
		})
	}
	return items
}

// sidecarCwd reads just the working directory out of one sidecar. Best-effort:
// a label is a nicety, and a sidecar that will not parse must not cost the
// listing the group it belongs to.
func sidecarCwd(path string) string {
	if path == "" || !strings.HasPrefix(filepath.Base(path), "local_") {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var s struct {
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	return s.Cwd
}

// trimHome shortens a path for display without hiding which machine's home it
// came from when it is not this one.
func trimHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(p, home); ok {
			return "~" + rest
		}
	}
	// Desktop writes some of these already portablised.
	if rest, ok := strings.CutPrefix(p, "$HOME"); ok {
		return "~" + rest
	}
	return p
}

// countFiles counts files under dir at any depth, optionally by suffix.
func countFiles(dir, suffix string) int {
	n, _ := countAndLatest(dir, suffix)
	return n
}

// countAndLatest counts matching files under dir at any depth and reports the
// newest mtime among them.
func countAndLatest(dir, suffix string) (int, time.Time) {
	n, latest, _ := countLatestNewest(dir, suffix)
	return n, latest
}

// countLatestNewest also reports which file the newest mtime belonged to, so a
// caller can read that one for a label without walking twice.
func countLatestNewest(dir, suffix string) (int, time.Time, string) {
	var n int
	var latest time.Time
	var newest string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if suffix != "" && !strings.HasSuffix(d.Name(), suffix) {
			return nil
		}
		n++
		if info, ierr := d.Info(); ierr == nil && info.ModTime().After(latest) {
			latest, newest = info.ModTime(), p
		}
		return nil
	})
	return n, latest, newest
}

// unslug turns a project slug back into the path it was made from. Claude Code
// flattens separators to dashes, which is lossy — a dash in a real directory
// name is indistinguishable from a separator — so this is a reading aid, and
// the slug itself is kept beside it rather than replaced.
func unslug(slug string) string {
	if !strings.HasPrefix(slug, "-") {
		return slug
	}
	return "/" + strings.ReplaceAll(strings.TrimPrefix(slug, "-"), "-", "/")
}
