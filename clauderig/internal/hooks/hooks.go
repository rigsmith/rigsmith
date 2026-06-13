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
// install`): SessionStart pulls, Stop pushes. Bare `clauderig` keeps them
// portable — each machine resolves it on PATH.
func SyncPlans() []Plan {
	return []Plan{
		{Event: "SessionStart", Command: "clauderig pull"},
		{Event: "Stop", Command: "clauderig sync"},
	}
}

// GuardPlans enforce worktree/PR discipline and belong at repo scope (`clauderig
// project|local install`): the PreToolUse guard runs on the tool calls that can
// move the session dir or write code to a base branch.
func GuardPlans() []Plan {
	return []Plan{
		{Event: "PreToolUse", Matcher: "Edit|Write|NotebookEdit|Bash|EnterWorktree|ExitWorktree", Command: "clauderig guard"},
	}
}

// DefaultPlans is every plan clauderig knows (used where the full set is wanted).
func DefaultPlans() []Plan {
	return append(SyncPlans(), GuardPlans()...)
}

// Install adds the given plans to the settings.json at path (created if absent),
// idempotently — an event already carrying a clauderig hook is left alone. Other
// settings and other hooks are preserved. Returns the events newly added.
func Install(path string, plans []Plan) (added []string, err error) {
	s, err := load(path)
	if err != nil {
		return nil, err
	}
	h := hooksMap(s)
	for _, p := range plans {
		raw, exists := h[p.Event]
		groups, ok := raw.([]any)
		if exists && !ok {
			continue // unexpected shape (malformed / future schema) — don't clobber it
		}
		if anyHasMarker(groups) {
			continue
		}
		group := map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": p.Command}},
		}
		if p.Matcher != "" {
			group["matcher"] = p.Matcher
		}
		groups = append(groups, group)
		h[p.Event] = groups
		added = append(added, p.Event)
	}
	if len(added) == 0 {
		return added, nil
	}
	return added, save(path, s)
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
