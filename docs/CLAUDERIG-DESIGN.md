# clauderig design

> Status: **scoping** (2026-06-12). Decisions below are agreed with John in the
> scoping session; nothing is built yet. This is the spec to build against.

`clauderig` — the fourth rig: sync your Claude Code environment (config, skills,
and session history) across machines via your own git remote, correcting paths
across OSes on restore. Single static Go binary, zero runtime deps, `curl | sh`
onto any machine — the same north-star as `rig` / `relrig` / `changerig`.

The job that's actually hard, and that the existing community tools (claude-sync,
cc-sync-template, ccms, …) punt on: **cross-OS path correction** and **not
leaking secrets**. clauderig owns both.

## The one-line model

Sync a curated **allowlist** from a set of **roots** to a private git repo;
**redact secrets** on commit; **rewrite paths** on restore so a session captured
at `/Users/john/Git/x` resumes at `C:\Users\John\Git\x`. Config lives on a normal
branch (precious, tiny); bulky session history lives on an **orphan branch** that
is periodically squashed so the repo stays bounded.

## Roots (clauderig is multi-root)

Each root is `{ os-resolved location, allowlist, rewrite rules }`. The location is
resolved per-OS by `core/pathmap` (ported from halyard's path cascade).

| Root | macOS | Windows | Linux | Rewrite |
|---|---|---|---|---|
| **CLI** | `~/.claude` | `~/.claude` | `~/.claude` | dir-**slug** rewrite on `projects/` |
| **Desktop/Cowork** | `~/Library/Application Support/Claude` | `%APPDATA%\Claude` | `~/.config/Claude` | **field**-level `cwd`/`originCwd` rewrite |

The Desktop root is **12 GB on disk** but ~99.99% is Electron/Chromium cache —
which is exactly why the inclusion model is an allowlist, never a denylist.

## Allowlist

Legend: ✅ sync · 🔧 junk (machine-local/ephemeral) · 🔑 secret (never leaves machine)

### Root: `~/.claude` (CLI)

| Path | Verdict | Notes |
|---|---|---|
| `settings.json` | ✅ (redacted) | strip inline `env` / `apiKeyHelper` / MCP creds before commit |
| `settings.local.json` | ✅ (redacted) | per-machine prefs; redact same as above |
| `skills/` | ✅ | small (~520K), high value |
| `plans/` | ✅ | small |
| `commands/`, `agents/`, `CLAUDE.md` | ✅ | global user config, if present |
| `plugins/{marketplaces,data}` | ✅ | config; **not** `plugins/cache` |
| `projects/<slug>/*.jsonl` | ✅ | the **resume payload**; slug-rewrite + 30d retention (history branch) |
| `sessions/*.json` | 🔧 | PID-named live-process registry; stale PIDs on another machine (see Open Q1) |
| `file-history/` | 🔧 (v1) | 92 MB rewind cache; not needed for resume. Opt-in later |
| `cache/`, `*-cache.json`, `statsig/`, `telemetry/`, `debug/` | 🔧 | machine-local / device ids |
| `shell-snapshots/`, `session-env/`, `ide/*.lock`, `tasks/*/.lock` | 🔧 | runtime/host state |
| `downloads/`, `.DS_Store`, `.last-*` | 🔧 | junk |
| `.credentials.json` (Linux/Win only) | 🔑 | on macOS creds live in **Keychain** — not in the tree at all |

### Root: app-support `Claude/` (Desktop/Cowork)

| Path | Verdict | Notes |
|---|---|---|
| `claude-code-sessions/` | ✅ | Desktop/Cowork session metadata; UUID-keyed, `cwd`/`originCwd` **inside** → field rewrite |
| `local-agent-mode-sessions/` | ✅ | same shape |
| `claude_desktop_config.json` | ✅ (redacted) | MCP server config; may carry secrets → redact |
| `config.json`, `cowork-enabled-cli-ops.json`, `extensions-blocklist.json` | ✅ | small config |
| `git-worktrees.json` | ✅ | path-keyed → rewrite |
| `Cache/`, `Code Cache/`, `GPUCache/`, `IndexedDB/`, `blob_storage/`, `Crashpad/`, `sentry/`, `Cookies*`, `*Storage*`, `DIPS*`, `Trust Tokens*` | 🔧 | the 12 GB of Electron junk |
| `window-state.json` | 🔧 | machine-local UI geometry |

**Allowlist rots — by design.** Claude Code adds files over time; a new
secret-bearing file added upstream must not silently leak. So: allowlist +
**field-level redactor** for the files that may carry secrets + a **pre-commit
entropy/regex tripwire** that fails the commit loudly if a token slips through.

## Secrets

Model: **strip, don't sync** (option c). Consequence, stated as a product
promise: clauderig syncs *config, not credentials* — onboarding a new machine
includes a re-auth step (`claude` login, re-enter API keys, re-auth MCP). This is
also correct security hygiene (per-device credentials), and on macOS the creds
aren't even in the synced tree (Keychain).

Secrets are often **inline** in otherwise-syncable files, so file-exclusion is
insufficient. The redactor parses JSON, strips known secret-bearing fields, commits
the redacted doc, and on restore **merges synced fields back without clobbering
the local machine's secrets**. (Redaction is an always-on *transform*, separate
from conflict *merge* below.)

## Path rewriting

Ported from halyard's `Favorites` path system into `core/pathmap`: a **token +
cascade** resolver (`$HOME`, per-OS literals, machine override) with cycle
detection and OS-aware case comparison. Two fidelities, both v1:

- **Slug rewrite** (`projects/`): un-flatten slug → absolute → reverse-resolve to
  `$HOME/Git/x` → on target, token → absolute → re-flatten to that machine's slug.
  Transcript *contents* are left as-is (slug-only) — tool references inside merely
  record where a tool *ran*; resume works without rewriting them.
- **Field rewrite** (Desktop sessions, `git-worktrees.json`): rewrite `cwd` /
  `originCwd` / path fields in place.

Default mapping is the **home convention** (`~/Git` ⇄ `C:\Users\John\Git`, tail
identical = halyard's "portable" case). Overridable: custom home dir + explicit
per-path rewrites (halyard's per-OS literal + machine override). When a target
path doesn't exist yet (repo not cloned), **restore anyway** so a later
`git clone` lands the user ready to go (`PathStatus.Unconfigured` → write-anyway).

## Retention & repo shape

- **30-day** working-tree window on `projects/` (≈ all of John's current 467 MB;
  the byte/​session curve is shallow past 14d but 30d is fine under the orphan
  scheme).
- **Two branches**: `main` = config (precious, tiny, full history kept);
  `history` = **orphan branch** for `projects/`, **periodically squashed** to a
  single root commit so `.git` never grows past the working tree. Transcript sync
  history is disposable — squashing loses nothing that matters.

## Transport

Plain **git** — works with any remote, so transport stays remote-agnostic.
"GitHub" only enters as the bootstrap convenience: use `gh` to create the private
repo. (A hosted clauderig backend is a possible v2; not v1.)

## Triggers

- `clauderig sync` / `clauderig restore` — explicit, interactive.
- Claude Code **hooks**: `SessionStart` → pull; `Stop`/`SessionEnd` → commit+push.
  The binary is the hook target (cross-platform because it's our static binary),
  and clauderig installs its own hooks idempotently into the synced `settings.json`
  — self-bootstrapping.

**Hooks are non-interactive** → the hook path must **never prompt and never
clobber**: pull is safe-fast-forward-or-skip, push is best-effort. Only explicit
`clauderig restore` may prompt.

## Conflict resolution

git auto-merges non-conflicting files (the common case across machines). **Last
writer wins** is only the *fallback* on a true same-file conflict — simultaneous
multi-machine editing is explicitly **out of scope**. Append-only JSONL deltas
cleanly, so transcript conflicts are rare.

## Restore safety

First `clauderig restore` onto a non-empty `~/.claude`: **user chooses** back-up-
then-proceed or abort. Non-interactive contexts (hooks/CI) **default to abort**.
Flags: `--backup`, `--force`.

## Version skew

Claude Code self-updates (`.last-update-result.json`), so machines drift. clauderig
stamps the producing Claude Code version in its manifest and **warns on mismatch**;
it does **not** auto-update Claude Code (offer-only at most — auto-update is scope
creep).

## Where it lives

New `clauderig/` module in this repo (binary `clauderig`), same standards as the
other rigs (cobra/fang/huh/lipgloss, MIT, goreleaser entry). Generalized into
`core/`:

- `core/pathmap` — the halyard-derived token/cascade resolver (**at minimum** this).
- `core/redact` — JSON field redaction + entropy tripwire (candidate).
- The allowlist/rewrite-rule model is clauderig-specific; keep it in the module
  unless a second consumer appears.

## Open questions

1. **Q1 — `sessions/*.json` exclusion.** Believed safe to exclude (PID-keyed live
   registry; durable mapping is in `projects/` + `claude-code-sessions/`). **Verify
   empirically**: on the next machine move, restore *without* it and confirm
   `--resume`/`--continue` still lists everything. If not, it joins the allowlist.
2. **Q2 — `file-history/` (92 MB).** Excluded v1. Worth an opt-in flag for users
   who want rewind/checkpoint continuity across machines?
3. **Q3 — squash cadence.** Time-based (weekly) vs size-based (when `history`
   branch exceeds N MB) vs on-restore-prune. Pick before building retention.
4. **Q4 — `claude-code-sessions` resume fidelity.** Does rewriting only
   `cwd`/`originCwd` fully restore Desktop/Cowork session resumability, or are
   there nested path refs? Spike alongside Q1.
