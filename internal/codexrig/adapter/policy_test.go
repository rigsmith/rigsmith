package adapter

import "testing"

func TestCandidatePolicy(t *testing.T) {
	for rel, kind := range map[string]string{
		"config.toml": "config", "deep-review.config.toml": "config-profile",
		"A_2.config.toml": "config-profile", "AGENTS.md": "instructions",
		"AGENTS.override.md": "instructions", "hooks.json": "hooks",
		"rules/default.rules": "rules", "skills/writer/SKILL.md": "skill",
		"skills/writer/scripts/check.py": "skill",
	} {
		c, ok := Classify(CodexHome, rel)
		if !ok || c.Path != rel || c.Kind != kind || c.Requires == "" {
			t.Fatal(rel, c, ok)
		}
	}
	for _, root := range []RootKind{CodexHome, UserSkills, "unknown"} {
		for _, rel := range []string{"", ".", "../config.toml", "/config.toml", "a/../config.toml", "a//b", `skills\s\SKILL.md`, "skills/s/file:stream", "skills/s/line\nbreak", "skills/s/\xff"} {
			if _, ok := Classify(root, rel); ok {
				t.Fatal("invalid path accepted", root, rel)
			}
		}
	}
	for _, rel := range []string{
		"auth.json", "state_5.sqlite", "state_5.sqlite-wal", "history.jsonl", "session_index.jsonl",
		"sessions/2026/09/11/rollout-example.jsonl", "archived_sessions/rollout-example.jsonl",
		"automations/daily/automation.toml", "plugins/cache/vendor/plugin/skills/demo/SKILL.md",
		"memories/MEMORY.md", "shell_snapshots/shell.sh", "worktrees/a/AGENTS.md", "logs/cli.log",
		"config.toml/child", "rules/default.rules/child", "rules/deeper/default.rules",
		".config.toml", "bad.name.config.toml", "profile/config.toml", "AGENTS.MD", "skills/SKILL.md",
		"unknown.toml", "skills/.system/plugin/SKILL.md", "skills/demo/node_modules/deep/file",
	} {
		if c, ok := Classify(CodexHome, rel); ok {
			t.Fatal("excluded Codex state accepted", rel, c)
		}
	}
	for _, rel := range []string{"demo/SKILL.md", "demo/scripts/run.sh", "demo/references/topic.md"} {
		if c, ok := Classify(UserSkills, rel); !ok || c.Kind != "skill" {
			t.Fatal(rel, c, ok)
		}
	}
	for _, secret := range []string{"auth.json", "AUTH.JSON", "credentials.json", "CREDENTIALS.JSON", "local.KEY", "cert.pem", "state.sqlite-wal", "CACHE.DB", ".env", ".env.bak", ".git/config", "NODE_MODULES/package/source", ".system/tool/SKILL.md"} {
		for _, root := range []RootKind{CodexHome, UserSkills} {
			rel := "demo/" + secret
			if root == CodexHome {
				rel = "skills/" + rel
			}
			if _, ok := Classify(root, rel); ok {
				t.Fatal("sensitive or generated candidate", root, rel)
			}
		}
	}
}
