package hooks

import "fmt"

// CheckSyncRouting requires one standard command per owned sync event. Enabling
// local routing must not silently accept stale, duplicate or scoped producers.
// It does not edit settings; unrelated hooks remain the user's responsibility.
func CheckSyncRouting(path string) error {
	s, err := load(path)
	if err != nil {
		return err
	}
	if value, present := s["disableAllHooks"]; present {
		disabled, ok := value.(bool)
		if !ok {
			return fmt.Errorf("disableAllHooks must be a boolean when present")
		}
		if disabled {
			return fmt.Errorf("Claude hooks are disabled in settings")
		}
	}
	h, _ := s["hooks"].(map[string]any)
	for _, p := range SyncPlans() {
		groups, _ := h[p.Event].([]any)
		count := 0
		for _, raw := range groups {
			g, ok := raw.(map[string]any)
			if !ok || !hasMarker(g) {
				continue
			}
			matcherOK := true
			if value, present := g["matcher"]; present {
				matcher, ok := value.(string)
				matcherOK = ok && matcher == p.Matcher
			}
			if !matcherOK || !groupMatchesPlan(g, p) {
				return fmt.Errorf("%s hook differs from the standard command; run clauderig hooks install before enabling queued hooks", p.Event)
			}
			hs, _ := g["hooks"].([]any)
			for _, raw := range hs {
				command, _ := raw.(map[string]any)
				if command["command"] != p.Command {
					continue
				}
				count++
				if command["type"] != "command" {
					return fmt.Errorf("%s hook must have type command", p.Event)
				}
			}
		}
		if count != 1 {
			return fmt.Errorf("%s needs exactly one standard clauderig hook; install or repair hooks before enabling queued hooks", p.Event)
		}
	}
	return nil
}
