// Package hooks installs codexrig into Codex's own lifecycle, so a backup
// happens because you used Codex rather than because you remembered to.
//
// The file format was established by asking Codex rather than by reading
// documentation, and it is worth writing down because none of it is guessable:
//
//	{
//	  "description": "…",
//	  "hooks": {
//	    "SessionStart": [ { "hooks": [ { "type": "command", "command": "codexrig pull" } ] } ],
//	    "PreToolUse":   [ { "matcher": "Bash", "hooks": [ … ] } ]
//	  }
//	}
//
// The event names are PascalCase. Not camelCase, which is what the app-server
// protocol uses on the wire, and not snake_case, which is what Codex's own trust
// keys use — both were tried and both are silently IGNORED, because the parser
// does not reject unknown fields. A hook under the wrong key produces no error,
// no warning, and no hook, which is the worst failure a tool like this can have.
//
// Two more facts from the same source. `async: true` is parsed and then skipped
// with a warning ("async hooks are not supported yet" in 0.144.6), so nothing
// here sets it. And a hook is UNTRUSTED until its hash is recorded in
// config.toml — see Trust, which asks Codex for the hash rather than computing
// one, because the hash is Codex's opinion about a file, not ours.
package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Marker is the substring that identifies a hook as codexrig's. Ownership is
// structural rather than a comment: a group whose command names this binary is
// ours to update and to remove, and everything else in the file is somebody
// else's and is left exactly alone.
//
// It is "codexrig" rather than "rig", which would also match clauderig's
// commands and every rig binary on the machine.
const Marker = "codexrig"

// FileName is the hooks file's name within a scope.
const FileName = "hooks.json"

// Event is a Codex hook event, spelled the way the FILE spells it.
type Event string

const (
	PreToolUse        Event = "PreToolUse"
	PermissionRequest Event = "PermissionRequest"
	PostToolUse       Event = "PostToolUse"
	PreCompact        Event = "PreCompact"
	PostCompact       Event = "PostCompact"
	SessionStart      Event = "SessionStart"
	UserPromptSubmit  Event = "UserPromptSubmit"
	SubagentStart     Event = "SubagentStart"
	SubagentStop      Event = "SubagentStop"
	Stop              Event = "Stop"
)

// TrustKeyEvent maps an event to the snake_case spelling Codex uses inside its
// trust keys. The file and the trust key genuinely disagree about casing, and
// hard-coding one for both is how a trust entry ends up naming a hook that does
// not exist.
func TrustKeyEvent(e Event) string {
	var b strings.Builder
	for i, r := range string(e) {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Plan is one hook codexrig wants installed.
type Plan struct {
	Event   Event
	Matcher string
	Command string
}

// SyncPlans are the backup hooks: pull what another machine synced when a
// session starts, capture at the end of a turn, and flush the session's own
// rollout when it ends.
//
// Stop deliberately runs `sync --hook` rather than a bare `sync`. The two would
// be different plans for one event, which can never both be satisfied — an
// install writing one would report drift against the other forever. The flag
// also carries real meaning: it is what turns the debounce on, and Stop fires at
// the end of every turn in every open session.
func SyncPlans() []Plan {
	return []Plan{
		{Event: SessionStart, Command: "codexrig pull"},
		{Event: Stop, Command: "codexrig sync --hook"},
	}
}

// GuardPlans are the worktree-discipline hooks, installed per repository.
func GuardPlans(matcher string) []Plan {
	return []Plan{{Event: PreToolUse, Matcher: matcher, Command: "codexrig guard"}}
}

// document is the hooks.json shape. Unknown keys are preserved by decoding into
// a raw map rather than this struct — see load/save — so another tool's file is
// never reduced to what codexrig understands.
type handler struct {
	Type           string `json:"type"`
	Command        string `json:"command,omitempty"`
	CommandWindows string `json:"commandWindows,omitempty"`
	StatusMessage  string `json:"statusMessage,omitempty"`
}

type group struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []handler `json:"hooks"`
}

// Install adds any missing codexrig hooks and updates any that drifted. It
// returns what it added and what it changed.
func Install(path string, plans []Plan) (added, updated []string, err error) {
	doc, err := load(path)
	if err != nil {
		return nil, nil, err
	}
	hooksAny, _ := doc["hooks"].(map[string]any)
	if hooksAny == nil {
		hooksAny = map[string]any{}
	}

	for _, p := range plans {
		key := string(p.Event)
		raw, _ := hooksAny[key].([]any)
		mine := -1
		for i, g := range raw {
			if groupIsOurs(g) {
				mine = i
				break
			}
		}
		newGroup := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": p.Command}}}
		if p.Matcher != "" {
			newGroup["matcher"] = p.Matcher
		}
		if mine < 0 {
			hooksAny[key] = append(raw, newGroup)
			added = append(added, key)
			continue
		}
		if !sameGroup(raw[mine], newGroup) {
			raw[mine] = newGroup
			hooksAny[key] = raw
			updated = append(updated, key)
		}
	}
	if len(added) == 0 && len(updated) == 0 {
		return nil, nil, nil
	}
	doc["hooks"] = hooksAny
	if _, ok := doc["description"]; !ok {
		doc["description"] = "Managed by codexrig — run `codexrig hooks install` to update."
	}
	return added, updated, save(path, doc)
}

// Uninstall removes codexrig's hooks and nothing else.
func Uninstall(path string) ([]string, error) {
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
		for _, g := range raw {
			if groupIsOurs(g) {
				continue
			}
			kept = append(kept, g)
		}
		if len(kept) == len(raw) {
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

// Status reports which events carry a codexrig hook.
func Status(path string) ([]string, error) {
	doc, err := load(path)
	if err != nil {
		return nil, err
	}
	hooksAny, _ := doc["hooks"].(map[string]any)
	var present []string
	for key, v := range hooksAny {
		raw, _ := v.([]any)
		for _, g := range raw {
			if groupIsOurs(g) {
				present = append(present, key)
				break
			}
		}
	}
	sort.Strings(present)
	return present, nil
}

// Drift reports events whose installed codexrig hook no longer matches the plan.
//
// Presence is not health. An event with NO codexrig hook is not drift — that is
// Install's business — so a partial installation and a stale one are different
// problems with different fixes.
func Drift(path string, plans []Plan) ([]string, error) {
	doc, err := load(path)
	if err != nil {
		return nil, err
	}
	hooksAny, _ := doc["hooks"].(map[string]any)
	var drifted []string
	for _, p := range plans {
		raw, _ := hooksAny[string(p.Event)].([]any)
		for _, g := range raw {
			if !groupIsOurs(g) {
				continue
			}
			want := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": p.Command}}}
			if p.Matcher != "" {
				want["matcher"] = p.Matcher
			}
			if !sameGroup(g, want) {
				drifted = append(drifted, string(p.Event))
			}
			break
		}
	}
	sort.Strings(drifted)
	return drifted, nil
}

// groupIsOurs reports whether a group carries a command naming this tool.
func groupIsOurs(g any) bool {
	m, ok := g.(map[string]any)
	if !ok {
		return false
	}
	hs, _ := m["hooks"].([]any)
	for _, h := range hs {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, Marker) {
			return true
		}
	}
	return false
}

// sameGroup compares only what codexrig owns — the matcher and the command —
// so a field somebody added by hand does not read as drift and get overwritten
// on every run.
func sameGroup(a, b any) bool {
	am, ok1 := a.(map[string]any)
	bm, ok2 := b.(map[string]any)
	if !ok1 || !ok2 {
		return false
	}
	if fmt.Sprint(am["matcher"]) != fmt.Sprint(bm["matcher"]) {
		return false
	}
	return ourCommand(am) == ourCommand(bm)
}

func ourCommand(g map[string]any) string {
	hs, _ := g["hooks"].([]any)
	for _, h := range hs {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, Marker) {
			return cmd
		}
	}
	return ""
}

// load reads a hooks file into a raw map, so everything codexrig does not model
// survives the round trip. An absent or empty file is an empty document, not an
// error: installing into a machine that has never had hooks is the normal case.
func load(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(b)) == "" {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		// Refuse rather than replace. A file that does not parse is somebody's
		// work in progress, and overwriting it is the one unrecoverable thing
		// this package could do.
		return nil, fmt.Errorf("%s does not parse as JSON, so codexrig will not rewrite it: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func save(path string, doc map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	// Temp file and rename: an interrupted in-place write leaves a hooks.json
	// that load() refuses, and from then on every install, uninstall, status
	// and drift fails until a person repairs it by hand — the class of damage
	// the package comment on load() calls unrecoverable.
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
