// Package sessions answers one question for every front end: what Claude Code
// sessions exist, and what do we know about each.
//
// A session is not one file in one place. Its transcript may sit in this
// machine's live ~/.claude, in the synced staging repo, or in neither; its
// title, project and account may come from a Claude Desktop sidecar filed under
// any of several Desktop installs; and once a transcript ages out of the synced
// window the permanent ledger is all that remains. Assembling those into one
// row is fiddly, easy to get subtly wrong, and was for a while implemented only
// inside the `recent` command — where the UI could not reach it.
//
// It lives here so the CLI and the desktop UI answer with the same facts. The
// package is deliberately render-free: it returns [Row] values and no strings
// anyone would print directly.
package sessions

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/engine"
	"github.com/rigsmith/rigsmith/internal/clauderig/search"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
	"github.com/rigsmith/rigsmith/internal/clauderig/transcript"
)

// Source labels. CLISource is the live ~/.claude root — the only one
// `claude --resume` reads; DesktopSource is the Claude Desktop app-support tree
// (cowork transcripts); RepoSource is the synced staging copy.
const (
	CLISource     = "cli"
	DesktopSource = "desktop"
	RepoSource    = "repo"
)

// Roots lists the Desktop sidecar trees to scan for titles, projects and
// account attribution: the machine-wide Desktop install, every clauderig-managed
// profile, and their staged counterparts in the synced repo.
func Roots(cfg *config.Config, me config.Machine, liveOnly, repoOnly bool) []session.Root {
	var roots []session.Root
	if !repoOnly {
		if loc, _ := cfg.RootLocation("desktop", me); loc != "" {
			roots = append(roots, session.Root{Label: DesktopSource, Base: loc})
		}
		if store, err := desktop.DefaultStore(); err == nil {
			profiles, _ := store.List() // absent store → no profiles, not an error
			for _, p := range profiles {
				roots = append(roots, session.Root{
					Label: DesktopSource, Base: p.DataDir(), Profile: p.Name})
			}
		}
	}
	if !liveOnly {
		if staging, err := config.StagingDir(); err == nil {
			roots = append(roots, session.Root{Label: RepoSource, Base: filepath.Join(staging, "desktop")})
			// Shared with `restore` rather than re-scanned here: one definition
			// of which profiles are staged, and it validates the name before it
			// becomes a path.
			for _, name := range engine.StagedProfileNames(staging) {
				roots = append(roots, session.Root{
					Label: RepoSource, Base: engine.StagedProfileDataDir(staging, name), Profile: name})
			}
		}
	}
	return roots
}

// Targets lists the directory trees that hold transcripts: each enabled root on
// this machine, plus the synced staging repo.
func Targets(cfg *config.Config, me config.Machine, liveOnly, repoOnly bool) []search.Target {
	var targets []search.Target
	if !repoOnly {
		for _, r := range cfg.Roots {
			if !r.Enabled {
				continue
			}
			if loc, _ := cfg.RootLocation(r.ID, me); loc != "" {
				targets = append(targets, search.Target{Label: r.ID, Dir: loc})
			}
		}
	}
	if !liveOnly {
		if staging, err := config.StagingDir(); err == nil {
			targets = append(targets, search.Target{Label: RepoSource, Dir: staging})
		}
	}
	return targets
}

// TranscriptPaths maps session id → transcript path for one target label. The
// first copy found wins, so callers scan live-first when they want the copy
// `claude --resume` would open.
func TranscriptPaths(targets []search.Target, label string) map[string]string {
	paths, _ := TranscriptPathsAll(targets, label)
	return paths
}

// TranscriptPathsAll is TranscriptPaths, plus the copies it did not choose.
//
// One session can be filed under two project slugs — start in one directory,
// continue in another, and Claude Code writes a second transcript under the new
// slug while the first stays frozen at the moment you moved. Both are real
// files with the same id.
//
// This used to keep whichever the directory walk reached first and discard the
// rest silently. That is filesystem order, which is to say alphabetical luck: a
// session whose stale copy happened to sort first showed its state from the day
// it split, with no indication that a complete copy existed. It is how a week of
// work looked lost.
//
// The winner is now the transcript whose last record is newest, and the losers
// come back so a caller can report them instead of pretending they are not
// there.
func TranscriptPathsAll(targets []search.Target, label string) (paths map[string]string, extra map[string][]string) {
	found := map[string][]string{}
	for _, t := range targets {
		if t.Label != label || t.Dir == "" {
			continue
		}
		filepath.WalkDir(t.Dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(t.Dir, p)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if !strings.HasSuffix(rel, ".jsonl") || !IsSessionTranscriptRel(rel) {
				return nil
			}
			if id := session.IDFromTranscriptRel(rel); id != "" {
				found[id] = append(found[id], p)
			}
			return nil
		})
	}

	paths, extra = map[string]string{}, map[string][]string{}
	for id, list := range found {
		if len(list) == 1 {
			paths[id] = list[0]
			continue
		}
		// Only here does the extra read happen. Duplicates are rare — one in
		// several hundred on a working machine — so dating every candidate
		// costs nothing in the ordinary case and is the only way to be right in
		// the case that matters.
		best := newestTranscript(list)
		paths[id] = best
		for _, p := range list {
			if p != best {
				extra[id] = append(extra[id], p)
			}
		}
		sort.Strings(extra[id])
	}
	return paths, extra
}

// newestTranscript picks the copy whose last record is latest.
//
// Ties break on size and then on path: a transcript that cannot be dated must
// not make the choice depend on walk order again, and two identical files have
// to resolve the same way on every run or the UI flickers between them.
func newestTranscript(list []string) string {
	best, bestAt, bestSize := "", time.Time{}, int64(-1)
	for _, p := range list {
		var at time.Time
		if act, ok := session.LastActivity(p); ok {
			at = act.At
		}
		var size int64
		if info, err := transcript.Stat(p); err == nil {
			size = info.Size()
		}
		switch {
		case best == "",
			at.After(bestAt),
			at.Equal(bestAt) && size > bestSize,
			at.Equal(bestAt) && size == bestSize && p < best:
			best, bestAt, bestSize = p, at, size
		}
	}
	return best
}

// IsSessionTranscriptRel reports whether a projects-relative path is a session's
// own transcript rather than something nested beside it (a subagent transcript,
// a session's working directory).
func IsSessionTranscriptRel(rel string) bool {
	i := strings.Index(rel, "projects/")
	if i < 0 {
		return false
	}
	return strings.Count(rel[i+len("projects/"):], "/") == 1
}

// ProfileByAccount maps accountUuid → Desktop profile name, and reports whether
// the mapping is complete. Incomplete means at least one profile could not be
// linked to an account, so an unmatched session's profile is unknown rather than
// absent — a distinction [Reprofile] depends on.
func ProfileByAccount() (byAccount map[string]string, complete bool) {
	st, err := desktop.DefaultStore()
	if err != nil {
		return nil, false
	}
	profiles, err := st.List()
	if err != nil || len(profiles) == 0 {
		return nil, false
	}
	as, err := account.DefaultStore()
	if err != nil {
		return nil, false
	}
	accounts, err := as.List()
	if err != nil {
		return nil, false
	}
	uuidByID := map[string]string{}
	for _, a := range accounts {
		uuid := strings.ToLower(a.AccountUUID)
		if uuid == "" {
			// meta.json carries the uuid only for accounts captured after that
			// field existed. Without this fallback the map comes back empty for
			// older accounts, and does so silently.
			if raw, oerr := as.OAuth(a.ID); oerr == nil {
				// Lowercased like the field above it: the lookup this feeds is
				// keyed on a lowercased uuid, so a mixed-case one here would
				// leave exactly the older accounts this fallback exists for
				// still unresolved.
				uuid = strings.ToLower(account.ProfileAccountUUID(raw))
			}
		}
		if uuid != "" {
			uuidByID[a.ID] = uuid
		}
	}
	out := map[string]string{}
	complete = true
	for _, p := range profiles {
		uuid := uuidByID[p.AccountID]
		if uuid == "" {
			complete = false // unlinked profile: its sessions are unresolvable here
			continue
		}
		// Two profiles on one account would make the label a coin flip.
		if prev, dup := out[uuid]; dup && prev != p.Name {
			out[uuid] = ""
			continue
		}
		out[uuid] = p.Name
	}
	return out, complete
}

// Reprofile rewrites each session's Profile from the account its sidecar is
// filed under, which survives a sidecar being copied between profile trees in a
// way the tree it was found in does not.
func Reprofile(idx session.Index, byAccount map[string]string, complete bool) {
	if len(byAccount) == 0 {
		return
	}
	for id, m := range idx {
		if m.Account == "" {
			continue
		}
		name, known := byAccount[strings.ToLower(m.Account)]
		switch {
		case known:
			// Includes the empty name two profiles share: a label nobody can
			// justify is worse than none, since it decides where the user is sent.
			m.Profile = name
		case complete:
			m.Profile = ""
		default:
			continue
		}
		idx[session.CanonicalID(id)] = m
	}
}

// ResolvePath maps a path recorded on some other machine onto this one, leaving
// it untouched when no mapping applies.
func ResolvePath(me config.Machine, p string) string {
	if res := me.Resolver().Resolve(p); res.Path != "" {
		return res.Path
	}
	return p
}

// ClientLabel turns a transcript's entrypoint into the short client name shown
// to people: "vscode", "desktop", "cli", "sdk-*".
func ClientLabel(entrypoint string) string {
	return strings.TrimPrefix(entrypoint, "claude-")
}

// AccountLabels maps accountUuid → the name a person would recognise: the alias
// they chose, else the email. Attribution is recorded as a uuid and nothing
// else, so without this every account column reads as a hex string.
//
// Best-effort: an unreadable store yields no labels, which leaves the uuid
// showing rather than failing a listing over cosmetics.
func AccountLabels() map[string]string {
	out := map[string]string{}
	as, err := account.DefaultStore()
	if err != nil {
		return out
	}
	accounts, err := as.List()
	if err != nil {
		return out
	}
	for _, a := range accounts {
		uuid := strings.ToLower(a.AccountUUID)
		if uuid == "" {
			// Same fallback as ProfileByAccount: accounts captured before the
			// uuid was recorded still have one inside their stored credential.
			if raw, oerr := as.OAuth(a.ID); oerr == nil {
				uuid = strings.ToLower(account.ProfileAccountUUID(raw))
			}
		}
		if uuid == "" {
			continue
		}
		switch {
		case a.Alias != "":
			out[uuid] = a.Alias
		case a.Email != "":
			out[uuid] = a.Email
		}
	}
	return out
}
