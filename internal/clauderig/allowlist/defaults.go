package allowlist

// For returns the allowlist for a root id ("desktop" → Desktop, else CLI).
func For(rootID string) List {
	if rootID == "desktop" {
		return Desktop()
	}
	return CLI()
}

// CLI is the allowlist for the ~/.claude root. Mirrors the design doc's table:
// config + skills + plans + commands/agents + the marketplaces/data plugin config
// + the project transcripts (retention is applied separately by mtime, not here).
// Everything else — caches, statsig, sessions registry, shell snapshots, locks,
// telemetry, file-history, credentials — is denied by default.
// vendored are dependency trees that can appear anywhere inside an otherwise
// allowed tree — a skill with npm deps, a Cowork session that ran a build in its
// outputs dir. They're reinstallable from the lockfile, enormous, and pure churn,
// so they're pruned by name at any depth rather than carved out per-site.
func vendored() []Rule {
	return []Rule{exc(anyDepth + "node_modules")}
}

func CLI() List {
	return List{Rules: append(vendored(),
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
	)}
}

// Desktop is the allowlist for the app-support Claude root. Only the small config
// and session-metadata files; the ~12 GB of Electron/Chromium cache, cookies,
// storage, and machine-local UI state is denied by default (never descended).
func Desktop() List {
	return List{Rules: append(vendored(),
		inc("claude-code-sessions"),
		inc("local-agent-mode-sessions"),
		// A local_<id>/ DIRECTORY under a session is a Cowork sandbox working dir,
		// not config: an audit log, build outputs, and the documents the user
		// uploaded to that session, beside an .audit-key. Uploads are arbitrary user
		// content — there is nothing structurally secret for redact.Scan to detect,
		// so no downstream pass can make them safe and the exclusion has to happen
		// here. The sidecar local_<id>.json FILE beside it is the session metadata we
		// actually want, re-included by the longer (higher-scoring) pattern below.
		exc("local-agent-mode-sessions/*/*/local_*"),
		inc("local-agent-mode-sessions/*/*/local_*.json"),
		inc("claude_desktop_config.json"),
		inc("cowork-enabled-cli-ops.json"),
		inc("extensions-blocklist.json"),
		inc("git-worktrees.json"),
		// config.json IS synced, but a keep-only filter (engine.keepOnly) reduces it
		// to its stable `preferences` — the Desktop app rewrites the rest constantly
		// with rotating cache/token values (oauth.tokenCache, dxt.allowlistCache, …).
		inc("config.json"),
	)}
}
