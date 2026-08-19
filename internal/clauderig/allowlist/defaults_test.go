package allowlist

import "testing"

// The bundled-skills tree is vendor content the app refetches, and it dwarfs the
// session metadata around it — 446 of 465 files in one real Desktop root. It has
// to be pruned as a directory, not merely denied per file, or every sync would
// still walk 8 MB of it.
func TestDesktop_SkipsTheBundledSkillsTree(t *testing.T) {
	l := Desktop()
	for _, rel := range []string{
		"local-agent-mode-sessions/skills-plugin",
		"local-agent-mode-sessions/skills-plugin/a/b/skills/docx/SKILL.md",
		"local-agent-mode-sessions/skills-plugin/a/b/skills/xlsx/scripts/office/schemas/x.xsd",
	} {
		if l.Match(rel) {
			t.Errorf("Match(%q) = true, want the vendored skills tree excluded", rel)
		}
	}
	// Still reaching the real session metadata beside it.
	for _, rel := range []string{
		"local-agent-mode-sessions/acct/org/scheduled-tasks.json",
		"local-agent-mode-sessions/acct/org/local_abc.json",
	} {
		if !l.Match(rel) {
			t.Errorf("Match(%q) = false, want session metadata still synced", rel)
		}
	}
}
