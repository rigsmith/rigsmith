// Package hooks installs clauderig into Claude Code's settings.json hooks so sync
// runs automatically: SessionStart pulls the latest into the staging repo, Stop
// pushes this session's changes. The hook command is the bare `clauderig` (relies
// on PATH), not an absolute path, so it stays correct when settings.json itself
// syncs to another machine — the self-bootstrapping property.
package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/clauderig/guard"
)

// Marker identifies a clauderig-owned hook (its command contains this).
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
// with every deferred transcript flushed — Stop fires after each turn, so
// sync throttles a large transcript there, and the session ending is the
// one moment its last turn must not wait for the next session. Bare
// `clauderig` keeps them portable — each machine resolves it on PATH.
func SyncPlans() []Plan {
	return []Plan{
		{Event: "SessionStart", Command: "clauderig pull"},
		{Event: "Stop", Command: "clauderig sync"},
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
// (matcher, command) are rewritten, on the group carrying its marker.
func Install(path string, plans []Plan) (added []string, err error) {
	added, _, err = install(path, plans)
	return added, err
}

// InstallOrUpdate is Install, also reporting the events whose existing clauderig
// hook it had to correct — so `doctor` can say what it repaired.
func InstallOrUpdate(path string, plans []Plan) (added, updated []string, err error) {
	return install(path, plans)
}

func install(path string, plans []Plan) (added, updated []string, err error) {
	s, err := load(path)
	if err != nil {
		return nil, nil, err
	}
	h := hooksMap(s)
	for _, p := range plans {
		raw, exists := h[p.Event]
		groups, ok := raw.([]any)
		if exists && !ok {
			continue // unexpected shape (malformed / future schema) — don't clobber it
		}
		if anyHasMarker(groups) {
			if reconcileGroups(groups, p) {
				h[p.Event] = groups
				updated = append(updated, p.Event)
			}
			continue
		}
		groups = append(groups, newGroup(p))
		h[p.Event] = groups
		added = append(added, p.Event)
	}
	if len(added) == 0 && len(updated) == 0 {
		return added, updated, nil
	}
	return added, updated, save(path, s)
}

func newGroup(p Plan) map[string]any {
	group := map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": p.Command}},
	}
	if p.Matcher != "" {
		group["matcher"] = p.Matcher
	}
	return group
}

// reconcileGroups brings the clauderig-owned group in groups up to plan p,
// reporting whether anything changed. Groups clauderig does not own are left
// exactly as they are — this must never edit someone else's hook.
func reconcileGroups(groups []any, p Plan) (changed bool) {
	for _, raw := range groups {
		g, ok := raw.(map[string]any)
		if !ok || !hasMarker(g) {
			continue
		}
		// Both directions: a plan that drops the matcher has to remove the field,
		// or the hook stays scoped to the old tool list and may never fire — with
		// Install reporting success.
		if cur, had := g["matcher"].(string); cur != p.Matcher || (had && p.Matcher == "") {
			if p.Matcher == "" {
				delete(g, "matcher")
			} else {
				g["matcher"] = p.Matcher
			}
			changed = true
		}
		hs, ok := g["hooks"].([]any)
		if !ok {
			continue
		}
		for _, hraw := range hs {
			hm, ok := hraw.(map[string]any)
			if !ok {
				continue
			}
			cur, _ := hm["command"].(string)
			if !strings.Contains(cur, Marker) || cur == p.Command {
				continue
			}
			hm["command"] = p.Command
			changed = true
		}
	}
	return changed
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
		if !ok || !anyHasMarker(groups) {
			continue
		}
		for _, raw := range groups {
			g, ok := raw.(map[string]any)
			if !ok || !hasMarker(g) {
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
		if cur, _ := hm["command"].(string); strings.Contains(cur, Marker) && cur != p.Command {
			return false
		}
	}
	return true
}

// Uninstall removes clauderig-owned hooks, leaving other hooks and settings
// intact. Returns the events from which a hook was removed.
func Uninstall(path string) (removed []string, err error) {
	s, err := load(path)
	if err != nil {
		return nil, err
	}
	h, ok := s["hooks"].(map[string]any)
	if !ok {
		return nil, nil
	}
	for event, v := range h {
		groups, ok := v.([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(groups))
		changed := false
		for _, g := range groups {
			if hasMarker(g) {
				changed = true
				continue
			}
			kept = append(kept, g)
		}
		if changed {
			removed = append(removed, event)
			if len(kept) == 0 {
				delete(h, event)
			} else {
				h[event] = kept
			}
		}
	}
	if len(removed) == 0 {
		return removed, nil
	}
	return removed, save(path, s)
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
		if groups, ok := v.([]any); ok && anyHasMarker(groups) {
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
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
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

func hooksMap(settings map[string]any) map[string]any {
	if h, ok := settings["hooks"].(map[string]any); ok {
		return h
	}
	h := map[string]any{}
	settings["hooks"] = h
	return h
}

func anyHasMarker(groups []any) bool {
	for _, g := range groups {
		if hasMarker(g) {
			return true
		}
	}
	return false
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
