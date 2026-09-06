package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/sessions"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
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
	// Account is who this store is signed in as. Three Desktop trees live on one
	// machine — the app's own, and one per clauderig profile — and the only
	// thing that tells them apart at a glance is whose sessions are inside.
	Account string `json:"account,omitempty"`
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
	Note string `json:"note,omitempty"`
	// Account is who the sessions in this group belong to. A Desktop store holds
	// more than one account's sessions side by side — the app shows you one at a
	// time, so a listing that merges them shows you a folder you recognise
	// containing sessions you do not.
	Account string `json:"account,omitempty"`
	Items   int    `json:"items"`
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
	// Deleted marks a session Claude Desktop has removed from its own sidebar.
	// The record survives, and it is exactly what someone hunting a session
	// they can no longer see is looking for.
	Deleted bool `json:"deleted,omitempty"`
	// Worktree and PRs are what the session was working on. A worktree name is
	// often the only thing anyone remembers about a piece of work.
	Worktree string   `json:"worktree,omitempty"`
	PRs      []string `json:"prs,omitempty"`
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

// FolderContents is one folder and everything filed in it.
type FolderContents struct {
	Folder PlaceGroup  `json:"folder"`
	Items  []PlaceItem `json:"items"`
}

// ContentsView is a whole store, folders and sessions together.
type ContentsView struct {
	Store   PlaceStore       `json:"store"`
	Config  []PlaceItem      `json:"config"`
	Folders []FolderContents `json:"folders"`
	Error   string           `json:"error,omitempty"`
}

// Contents returns everything in one store in a single call, so the window can
// show the whole thing at once the way Claude Desktop's sidebar does — folders
// with their sessions under them, nothing hidden behind a disclosure.
//
// Whole-store rather than per-folder because the alternative is 174 round trips
// to draw one list. The cost is small and known: the largest store here is 174
// folders and 1,102 sessions, which is 0.3 MB and under a fifth of a second,
// because listing a folder reads directory entries rather than the files in it.
func (p *Places) Contents(ctx context.Context, storeID string) (ContentsView, error) {
	loc, err := findLocation(storeID)
	if err != nil {
		return ContentsView{Error: err.Error()}, nil
	}
	out := ContentsView{Store: describeStore(loc), Config: storeConfig(loc)}
	groups, gerr := p.Groups(ctx, storeID)
	if gerr != nil {
		return ContentsView{Error: gerr.Error()}, nil
	}
	if groups.Error != "" {
		return ContentsView{Error: groups.Error}, nil
	}
	for _, g := range groups.Groups {
		items, ierr := p.Items(ctx, storeID, g.ID)
		if ierr != nil || items.Error != "" {
			// One unreadable folder must not cost the whole store its listing:
			// it is still shown, with nothing in it, which is the honest report.
			out.Folders = append(out.Folders, FolderContents{Folder: g})
			continue
		}
		out.Folders = append(out.Folders, FolderContents{Folder: g, Items: items.Items})
	}
	return out, nil
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
		out.Groups = cliProjects(loc, true)
	} else {
		out.Groups = desktopFolders(loc)
	}
	sortGroups(out.Groups)
	return out, nil
}

// sortGroups puts an account's folders together, then orders within them.
//
// Sorting on recency alone interleaved the accounts — a brightshore folder
// landing between two relatecpa ones — and since the heading is drawn whenever
// the account changes, one account appeared three times down a list of seven.
// The store holds both logins at once; it should say so once.
//
// Accounts are ordered by their own most recent activity, so the one you were
// last in is at the top. The deleted bucket sinks to the end of its account:
// it is a place to go looking, not something you were working in.
func sortGroups(groups []PlaceGroup) {
	latest := map[string]time.Time{}
	for _, g := range groups {
		if g.Latest.After(latest[g.Account]) {
			latest[g.Account] = g.Latest
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.Account != b.Account {
			return latest[a.Account].After(latest[b.Account])
		}
		if ad, bd := a.Label == deletedFolder, b.Label == deletedFolder; ad != bd {
			return bd
		}
		return a.Latest.After(b.Latest)
	})
}

// Items reads one group. This is the only call that opens files, and it opens
// only the sidecars of the one workspace or project asked for.
func (p *Places) Items(ctx context.Context, storeID, groupID string) (ItemsView, error) {
	loc, err := findLocation(storeID)
	if err != nil {
		return ItemsView{Error: err.Error()}, nil
	}
	var items []PlaceItem
	if loc.kind == "cli" {
		dir, derr := groupDir(loc, groupID)
		if derr != nil {
			return ItemsView{Error: derr.Error()}, nil
		}
		items = cliItems(dir)
	} else {
		rest, ok := strings.CutPrefix(groupID, "folder:")
		if !ok {
			return ItemsView{Error: "unknown folder"}, nil
		}
		account, folder, _ := strings.Cut(rest, "\x00")
		for _, sc := range scanSidecars(filepath.Join(loc.base, codeSessions)) {
			if sc.folder == folder && sc.account == account {
				items = append(items, sc.item)
			}
		}
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
	s.Account = storeAccount(loc)
	if loc.kind == "cli" {
		// Counting only: resolving each project's real path means opening a
		// transcript per project, and the store list has no business doing
		// that for four thousand of them before anyone has clicked anything.
		groups := cliProjects(loc, false)
		s.Groups = len(groups)
		for _, g := range groups {
			s.Transcripts += g.Items
		}
	} else {
		groups := desktopFolders(loc)
		s.Groups = len(groups)
		for _, g := range groups {
			s.Sidecars += g.Items
		}
		s.Cowork = countFiles(filepath.Join(loc.base, coworkSessions), ".json")
	}
	s.Configs = len(storeConfig(loc))
	return s
}

// storeAccount reports who a Desktop store is signed in as, by three routes in
// descending order of how much they know.
//
// The app records lastKnownAccountUuid in its own config, which is the direct
// answer for a tree on this machine. The synced copies do not have it — sync
// keeps only the stable `preferences` out of that file — so a profile falls back
// to the login it was created for, and the machine-wide copy falls back to
// whoever owns most of the sessions in it. The last is a reading of the
// evidence rather than a record, and it is right for the same reason the
// listing is useful at all: the sessions are the thing that is actually there.
func storeAccount(loc location) string {
	if loc.kind == "cli" {
		return ""
	}
	labels := sessions.AccountLabels()
	if uuid := lastKnownAccount(loc.base); uuid != "" {
		if label := labels[strings.ToLower(uuid)]; label != "" {
			return label
		}
	}
	if loc.profile != "" {
		if email := profileEmail(filepath.Dir(loc.base)); email != "" {
			return email
		}
	}
	count := map[string]int{}
	for _, sc := range scanSidecars(filepath.Join(loc.base, codeSessions)) {
		if sc.account != "" {
			count[sc.account]++
		}
	}
	best, most := "", 0
	for uuid, n := range count {
		if n > most {
			best, most = uuid, n
		}
	}
	if label := labels[strings.ToLower(best)]; label != "" {
		return label
	}
	return ""
}

func lastKnownAccount(base string) string {
	b, err := os.ReadFile(filepath.Join(base, "config.json"))
	if err != nil {
		return ""
	}
	var c struct {
		LastKnownAccountUUID string `json:"lastKnownAccountUuid"`
	}
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return c.LastKnownAccountUUID
}

func profileEmail(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "profile.json"))
	if err != nil {
		return ""
	}
	var p struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(b, &p) != nil {
		return ""
	}
	return p.Email
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

// desktopFolders groups a Desktop's sessions the way Desktop itself does: by
// the folder they were opened in. Each sidecar records that as originCwd, and
// it is what the app's own sidebar puts its headings on — so this window and
// the app agree about where a session lives, which is the whole point when
// someone is retracing where they were.
//
// The uuids in the path (<account>/<workspace>) are not that. They are opaque,
// they repeat across accounts, and nobody has ever remembered one.
func desktopFolders(loc location) []PlaceGroup {
	labels := sessions.AccountLabels()
	byFolder := map[string]*PlaceGroup{}
	for _, sc := range scanSidecars(filepath.Join(loc.base, codeSessions)) {
		who := labels[strings.ToLower(sc.account)]
		if who == "" {
			who = shortSessionID(sc.account)
		}
		key := who + "\x00" + sc.folder
		g := byFolder[key]
		if g == nil {
			g = &PlaceGroup{
				ID: folderID(sc.account, sc.folder), Label: sc.folder,
				Account: who, Note: "opened here",
			}
			byFolder[key] = g
		}
		g.Items++
		if sc.item.When.After(g.Latest) {
			g.Latest = sc.item.When
		}
	}
	groups := make([]PlaceGroup, 0, len(byFolder))
	for _, g := range byFolder {
		groups = append(groups, *g)
	}
	return groups
}

// sidecarRef is one Desktop session record and the folder it belongs under.
type sidecarRef struct {
	folder  string
	account string
	item    PlaceItem
}

// scanSidecars reads every session record under a Desktop's session tree,
// including the deleted_ ones — a session the app has dropped from its sidebar
// still has a record here, and that record is often the whole reason someone
// opened this window.
func scanSidecars(root string) []sidecarRef {
	var out []sidecarRef
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// The tree is <account>/<workspace>/<record>, so the account is the
		// first segment below the root.
		account := ""
		if rel, rerr := filepath.Rel(root, path); rerr == nil {
			if parts := strings.Split(filepath.ToSlash(rel), "/"); len(parts) > 1 {
				account = parts[0]
			}
		}
		name := d.Name()
		deleted := strings.HasPrefix(name, "deleted_")
		if !deleted && !strings.HasPrefix(name, "local_") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		it := PlaceItem{
			Kind: ItemSidecar, Label: name, Path: path,
			Bytes: info.Size(), When: info.ModTime(), Deleted: deleted,
		}
		if deleted {
			// A deleted record is a tombstone, not a session: the whole file is
			// the millisecond it was deleted at, and the id is in its name.
			// There is no title and no folder to file it under, so they get a
			// place of their own rather than a bucket of unknowns — "these were
			// deleted, and when" is a complete answer on its own.
			it.Session = strings.TrimSuffix(strings.TrimPrefix(name, "deleted_"), ".json")
			it.Label = shortSessionID(it.Session)
			if b, rerr := os.ReadFile(path); rerr == nil {
				if ms, cerr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); cerr == nil && ms > 0 {
					it.When = time.UnixMilli(ms)
				}
			}
			out = append(out, sidecarRef{folder: deletedFolder, account: account, item: it})
			return nil
		}
		folder := readSidecar(path, &it)
		if folder == "" {
			folder = unknownFolder
		}
		// A record with no title of its own reads better as its own id than as
		// the filename that id is wrapped in: "local_051bc295-…json" is the
		// same string with noise either side.
		if it.Label == name {
			id := strings.TrimSuffix(strings.TrimPrefix(name, "local_"), ".json")
			it.Label = "(untitled) " + shortSessionID(id)
		}
		out = append(out, sidecarRef{folder: folder, account: account, item: it})
		return nil
	})
	return out
}

// unknownFolder is where records that do not say go, rather than being dropped.
const unknownFolder = "(folder not recorded)"

// deletedFolder collects the tombstones Claude Desktop leaves behind. Someone
// who cannot find a session at all is often looking for one of these, and "it
// was deleted, here is when" is the answer they came for.
const deletedFolder = "Deleted in Desktop"

// folderID encodes a folder path as a group id. The id travels to the window
// and back, and a raw path would collide with the CLI store's slug ids and read
// as a path traversal on the way in.
func folderID(account, folder string) string { return "folder:" + account + "\x00" + folder }

// cliProjects lists the project directories under a CLI root. The slug is the
// working directory with its separators flattened, so it is turned back into
// something readable — a path is what someone remembers, not a slug.
func cliProjects(loc location, resolve bool) []PlaceGroup {
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
		dir := filepath.Join(root, e.Name())
		n, latest, newest := countLatestNewest(dir, ".jsonl")
		if n == 0 {
			continue
		}
		g := PlaceGroup{ID: e.Name(), Label: unslug(e.Name()), Note: e.Name(), Items: n, Latest: latest}
		if resolve {
			if real := projectPath(e.Name(), newest); real != "" {
				g.Label = trimHome(real)
			}
		}
		groups = append(groups, g)
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

// readSidecar fills in what the file itself says. Best-effort throughout: a
// sidecar that will not parse still belongs in the listing, because its
// existence is the fact being looked for.
func readSidecar(path string, it *PlaceItem) (folder string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var s struct {
		SessionID      string `json:"sessionId"`
		CLISessionID   string `json:"cliSessionId"`
		Title          string `json:"title"`
		Cwd            string `json:"cwd"`
		OriginCwd      string `json:"originCwd"`
		Branch         string `json:"branch"`
		WorktreeName   string `json:"worktreeName"`
		IsArchived     bool   `json:"isArchived"`
		LastActivityAt int64  `json:"lastActivityAt"`
		PRs            []struct {
			Repo   string `json:"repo"`
			Number int    `json:"prNumber"`
			State  string `json:"state"`
		} `json:"prs"`
	}
	if json.Unmarshal(b, &s) != nil {
		return ""
	}
	it.Worktree = s.WorktreeName
	for _, pr := range s.PRs {
		label := "#" + strconv.Itoa(pr.Number)
		if pr.Repo != "" {
			label = pr.Repo + label
		}
		if pr.State != "" && !strings.EqualFold(pr.State, "OPEN") {
			label += " (" + strings.ToLower(pr.State) + ")"
		}
		it.PRs = append(it.PRs, label)
	}
	// originCwd is the folder Desktop files the session under — the heading its
	// own sidebar shows. cwd is where the session actually ran, which for a
	// worktree session is several levels below that.
	folder = trimHome(s.OriginCwd)
	if folder == "" {
		folder = trimHome(s.Cwd)
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
	return folder
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

// projectPath recovers a project directory's real name. The slug it is filed
// under has had every separator and dot flattened to a dash, so it cannot be
// reversed — "-Users-john-Git-XTerm-NET" reads back as .../XTerm/NET, and the
// directory is actually XTerm.NET. Showing that guess as the label is worse
// than useless: it is a path that looks right and is not.
//
// The transcripts inside record the working directory they ran in, so the real
// spelling is available. It is not simply trusted, because a transcript can sit
// under a parent project's slug while its own cwd is a worktree several levels
// down. Instead the cwd's prefixes are re-slugged until one matches the
// directory name — that prefix is the project, spelled the way it really is,
// and proved so rather than assumed.
func projectPath(slug, transcript string) string {
	cwd := transcriptCwd(transcript)
	for p := cwd; p != "" && p != "/" && p != "."; p = filepath.Dir(p) {
		if slugOf(p) == slug {
			return p
		}
	}
	return ""
}

// slugOf reproduces how Claude Code names a project directory: every character
// that is not a letter or a digit becomes a dash.
func slugOf(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// transcriptCwd reads the first working directory a transcript mentions. The
// opening records are session bookkeeping and carry none, so it reads a little
// way in — bounded, because a transcript that never says is not going to.
func transcriptCwd(path string) string {
	if path == "" {
		return ""
	}
	f, err := transcript.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for i := 0; i < 40 && sc.Scan(); i++ {
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Cwd != "" {
			return rec.Cwd
		}
	}
	return ""
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
