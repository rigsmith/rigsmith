// Package hooks installs clauderig into Claude Code's settings.json hooks so sync
// runs automatically: SessionStart pulls the latest into the staging repo, Stop
// pushes this session's changes. The hook command is the bare `clauderig` (relies
// on PATH), not an absolute path, so it stays correct when settings.json itself
// syncs to another machine — the self-bootstrapping property.
package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/guard"
)

// Marker identifies diagnostic candidates only; it never authorizes edits.
const Marker = "clauderig"

// Plan is one event→command hook clauderig installs. Matcher, when set, scopes
// the hook to matching tool names (PreToolUse/PostToolUse only).
type Plan struct {
	Event   string
	Matcher string
	Command string
}

// SyncPlans keep ~/.claude in sync and belong at user scope (`clauderig hooks
// install`): SessionStart pulls, Stop pushes, and SessionEnd pushes once more
// with the ending session's transcript flushed — Stop fires after each turn,
// so sync throttles a large transcript there, and the session ending is the
// one moment its last turn must not wait for the next session. The hook's
// stdin payload names the transcript, so other sessions' transcripts keep
// their throttle. Bare `clauderig` keeps them portable — each machine
// resolves it on PATH.
func SyncPlans() []Plan {
	return []Plan{
		{Event: "SessionStart", Command: "clauderig pull"},
		// --hook, not a bare sync: Stop fires at the end of every turn in every
		// open chat, so it debounces and takes a lock rather than walking the
		// whole tree several times a minute. A sync typed by hand still runs
		// immediately. This REPLACES the bare `clauderig sync` that used to be
		// here — two plans for one event can never both be satisfied, and an
		// install that writes one of them reports drift against the other for
		// ever after.
		{Event: "Stop", Command: "clauderig sync --hook"},
		{Event: "SessionEnd", Command: "clauderig sync --flush"},
	}
}

// GuardPlans enforce worktree/PR discipline and belong at repo scope (`clauderig
// project|local install`): the PreToolUse guard runs on the tool calls that can
// move the session dir or write code to a base branch.
func GuardPlans() []Plan {
	return []Plan{
		// Derived from the guard's own registry, never restated: this matcher is
		// what the hook fires on, so a tool the guard handles but the matcher
		// omits is not guarded at all. That is exactly how Monitor — which runs
		// shell commands in the same environment as Bash — went unguarded.
		{Event: "PreToolUse", Matcher: strings.Join(guard.Tools(), "|"), Command: "clauderig guard"},
	}
}

// DefaultPlans is every plan clauderig knows (used where the full set is wanted).
func DefaultPlans() []Plan {
	return append(SyncPlans(), GuardPlans()...)
}

// Install adds the given plans to the settings.json at path (created if absent).
// Other settings and other hooks are preserved. Returns the events newly added.
//
// A clauderig hook that is already installed is brought up to date rather than
// left alone. That matters because the PreToolUse matcher is the list of tools
// the guard even runs for: when Claude Code ships a new command-bearing tool and
// clauderig adds it to the plan, "already installed, skip" would mean every
// existing machine keeps the old list forever and stays unguarded for that tool,
// while the release notes say otherwise. Only the fields clauderig owns
// (matcher, command) are migrated only for exact current or known historical plans.
func Install(path string, plans []Plan) (added []string, err error) {
	defer func() { err = DisplayError(err) }()
	added, _, err = install(path, plans)
	return added, err
}

// InstallOrUpdate is Install, also reporting the events whose existing clauderig
// hook it had to correct — so `doctor` can say what it repaired.
func InstallOrUpdate(path string, plans []Plan) (added, updated []string, err error) {
	a, u, err := install(path, plans)
	return a, u, DisplayError(err)
}

func install(path string, plans []Plan) (added, updated []string, err error) {
	doc, err := load(path)
	if err != nil {
		return nil, nil, err
	}
	added, updated = prepareInstall(doc, plans)
	if len(added)+len(updated) == 0 {
		return nil, nil, nil
	}
	return added, updated, save(path, doc)
}

func knownCommand(event string, command string, plan Plan) bool {
	return command == plan.Command || (event == "Stop" && plan.Command == "clauderig sync --hook" && command == "clauderig sync")
}

func matchesPlanMatcher(g map[string]any, p Plan) bool {
	matcher, present := g["matcher"]
	return (p.Matcher == "" && !present) || (p.Matcher != "" && matcher == p.Matcher)
}

func prepareInstall(doc map[string]any, plans []Plan) (added, updated []string) {
	hooksAny, _ := doc["hooks"].(map[string]any)
	if hooksAny == nil {
		hooksAny = map[string]any{}
	}
	for _, p := range plans {
		raw, _ := hooksAny[p.Event].([]any)
		var kept []any
		exact, changed := false, false
		// Find current handlers first, including ones after a legacy sibling.
		for _, rawGroup := range raw {
			g, _ := rawGroup.(map[string]any)
			if !matchesPlanMatcher(g, p) || fieldIssue(g, "matcher", "hooks") != "" {
				continue
			}
			hs, _ := g["hooks"].([]any)
			for _, rawHandler := range hs {
				h, _ := rawHandler.(map[string]any)
				if h["type"] == "command" && h["command"] == p.Command && fieldIssue(h, "type", "command") == "" {
					exact = true
				}
			}
		}
		for _, rawGroup := range raw {
			g, _ := rawGroup.(map[string]any)
			hs, _ := g["hooks"].([]any)
			matcherExact := matchesPlanMatcher(g, p)
			var handlers []any
			for _, rawHandler := range hs {
				h, _ := rawHandler.(map[string]any)
				command, _ := h["command"].(string)
				// Unknown fields and lookalikes are somebody else's work, even when
				// they share a group with a command we recognize.
				repairable := h["type"] == "command" && fieldIssue(h, "type", "command") == "" && fieldIssue(g, "matcher", "hooks") == "" && knownCommand(p.Event, command, p) && knownMatcher(g, p)
				if repairable && matcherExact && command == p.Command {
					exact = true
					handlers = append(handlers, rawHandler)
				} else if repairable && !exact && (matcherExact || len(hs) == 1) {
					h["command"] = p.Command
					if p.Matcher == "" {
						delete(g, "matcher")
					} else {
						g["matcher"] = p.Matcher
					}
					handlers = append(handlers, h)
					exact = true
					changed = true
				} else if repairable {
					changed = true
				} else {
					handlers = append(handlers, rawHandler)
				}
			}
			if len(handlers) == len(hs) {
				kept = append(kept, rawGroup)
			} else if len(handlers) > 0 {
				g["hooks"] = handlers
				kept = append(kept, g)
			}
		}
		if !exact {
			g := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": p.Command}}}
			if p.Matcher != "" {
				g["matcher"] = p.Matcher
			}
			kept = append(kept, g)
			if !changed {
				added = append(added, p.Event)
			}
		}
		if changed {
			updated = append(updated, p.Event)
		}
		hooksAny[p.Event] = kept
	}
	doc["hooks"] = hooksAny
	return added, updated
}

// Drift reports the events whose installed clauderig hook no longer matches the
// plan — a settings.json written by an older release.
//
// Presence is not health here. The PreToolUse matcher decides which tools the
// guard runs for at all, so a hook that is present but carries last release's
// matcher is silently not guarding whatever was added since. Events with no
// clauderig hook at all are NOT drift; that is `Install`'s business.
func Drift(path string, plans []Plan) (events []string, err error) {
	s, err := load(path)
	if err != nil {
		return nil, err
	}
	h, ok := s["hooks"].(map[string]any)
	if !ok {
		return nil, nil
	}
	for _, p := range plans {
		groups, ok := h[p.Event].([]any)
		if !ok {
			continue
		}
		for _, raw := range groups {
			g, ok := raw.(map[string]any)
			if !ok || !ownedGroup(g, p) {
				continue
			}
			if !groupMatchesPlan(g, p) {
				events = append(events, p.Event)
				break
			}
		}
	}
	return events, nil
}

func groupMatchesPlan(g map[string]any, p Plan) bool {
	// Compared in both directions, matching newGroup, which omits the field when
	// the plan has no matcher: an installed hook that is still scoped when the
	// plan says it should not be is drift too.
	if cur, _ := g["matcher"].(string); cur != p.Matcher {
		return false
	}
	hs, ok := g["hooks"].([]any)
	if !ok {
		return false
	}
	for _, hraw := range hs {
		hm, ok := hraw.(map[string]any)
		if !ok {
			continue
		}
		if cur, _ := hm["command"].(string); owned(g, hm, p) && cur != p.Command {
			return false
		}
	}
	return true
}

// Uninstall removes clauderig-owned hooks, leaving other hooks and settings
// intact. Returns the events from which a hook was removed.
func Uninstall(path string) ([]string, error) {
	r, e := uninstall(path, DefaultPlans())
	return r, DisplayError(e)
}

func uninstall(path string, plans []Plan) ([]string, error) {
	doc, err := load(path)
	if err != nil {
		return nil, err
	}
	hooksAny, _ := doc["hooks"].(map[string]any)
	if hooksAny == nil {
		return nil, nil
	}
	var removed []string
	for key, v := range hooksAny {
		raw, _ := v.([]any)
		kept := make([]any, 0, len(raw))
		changed := false
		for _, rawGroup := range raw {
			g, _ := rawGroup.(map[string]any)
			hs, _ := g["hooks"].([]any)
			var handlers []any
			for _, rawHandler := range hs {
				h, _ := rawHandler.(map[string]any)
				remove := false
				for _, plan := range plans {
					if plan.Event == key && owned(g, h, plan) && h["type"] == "command" && fieldIssue(h, "type", "command") == "" && fieldIssue(g, "matcher", "hooks") == "" {
						remove = true
					}
				}
				if remove {
					changed = true
				} else {
					handlers = append(handlers, rawHandler)
				}
			}
			if len(handlers) == len(hs) {
				kept = append(kept, rawGroup)
			} else if len(handlers) > 0 {
				g["hooks"] = handlers
				kept = append(kept, g)
			}
		}
		if !changed {
			continue
		}
		removed = append(removed, key)
		if len(kept) == 0 {
			// Leave no empty array behind: another tool reading this file
			// should see the event as absent, not as present-and-empty.
			delete(hooksAny, key)
			continue
		}
		hooksAny[key] = kept
	}
	if len(removed) == 0 {
		return nil, nil
	}
	sort.Strings(removed)
	doc["hooks"] = hooksAny
	return removed, save(path, doc)
}

// Status reports which events currently carry a clauderig hook.
func Status(path string) (present []string, err error) {
	s, err := load(path)
	if err != nil {
		return nil, err
	}
	h, ok := s["hooks"].(map[string]any)
	if !ok {
		return nil, nil
	}
	for event, v := range h {
		if groups, ok := v.([]any); ok && ownedGroups(groups, event) {
			present = append(present, event)
		}
	}
	return present, nil
}

func load(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, DisplayError(err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	return decodeDocument(b)
}

func decodeDocument(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("hooks document must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("hooks document contains trailing JSON")
	}
	if value, present := doc["hooks"]; present {
		events, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("hooks must be an object")
		}
		for event, value := range events {
			groups, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("hooks.%s must be an array", Display(event))
			}
			for _, value := range groups {
				group, ok := value.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("hook group must be an object")
				}
				handlers, ok := group["hooks"].([]any)
				if !ok {
					return nil, fmt.Errorf("group hooks must be an array")
				}
				for _, value := range handlers {
					if _, ok := value.(map[string]any); !ok {
						return nil, fmt.Errorf("hook handler must be an object")
					}
				}
			}
		}
	}
	return doc, nil
}

func save(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func hasMarker(group any) bool {
	g, ok := group.(map[string]any)
	if !ok {
		return false
	}
	hs, ok := g["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hs {
		if hm, ok := h.(map[string]any); ok {
			if c, ok := hm["command"].(string); ok && strings.Contains(c, Marker) {
				return true
			}
		}
	}
	return false
}

// Only installer-shaped handlers with a documented matcher can be migrated.
func owned(g, h map[string]any, p Plan) bool {
	command, _ := h["command"].(string)
	return h["type"] == "command" && fieldIssue(h, "type", "command") == "" && fieldIssue(g, "matcher", "hooks") == "" && knownCommand(p.Event, command, p) && knownMatcher(g, p)
}
func knownMatcher(g map[string]any, p Plan) bool {
	if matchesPlanMatcher(g, p) {
		return true
	}
	if p.Event != "PreToolUse" {
		return false
	}
	m, _ := g["matcher"].(string)
	return m == "Edit|Write|NotebookEdit|Bash|EnterWorktree|ExitWorktree" || m == "Edit|Write|NotebookEdit|Bash|Monitor|EnterWorktree|ExitWorktree"
}
func ownedGroup(g map[string]any, p Plan) bool {
	hs, _ := g["hooks"].([]any)
	for _, v := range hs {
		h, _ := v.(map[string]any)
		if owned(g, h, p) {
			return true
		}
	}
	return false
}
func ownedGroups(groups []any, event string) bool {
	for _, p := range DefaultPlans() {
		if p.Event == event {
			for _, v := range groups {
				g, _ := v.(map[string]any)
				if ownedGroup(g, p) {
					return true
				}
			}
		}
	}
	return false
}

func fieldIssue(m map[string]any, allowed ...string) string {
	var unknown []string
	for key := range m {
		ok := false
		for _, want := range allowed {
			if key == want {
				ok = true
				break
			}
		}
		if !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return "unsupported field(s): " + strings.Join(unknown, ", ")
	}
	return ""
}

// Candidates reports marker-bearing handlers without granting ownership.
func Candidates(path string) string {
	doc, err := load(path)
	if err != nil {
		return Display(err.Error())
	}
	events, _ := doc["hooks"].(map[string]any)
	var out []string
	for event, v := range events {
		groups, _ := v.([]any)
		for _, v := range groups {
			g, _ := v.(map[string]any)
			hs, _ := g["hooks"].([]any)
			for _, v := range hs {
				h, _ := v.(map[string]any)
				cmd, _ := h["command"].(string)
				if !strings.Contains(cmd, Marker) {
					continue
				}
				exact := false
				for _, p := range DefaultPlans() {
					if p.Event == event && owned(g, h, p) && matchesPlanMatcher(g, p) && cmd == p.Command {
						exact = true
					}
				}
				if !exact {
					out = append(out, fmt.Sprintf("%s: event=%s matcher=%s command=%s", candidateKind(g, h, event), Display(event), Display(fmt.Sprint(g["matcher"])), Display(cmd)))
				}
			}
		}
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

func candidateKind(g, h map[string]any, event string) string {
	for _, p := range DefaultPlans() {
		if p.Event == event && owned(g, h, p) {
			return "known stale hook"
		}
	}
	return "lookalike (left untouched)"
}
