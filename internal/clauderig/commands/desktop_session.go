package commands

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/clauderig/desktop"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
	"github.com/rigsmith/rigsmith/internal/clauderig/session"
)

// sessionUUID is the shape Claude Desktop requires of ?session=. It rejects
// anything else outright, so a reference that is not a uuid is treated as text
// to search for rather than passed through to fail there.
var sessionUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resumeDeepLink builds the URL Claude Desktop imports a CLI session from.
//
// Verified against Claude Desktop 2.x: the handler takes exactly one parameter,
// `session`, requires it to match a uuid, and calls importCliSession with it.
// Anything else is dropped with "missing or invalid session" and no visible
// effect, so the uuid check happens here rather than being discovered there.
func resumeDeepLink(sessionID string) string {
	return "claude://resume?session=" + url.QueryEscape(sessionID)
}

// sessionCandidate is one session `desktop open --session` could resume.
type sessionCandidate struct {
	ID    string
	Title string // sidecar title, else the transcript's first prompt
	Cwd   string
	Path  string // live transcript path
}

// label is the human description — title and project, no id. Callers that need
// the id add it, so it is never printed twice in the same line.
func (c sessionCandidate) label() string {
	t := c.Title
	if t == "" {
		t = "(no title)"
	}
	// Count and slice by runes: a title is arbitrary user text, and byte
	// slicing would cut a multi-byte character in half and emit mojibake.
	if r := []rune(t); len(r) > 58 {
		t = string(r[:57]) + "…"
	}
	if proj := c.project(); proj != "" {
		return fmt.Sprintf("%s  ·  %s", t, proj)
	}
	return t
}

// project names the session's directory, because a title alone rarely tells two
// sessions in different repos apart.
//
// The slug is deliberately NOT parsed for this. It is the cwd with separators
// swapped for dashes, which is lossy in exactly the case that matters here: in
// "-Users-john-Git-tweed-worktrees-grasp-lunar-cliff-claude" nothing marks
// which dashes were slashes, so the last segment reads "claude" for every
// worktree. The transcript records the real cwd, so read that instead.
func (c sessionCandidate) project() string {
	if c.Cwd != "" {
		return filepath.Base(c.Cwd)
	}
	return ""
}

// liveTranscripts indexes every transcript in the live CLI root by session id.
//
// The LIVE root specifically, not the synced repo: Desktop imports a session by
// reading the transcript off this machine's disk, so a session that exists only
// in the staging repo cannot be opened however well `search` can describe it.
// Checking here turns that into a clear message instead of a toast in the app.
func liveTranscripts(claudeHome string) map[string]string {
	out := map[string]string{}
	projects := filepath.Join(claudeHome, "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return out
	}
	for _, slug := range entries {
		if !slug.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projects, slug.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			// A session id can appear under two slugs (a worktree copy, or a slug
			// restore rewrote). Either transcript resumes the same session, so the
			// first is as good as the second.
			// Keyed lowercase so the lookup above can normalise to match, rather
			// than depending on the filesystem's casing being what the caller
			// happened to type.
			id = strings.ToLower(id)
			if _, seen := out[id]; !seen {
				out[id] = filepath.Join(projects, slug.Name(), name)
			}
		}
	}
	return out
}

// findSessions resolves a reference to the sessions it could mean.
//
// A uuid resolves to exactly itself — no search, no ambiguity. Anything else is
// matched against titles (Desktop sidecars) and, for the ~97% of sessions that
// have no sidecar, the transcript's first prompt, which is the same fallback
// title `search` shows. Matching is case-insensitive substring, like --cwd.
func findSessions(ref, claudeHome string, idx session.Index) []sessionCandidate {
	live := liveTranscripts(claudeHome)

	if sessionUUID.MatchString(ref) {
		// Normalise before looking up. sessionUUID accepts uppercase hex — as
		// Claude Desktop's own validator does — but both maps are keyed by the
		// on-disk transcript name, which Claude Code always writes lowercase.
		// Matching the ACCEPTED form against the STORED form is what turned a
		// pasted uppercase uuid into "no session matches" for a session that
		// was sitting right there: taking this branch also forgoes the
		// substring fallback that would otherwise have found it.
		id := strings.ToLower(ref)
		p, ok := live[id]
		if !ok {
			return nil
		}
		m := idx[id]
		return []sessionCandidate{{ID: id, Title: titleFor(m, p), Cwd: cwdFor(m, p), Path: p}}
	}

	needle := strings.ToLower(strings.TrimSpace(ref))
	// An empty needle is not a wildcard. strings.Contains matches "" against
	// everything, so without this a blank or whitespace-only reference resolves
	// to every session on the machine — and the command opens an arbitrary one.
	// The caller trims too; the invariant belongs here, where the matching is.
	if needle == "" {
		return nil
	}
	var out []sessionCandidate
	for id, p := range live {
		m := idx[id]
		title := titleFor(m, p)
		// Resolve the cwd ONCE and match on the same value that is displayed.
		// Matching m.Cwd instead would only ever search the ~3% of sessions with
		// a Desktop sidecar, while showing a project for all of them — so a
		// search by project name would silently miss most of what it lists.
		cwd := cwdFor(m, p)
		if !strings.Contains(strings.ToLower(title), needle) &&
			!strings.Contains(strings.ToLower(cwd), needle) {
			continue
		}
		out = append(out, sessionCandidate{ID: id, Title: title, Cwd: cwd, Path: p})
	}
	// Newest first, so the picker's top entry is the one most likely wanted.
	sort.Slice(out, func(i, j int) bool { return newer(out[i], out[j], idx) })
	return out
}

// cwdFor prefers the sidecar's cwd and falls back to the one the transcript
// itself recorded. Only matched candidates pay for the read, and matching has
// already opened every live transcript for its first prompt.
func cwdFor(m session.Meta, transcriptPath string) string {
	if m.Cwd != "" {
		return m.Cwd
	}
	if cwd, ok, err := project.CwdFromTranscript(transcriptPath); err == nil && ok {
		return cwd
	}
	return ""
}

func titleFor(m session.Meta, transcriptPath string) string {
	if m.Title != "" {
		return m.Title
	}
	return session.FirstPrompt(transcriptPath)
}

func newer(a, b sessionCandidate, idx session.Index) bool {
	at, bt := idx[a.ID].LastActivity, idx[b.ID].LastActivity
	if !at.Equal(bt) {
		return at.After(bt)
	}
	ai, _ := os.Stat(a.Path)
	bi, _ := os.Stat(b.Path)
	if ai == nil || bi == nil {
		return a.ID < b.ID
	}
	return ai.ModTime().After(bi.ModTime())
}

// pickSession narrows several matches to one: a picker on a terminal, and off
// one an error that lists them with ids, so the caller can re-run unambiguously
// rather than have a session chosen for them.
func pickSession(ref string, cands []sessionCandidate) (sessionCandidate, error) {
	switch len(cands) {
	case 0:
		return sessionCandidate{}, fmt.Errorf("no session on this machine matches %q\n"+
			"Only sessions whose transcript is in ~/.claude/projects can be opened — "+
			"`clauderig search %s` will say whether it lives only in the synced repo", ref, shQuote(ref))
	case 1:
		return cands[0], nil
	}
	if !interactive() {
		var b strings.Builder
		fmt.Fprintf(&b, "%d sessions match %q — re-run naming one of these ids\n", len(cands), ref)
		for i, c := range cands {
			if i == 12 {
				fmt.Fprintf(&b, "  … and %d more; narrow the text to see them\n", len(cands)-i)
				break
			}
			fmt.Fprintf(&b, "  %s  %s\n", shortID(c.ID), c.label())
		}
		return sessionCandidate{}, fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
	}
	opts := make([]huh.Option[string], 0, len(cands))
	byID := map[string]sessionCandidate{}
	for _, c := range cands {
		opts = append(opts, huh.NewOption(c.label()+"  ·  "+shortID(c.ID), c.ID))
		byID[c.ID] = c
	}
	choice := cands[0].ID
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("%d sessions match %q — open which?", len(cands), ref)).
			Options(opts...).Value(&choice),
	)).WithTheme(brand.Theme(brand.AccentClaude)).Run(); err != nil {
		return sessionCandidate{}, errCancelled
	}
	return byID[choice], nil
}

// otherRunningProfiles names the profiles running besides the target.
//
// This is the whole reason --session can refuse. A deep link is routed by
// SCHEME, not per instance: there is no per-instance address, and the profile
// flag that separates instances is a launch argument a URL cannot carry. With
// more than one instance up, the OS picks the recipient.
// otherRunningWindows names every Claude Desktop window that is NOT the target
// and could therefore receive a scheme-routed deep link.
//
// It asks the OS which processes exist rather than asking the store which
// profiles it knows. Five review rounds found five kinds of window the
// enumerate-the-profiles approach missed — unreadable metadata, a data dir
// outside clauderig's store, a store entry that is a directory symlink, the
// profile-less install, and a scan that failed — and each fix closed one and
// left the next. A process list has no such gaps: a window either exists or it
// does not.
//
// Names are recovered where possible, for the remedy; an unrecognised window is
// still counted, described by what is known about it.
func otherRunningWindows(app desktop.App, dirs map[string]string, target desktop.Profile) ([]string, error) {
	instances, err := app.Instances()
	if err != nil {
		return nil, fmt.Errorf("could not tell which Desktop windows are open: %w", err)
	}
	byDir := map[string]string{}
	for name, d := range dirs {
		byDir[canonicalDir(d)] = name
	}
	targetDir := canonicalDir(target.DataDir())

	seen := map[string]bool{}
	var others []string
	for _, inst := range instances {
		dir := canonicalDir(inst.DataDir)
		if dir == targetDir {
			continue
		}
		var label string
		switch {
		case inst.DataDir == "":
			label = defaultInstanceLabel
		case byDir[dir] != "":
			label = byDir[dir]
		default:
			// A profile clauderig does not manage. Still a competing window.
			label = fmt.Sprintf("a Desktop window on %s", inst.DataDir)
		}
		if !seen[label] {
			seen[label] = true
			others = append(others, label)
		}
	}
	sort.Strings(others)
	return others, nil
}

// canonicalDir normalises a data directory for comparison: symlinks resolved
// where possible, and case folded, so a store entry that is a directory symlink
// or a case-insensitive filesystem cannot make one window look like two.
func canonicalDir(dir string) string {
	if dir == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return strings.ToLower(filepath.Clean(dir))
}

// quittableByName reports whether `clauderig desktop quit <name>` would work for
// this label: a valid profile name, one the store can load, and one the shell
// will carry as a single argument. The routing scan is deliberately wider than
// that — it counts windows, including ones with no name at all — so the two
// questions are answered separately.
func quittableByName(st *desktop.Store, name string) bool {
	if st == nil || desktop.ValidName(name) != nil {
		return false
	}
	if strings.ContainsAny(name, " \t\"'"+"`"+`\\`) {
		return false
	}
	_, gerr := st.Get(name)
	return gerr == nil
}

// defaultInstanceLabel names the profile-less install in the refusal. It is not
// a profile name, so `desktop quit` cannot take it — the message says how to
// close it instead.
const defaultInstanceLabel = "the main Claude Desktop app"

// ambiguousRoutingError refuses to send a session while the OS gets to choose
// which window receives it.
//
// Observed, not theorised: asking for `brightshore` with `relatecpa` also open
// imported the session into relatecpa — the sidecar landed under relatecpa's
// accountUuid. That is a session crossing an ACCOUNT boundary, so the default
// is to refuse rather than warn and hope. --anyway sends it regardless, for the
// case where any window will do.
func ambiguousRoutingError(st *desktop.Store, target desktop.Profile, others []string) error {
	// The first line is rendered as the headline — capitalised, and given a
	// trailing period — so it must be one plain sentence that survives both, and
	// must not start with a profile name.
	// Only a real, resolvable profile can be quit by NAME. The scan deliberately
	// includes directories whose metadata will not parse, and a name may contain
	// a space — so emitting every candidate into a quit command produces
	// instructions that fail: `quit broken` cannot resolve, and `quit has space`
	// arrives as two arguments. Anything unquittable is named for manual
	// closing instead, alongside the profile-less app.
	var quittable, manual []string
	for _, o := range others {
		switch {
		case o == defaultInstanceLabel:
			manual = append(manual, o)
		case quittableByName(st, o):
			quittable = append(quittable, o)
		default:
			manual = append(manual, fmt.Sprintf("the %q profile window", o))
		}
	}
	hasDefault := len(manual) > 0
	var remedy string
	switch {
	case len(quittable) == 0:
		remedy = fmt.Sprintf("Close %s and re-run, or pass --anyway to send it to\n"+
			"whichever window the OS picks", strings.Join(manual, " and "))
	case !hasDefault:
		remedy = fmt.Sprintf("Quit the others (`clauderig desktop quit %s`) and re-run, or pass\n"+
			"--anyway to send it to whichever window the OS picks", strings.Join(quittable, " "))
	default:
		remedy = fmt.Sprintf("Quit the others (`clauderig desktop quit %s`), close %s by\n"+
			"hand, then re-run — or pass --anyway to send it to whichever window the OS picks",
			strings.Join(quittable, " "), strings.Join(manual, " and "))
	}
	return fmt.Errorf("another Claude Desktop window is open, so this session could be imported into the wrong account\n\n"+
		"%s is open alongside %s. A deep link is routed by scheme, not to a particular\n"+
		"window, so the OS decides which one receives it.\n\n%s",
		strings.Join(others, ", "), target.Name, remedy)
}

// competingWindows lists the Desktop windows other than target that could
// receive a scheme-routed deep link, failing closed on any discovery error.
func competingWindows(st *desktop.Store, app desktop.App, target desktop.Profile) ([]string, error) {
	dirs, lerr := st.CandidateDataDirs()
	if lerr != nil {
		return nil, fmt.Errorf("could not list Desktop profiles: %w\n"+
			"Sending now could import the session into the wrong account", lerr)
	}
	others, oerr := otherRunningWindows(app, dirs, target)
	if oerr != nil {
		return nil, fmt.Errorf("%w\nSending now could import the session into the wrong account", oerr)
	}
	return others, nil
}

// refuseIfRoutingIsAmbiguous stops the send while more than one window could
// receive it. One helper, used both before and after a launch: two copies of a
// safety rule are two chances for one of them to drift.
func refuseIfRoutingIsAmbiguous(st *desktop.Store, app desktop.App, target desktop.Profile, anyway bool) error {
	others, err := competingWindows(st, app, target)
	if err != nil {
		return err
	}
	if len(others) > 0 && !anyway {
		return ambiguousRoutingError(st, target, others)
	}
	return nil
}
