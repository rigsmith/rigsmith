package allowlist

// CLI is the allowlist for the ~/.claude root. Mirrors the design doc's table:
// config + skills + plans + commands/agents + the marketplaces/data plugin config
// + the project transcripts (retention is applied separately by mtime, not here).
// Everything else — caches, statsig, sessions registry, shell snapshots, locks,
// telemetry, file-history, credentials — is denied by default.
func CLI() List {
	return List{Rules: []Rule{
		inc("settings.json"),
		inc("settings.local.json"),
		inc("CLAUDE.md"),
		inc("skills"),
		inc("plans"),
		inc("commands"),
		inc("agents"),
		inc("plugins/marketplaces"),
		inc("plugins/data"),
		inc("projects"),
		// Defensive carve-outs in case Claude Code drops machine-local files inside
		// an allowed tree later (allowlist rots — fail safe, not open).
		exc("plugins/cache"),
		exc("projects/*/file-history"),
	}}
}

// Desktop is the allowlist for the app-support Claude root. Only the small config
// and session-metadata files; the ~12 GB of Electron/Chromium cache, cookies,
// storage, and machine-local UI state is denied by default (never descended).
func Desktop() List {
	return List{Rules: []Rule{
		inc("claude-code-sessions"),
		inc("local-agent-mode-sessions"),
		inc("claude_desktop_config.json"),
		inc("config.json"),
		inc("cowork-enabled-cli-ops.json"),
		inc("extensions-blocklist.json"),
		inc("git-worktrees.json"),
	}}
}
