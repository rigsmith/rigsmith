package allowlist

import "testing"

// The list is the safety boundary, so the test that matters most is the one that
// names every file this tool must never publish and asserts it does not.

func TestTheCredentialNeverSyncs(t *testing.T) {
	for _, opts := range []Options{{}, {Sessions: true}} {
		l := CLI(opts)
		for _, rel := range []string{"auth.json", "auth.json.lock"} {
			if l.Match(rel) {
				t.Errorf("CLI(%+v) would sync %q — that file IS the login", opts, rel)
			}
		}
	}
}

func TestNoLiveDatabaseOrItsSidecarsSync(t *testing.T) {
	l := CLI(Options{Sessions: true})
	for _, rel := range []string{
		"state_5.sqlite", "state_5.sqlite-wal", "state_5.sqlite-shm",
		"thread_history_1.sqlite", "logs_2.sqlite", "memories_1.sqlite",
		"queue_1.sqlite", "goals_1.sqlite",
		"sqlite/state_5.sqlite", "sqlite/codex-dev.db",
	} {
		if l.Match(rel) {
			t.Errorf("CLI would sync %q — a file-level copy of a hot database is a corrupt one", rel)
		}
	}
}

func TestTheConfigAndWhatYouTaughtCodexDoSync(t *testing.T) {
	l := CLI(Options{})
	for _, rel := range []string{
		"config.toml",
		"work.config.toml",
		"AGENTS.md",
		"skills/use-railway/SKILL.md",
		"skills/use-railway/references/api.md",
		"rules/default.rules",
		"prompts/review.md",
	} {
		if !l.Match(rel) {
			t.Errorf("CLI would NOT sync %q, which is exactly what this tool is for", rel)
		}
	}
}

func TestCodexOwnSystemSkillsAreCarvedOutOfTheSkillsInclude(t *testing.T) {
	// The case the longest-match rule exists for: a narrow exclude inside a
	// broad include. Carrying these would restore one release's copies on top
	// of another's.
	l := CLI(Options{})
	if !l.Match("skills/mine/SKILL.md") {
		t.Error("a user skill must sync")
	}
	if l.Match("skills/.system/review-agent/SKILL.md") {
		t.Error("Codex's own system skills must not sync")
	}
}

func TestSessionsAreOptIn(t *testing.T) {
	rollout := "sessions/2026/09/05/rollout-2026-09-05T11-22-59-01a0722a.jsonl"
	if CLI(Options{}).Match(rollout) {
		t.Error("rollouts must not sync until the user asks for them — they are backup, not portability")
	}
	if !CLI(Options{Sessions: true}).Match(rollout) {
		t.Error("with sessions on, a rollout must sync")
	}
	if !CLI(Options{Sessions: true}).Match("session_index.jsonl") {
		t.Error("the thread-name index should travel with the rollouts it names")
	}
}

func TestTheBigRegenerableTreesAreNeverEntered(t *testing.T) {
	// Pruning is what keeps a walk off ~400 MB of plugin runtimes and caches.
	// Descend is checked rather than Match because the guarantee is that the
	// directory is never ENTERED, and asserting on the files inside would need
	// them to exist.
	l := CLI(Options{Sessions: true})
	for _, dir := range []string{"plugins", "cache", "computer-use", "log", ".tmp", "tmp", "sqlite", "worktrees", "attachments", "automations"} {
		if l.Descend(dir) {
			t.Errorf("the walk would descend into %q", dir)
		}
	}
}

func TestMachineLocalStateDoesNotTravel(t *testing.T) {
	l := CLI(Options{Sessions: true})
	for _, rel := range []string{
		"installation_id",
		"models_cache.json",
		"hooks.json",
		"review-state-336.json",
		".codex-global-state.json",
		"external_agent_session_imports.json",
		"shell_snapshots/019ee53d.1234.sh",
		"thread-writer-locks/019ee53d.lock",
		"ipc/ipc.sock",
	} {
		if l.Match(rel) {
			t.Errorf("CLI would sync %q, which means something different on every machine", rel)
		}
	}
}

func TestAnUnknownRootSyncsNothing(t *testing.T) {
	// Default-deny applies to roots as well as to files: a root nobody wrote a
	// policy for is not a licence to walk it.
	if For("desktop", Options{Sessions: true}).Match("config.toml") {
		t.Error("an unknown root must sync nothing")
	}
}
