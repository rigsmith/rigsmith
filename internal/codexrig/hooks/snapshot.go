package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
)

type fileHook struct {
	info           appserver.HookInfo
	event          Event
	group, handler int
}

type snapshot struct {
	scope    Scope
	raw      []byte
	resolved string
	hooks    []fileHook
	err      error
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

func readSnapshot(scope Scope) snapshot {
	s := snapshot{scope: scope}
	s.resolved, s.err = canonicalHookPath(scope.path)
	if s.err != nil {
		return s
	}
	s.raw, s.err = os.ReadFile(scope.path)
	if os.IsNotExist(s.err) {
		s.err = nil
		return s
	}
	if s.err != nil {
		return s
	}
	var doc map[string]any
	if err := json.Unmarshal(s.raw, &doc); err != nil {
		s.err = fmt.Errorf("hooks file is invalid JSON: %w", err)
		return s
	}
	rootIssue := fieldIssue(doc, "description", "hooks")
	if err := scope.Validate(); err != nil {
		rootIssue = err.Error()
	}
	hooks, _ := doc["hooks"].(map[string]any)
	var events []string
	for event := range hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	for _, event := range events {
		groups, _ := hooks[event].([]any)
		for gi, rawGroup := range groups {
			group, _ := rawGroup.(map[string]any)
			matcher, _ := group["matcher"].(string)
			groupIssue := fieldIssue(group, "matcher", "hooks")
			if value, ok := group["matcher"]; ok {
				if _, ok := value.(string); !ok {
					groupIssue = "matcher must be a string"
				}
			}
			handlers, _ := group["hooks"].([]any)
			for hi, rawHandler := range handlers {
				handler, _ := rawHandler.(map[string]any)
				command, _ := handler["command"].(string)
				windows, _ := handler["commandWindows"].(string)
				typ, _ := handler["type"].(string)
				issue := fieldIssue(handler, "type", "command")
				if groupIssue != "" {
					issue = groupIssue
				}
				if rootIssue != "" {
					issue = rootIssue
				}
				validEvent := false
				for _, plan := range scope.Plans() {
					if event == string(plan.Event) {
						validEvent = true
						if _, present := group["matcher"]; present && plan.Matcher == "" && issue == "" {
							issue = "unsupported field: matcher (this scope's installer omits it)"
						}
					}
				}
				if !validEvent && issue == "" {
					issue = "event is not installed in this scope (file events are case-sensitive): " + event
				}
				info := appserver.HookInfo{Key: scope.path + ":" + TrustKeyEvent(Event(event)) + ":" + strconv.Itoa(gi) + ":" + strconv.Itoa(hi), SourcePath: scope.path, EventName: wireEvent(Event(event)), Command: command, HandlerType: typ, Matcher: matcher, Enabled: true, ValidationIssue: issue}
				info.CodexrigCandidate = strings.Contains(command, Marker) || strings.Contains(windows, Marker)
				// An alternate-only command can still be a marker-bearing candidate.
				if command == "" && windows != "" {
					info.Command = windows
				}
				s.hooks = append(s.hooks, fileHook{info: info, event: Event(event), group: gi, handler: hi})
			}
		}
	}
	return s
}

func (s snapshot) unchanged() bool {
	resolved, err := canonicalHookPath(s.scope.path)
	if err != nil || resolved != s.resolved || s.scope.Validate() != nil {
		return false
	}
	raw, err := os.ReadFile(s.scope.path)
	if os.IsNotExist(err) && len(s.raw) == 0 {
		return true
	}
	return err == nil && bytes.Equal(raw, s.raw)
}

func (s snapshot) locate(h appserver.HookInfo) (int, string) {
	if s.err != nil {
		return -1, s.err.Error()
	}
	// Split from the right: Windows source paths themselves contain colons.
	parts := strings.Split(h.Key, ":")
	if len(parts) < 4 {
		return -1, "Codex trust key is missing its event/handler indices"
	}
	gi, gerr := strconv.Atoi(parts[len(parts)-2])
	hi, herr := strconv.Atoi(parts[len(parts)-1])
	if gerr != nil || herr != nil {
		return -1, "Codex trust key has invalid handler indices"
	}
	keySource := strings.Join(parts[:len(parts)-3], ":")
	if !s.scope.addresses(keySource) {
		return -1, "Codex trust key names a different source file"
	}
	for i, local := range s.hooks {
		if local.group != gi || local.handler != hi || TrustKeyEvent(local.event) != parts[len(parts)-3] {
			continue
		}
		if local.info.EventName != h.EventName || local.info.Command != h.Command || local.info.Matcher != h.Matcher || local.info.HandlerType != h.HandlerType {
			return i, "Codex listing disagrees with the hook file; retry after reviewing it"
		}
		return i, local.info.ValidationIssue
	}
	return -1, "Codex listed a handler absent from the hook file; retry after reviewing it"
}

func (s snapshot) validate(h appserver.HookInfo) (int, string) {
	i, issue := s.locate(h)
	if err := s.scope.Validate(); err != nil {
		return i, err.Error()
	}
	if issue != "" {
		return i, issue
	}
	if !s.unchanged() {
		return i, "hooks file or symlink changed during validation; review and retry"
	}
	if len(h.UnknownFields) > 0 {
		return i, "unsupported reported field(s): " + strings.Join(h.UnknownFields, ", ")
	}
	if h.Async {
		return i, "unsupported reported field: async"
	}
	if h.StatusMessage != nil {
		return i, "unsupported reported field: statusMessage"
	}
	if h.TimeoutSec != nil && *h.TimeoutSec != 600 {
		return i, "unsupported reported field: timeoutSec"
	}
	if h.AdditionalContextLimit != nil {
		return i, "unsupported reported field: additionalContextLimit"
	}
	if !Matches(h, s.scope) {
		return i, "hook does not match this scope's complete installer plan"
	}
	return i, ""
}
