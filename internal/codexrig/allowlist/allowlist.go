// Package allowlist is codexrig's answer to "which files under ~/.codex may
// leave this machine". The rule engine lives in internal/agentrig/allowlist;
// this package is only the policy.
//
// It is allowlist-by-default-deny, which is the safety property that matters
// most here: a Codex release that starts writing a new secret-bearing file is
// excluded until somebody explicitly allows it, rather than syncing on the next
// run because nobody thought to add an exclude. Codex's home is a good argument
// for that posture — 1.0 GB on the machine this was written against, of which
// roughly 400 MB is plugin runtimes and caches, 85 MB is a telemetry database,
// and 4 KB is the credential.
//
// The dangerous exclusions are written down even though default-deny already
// covers them. An exclude beside auth.json is not protecting anything today; it
// is a note to whoever later widens an include, and it is the thing that turns
// "we never included it" into "we decided not to".
package allowlist

import (
	"github.com/rigsmith/rigsmith/internal/agentrig/allowlist"
	"github.com/rigsmith/rigsmith/internal/codexrig/config"
)

type (
	// List and Rule are re-exported so callers name one package.
	List = allowlist.List
	Rule = allowlist.Rule
	Link = allowlist.Link
)

var (
	inc  = allowlist.Inc
	exc  = allowlist.Exc
	Walk = allowlist.Walk
)

// Options select the parts of the policy a run wants.
type Options struct {
	// Sessions carries the rollout files — Codex's session transcripts — as
	// well as configuration. Off by default: a rollout is a large append-only
	// artifact whose cross-machine resume is not a proven round trip, so
	// carrying it is backup rather than portability, and that is a choice the
	// user makes rather than one the tool makes for them.
	Sessions bool
}

// For returns the allowlist for a root id.
func For(rootID string, opts Options) List {
	switch rootID {
	case config.RootCLI:
		return CLI(opts)
	default:
		// An unknown root syncs nothing. Default-deny applies to roots too: a
		// root id nobody wrote a policy for is not a licence to walk it.
		return List{}
	}
}

// CLI is the allowlist for Codex's home ($CODEX_HOME, ~/.codex by default).
func CLI(opts Options) List {
	rules := []Rule{
		// --- the configuration itself -----------------------------------
		// The user config, and the named profile overlays beside it
		// (`codex --profile NAME` reads NAME.config.toml). Both are TOML and go
		// through the TOML codec, which redacts by field and rewrites paths —
		// never the raw-file path, which would bypass both.
		inc("config.toml"),
		inc("*.config.toml"),

		// Global instructions. Codex's counterpart to CLAUDE.md.
		inc("AGENTS.md"),
		inc("AGENTS.override.md"),

		// What you have taught Codex.
		inc("skills"),
		inc("prompts"),
		inc("rules"),
		inc("themes"),

		// Codex ships its own system skills and re-creates them; carrying them
		// would restore one release's copies over another's.
		exc("skills/.system"),
		exc("skills/.codex-system-skills.marker"),

		// --- credentials: never, under any include ----------------------
		// auth.json is the entire secret. There is no OS credential store
		// behind it in 0.144.6, so a copy of this file is a working login.
		exc("auth.json"),
		exc("auth.json.lock"),

		// --- machine state that must not travel -------------------------
		// An installation id identifies THIS install; restoring it elsewhere
		// makes two machines claim to be one.
		exc("installation_id"),
		// A hook is trusted by the sha256 of a file at an absolute path. Both
		// halves are local: the path may not exist on the other machine, and a
		// stale hash grants nothing. Restoring it is at best inert.
		exc("hooks.json"),
		// Caches and derived catalogs, all re-fetched on demand.
		exc("models_cache.json"),
		exc("cache"),
		exc("vendor_imports"),
		exc("external_agent_session_imports.json"),
		// Per-PR review bookkeeping for a checkout that exists on one machine.
		exc("review-state-*.json"),
		// Desktop/Electron global state, migration markers, reconciliation flags.
		exc(".codex-global-state.json"),
		exc(".codex-global-state.json.bak"),
		exc(".personality_migration"),
		exc(".sandbox_migration"),
		exc(".app-server-state-reconciled-v1"),

		// --- live databases: never copy a hot SQLite file ---------------
		// Every one of these ships with a -wal and a -shm, and a file-level
		// copy of a database mid-write is a corrupt database, not a backup.
		// thread_history is in any case a projection of the rollouts, keyed by
		// byte offset into them, so the rollouts are the artifact worth having.
		exc("**/*.sqlite"),
		exc("**/*.sqlite-wal"),
		exc("**/*.sqlite-shm"),
		exc("**/*.db"),
		exc("sqlite"),

		// --- large, regenerable, or purely local ------------------------
		exc("plugins"),      // marketplace runtimes; ~315 MB, re-installable
		exc("computer-use"), // a bundled helper .app; ~69 MB
		exc("log"),          // diagnostics
		exc("logs"),
		exc(".tmp"),
		exc("tmp"),
		exc("ipc"),                 // a unix socket
		exc("thread-writer-locks"), // lock files for live threads
		exc("shell_snapshots"),     // captured shell environments, machine-specific
		exc("process_manager"),     // pids
		exc("node_repl"),
		exc("browser"),   // per-session browser grants
		exc("worktrees"), // checkouts, which belong to git
		exc("rollout-migrations"),
		exc("visualizations"),
		exc("pets"),
		exc("dictation-history"),
		exc("transcription-history.jsonl"),
		exc("attachments"), // needs an export policy, not a sweep

		// Automations are scheduled work. Restoring them on a second machine
		// would run every job twice; they need a deliberate import, not a copy.
		exc("automations"),

		// A dependency tree is never configuration, wherever it appears.
		exc(allowlist.AnyDepth + "node_modules"),
		exc(allowlist.AnyDepth + ".git"),
		exc(allowlist.AnyDepth + ".DS_Store"),
	}

	if opts.Sessions {
		// Codex shards rollouts by date: sessions/YYYY/MM/DD/rollout-<stamp>-<uuid>.jsonl.
		rules = append(rules,
			inc("sessions"),
			inc("archived_sessions"),
			// The thread-name index, so a restored machine can resolve
			// `codex resume <name>` rather than only a uuid.
			inc("session_index.jsonl"),
		)
	}
	return List{Rules: rules}
}
