package allowlist

import "strings"

// For returns the allowlist for a root id (any Desktop tree → Desktop, else CLI).
func For(rootID string) List {
	if strings.HasPrefix(rootID, desktopProfilePrefix) {
		return DesktopProfile()
	}
	if DesktopRoot(rootID) {
		return Desktop()
	}
	return CLI()
}

const desktopProfilePrefix = "desktop@"

// DesktopRoot reports whether rootID names a Claude Desktop application-support
// tree: the machine-wide install ("desktop"), or one `clauderig desktop` profile
// ("desktop@<name>"). Both have the same layout, so both get the same list.
func DesktopRoot(rootID string) bool {
	return rootID == "desktop" || strings.HasPrefix(rootID, desktopProfilePrefix)
}

// DesktopProfile is the allowlist for a `clauderig desktop` profile directory.
//
// A profile is the app's tree under data/, plus profile.json — clauderig's own
// record of the profile (name, label, when it was made). Desktop()'s rules are
// rebased under data/ rather than restated, so a rule added there covers
// profiles automatically and the two lists cannot drift; profile.json is added
// beside them so a restore brings the profile back as an entry `clauderig
// desktop open` can find, not just a directory of files.
//
// The login lives in the app's tree and is excluded there, so it is excluded
// here too — this rebase cannot widen what syncs, only relocate it.
func DesktopProfile() List {
	src := Desktop().Rules
	rules := make([]Rule, 0, len(src)+1)
	rules = append(rules, inc("profile.json"))
	for _, r := range src {
		rules = append(rules, under("data", r))
	}
	return List{Rules: rules}
}

// under rebases a rule beneath prefix, preserving the precedence between rules:
// every pattern grows by the same number of characters, and specificity is
// measured in characters. An any-depth rule is returned unchanged — it already
// matches wherever the segment appears, and pinning it to one subtree would
// narrow a deliberately broad prune.
func under(prefix string, r Rule) Rule {
	if strings.HasPrefix(r.Pattern, anyDepth) {
		return r
	}
	return Rule{Pattern: prefix + "/" + r.Pattern, Action: r.Action}
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
		// skills-plugin is not a session at all: it is the app's local copy of the
		// bundled skills (docx/pptx/xlsx/pdf/skill-creator), ~8 MB of vendored
		// scripts and XSD schemas that Claude Desktop re-downloads on its own.
		// Syncing it costs repo size and restore time and buys nothing — and once
		// each Desktop PROFILE is walked as its own root, the same 8 MB would land
		// once per account.
		exc("local-agent-mode-sessions/skills-plugin"),
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
