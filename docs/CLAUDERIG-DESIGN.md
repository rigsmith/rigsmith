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
| `file-history/` | 🔧 | 92 MB rewind cache; not needed for resume — **excluded, no opt-in** (Q2) |
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

- **Slug rewrite** (`projects/`): read the real `cwd` from the transcript →
  portablize to `$HOME/Git/x` → on target, resolve → re-flatten to that machine's
  slug. Slugs are **never un-flattened** — Claude Code maps every non-alphanumeric
  char to `-` (lossy: a `-` may have been `/`, `.`, `_`, or literal `-`; confirmed
  on real slugs, e.g. `/nuxt-roost/.dmux` → `--dmux`), so the cwd comes from the
  transcript, where it sits ~800 bytes in (188/197 within 4 KB) → a **bounded
  header scan**, never a whole-file parse. Transcript *contents* are left as-is —
  tool references inside merely record where a tool *ran*; resume works without
  rewriting them. (`core/pathmap.Portablize` + `clauderig/internal/project`.)
- **Value-based rewrite** (Desktop sessions, `git-worktrees.json`): walk the JSON
  and rewrite any string value that resolves under a *known mapped prefix*
  (`$HOME`, mapped repo roots), leaving unmapped/system paths (`/tmp`) untouched.
  Robust to fields beyond `cwd`/`originCwd` — a census found `planPath`, permission
  `ruleContent`, and added `directories[]` also carry paths (see Q4).

Default mapping is the **home convention** (`~/Git` ⇄ `C:\Users\John\Git`, tail
identical = halyard's "portable" case). Overridable: custom home dir + explicit
per-path rewrites (halyard's per-OS literal + machine override). When a target
path doesn't exist yet (repo not cloned), **restore anyway** so a later
`git clone` lands the user ready to go (`StatusUnconfigured` → write-anyway).

### Manifest

`sync` writes a small `clauderig-manifest.json` recording, per project, the
**portable cwd template** (`$HOME/Git/x`) — extracted once via the bounded scan
above. `restore` then reads **only the manifest** to rewrite slugs, reopening
**zero transcripts** (decoupling restore from Claude Code's transcript format).
The manifest also stamps the producing **Claude Code version** (skew warning) and
the **source OS**, and is the natural home for the Desktop `claude-code-sessions`
cwd mappings (Q4).

## Retention & repo shape

- **30-day** working-tree window on `projects/` (≈ all of John's current 467 MB;
  the byte/​session curve is shallow past 14d but 30d is fine under the orphan
  scheme).
- **Two branches**: `main` = config (precious, tiny, full history kept);
  `history` = **orphan branch** for `projects/`, squashed to a single root commit
  so `.git` never grows past the working tree. Transcript sync history is
  disposable — squashing loses nothing that matters.
- **Squash trigger: size-based (Q3).** Squash when the `history` branch's packed
  git footprint exceeds **2× the retained working-tree size** (self-tunes to your
  actual churn), with a floor (skip below ~500 MB — not worth the rewrite).
  Preferred over a time-based cron because it tracks the thing we care about
  (repo size) directly.

## Transport

Plain **git** for push/pull. But the remote is **hard-gated to a verified-private
repo, no exceptions** (`internal/ghrepo`): the synced data is your Claude Code
state and must never land somewhere public or unverifiable. A remote is accepted
only when a provider CLI confirms it's private — **GitHub via `gh`, GitLab via
`glab`** (dispatched by host). Other hosts are refused (can't verify privacy). `init` offers **create a new private repo via
`gh repo create --private`** or **use an existing private repo** (verified);
`config set-remote` applies the same gate. Every failure mode — `gh` absent,
non-GitHub URL, unverifiable, or public — is refused; the only way to have no
verified-private remote is to have **no remote** (local-only staging). (A hosted
clauderig backend / non-GitHub private-repo support is possible v2; not v1.)

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

git auto-merges non-conflicting files (the common case across machines).
Simultaneous multi-machine editing is explicitly **out of scope**. For true
same-file conflicts, the strategy is **content-aware, keyed by file type** (idea
lifted from claude-sync's merge engine, but keyed on git content — **not mtime**,
which is fragile across machines with clock/checkout skew):

| Pattern | Strategy | Why |
|---|---|---|
| `memory/**`, `MEMORY.md` | **append-union** (dedup) | memory is append-y; latest-wins would silently drop entries |
| `settings*`, `skills/**`, `commands/**` | latest-wins | declarative; newest intent should win |
| `projects/**` (`*.jsonl`) | append | append-only logs delta + union cleanly |
| anything else | latest-wins (safe default) | |

A **lock file** prevents the watcher and a hook from racing into a concurrent
sync (lifted from claude-sync).

## Restore safety

First `clauderig restore` onto a non-empty `~/.claude`: **user chooses** back-up-
then-proceed or abort. Non-interactive contexts (hooks/CI) **default to abort**.
Flags: `--backup`, `--force`.

Mechanism: a **pre-sync snapshot** is taken before any tree-touching pull/restore
(lifted from claude-sync) so every operation is rollback-able. Snapshots are
pruned to the N most recent.

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

## Interactive UI

Stack: cobra + fang + huh + lipgloss + bubbletea (rigsmith standard). Model is
**hub + focused TUIs** (matches `changerig`): a `clauderig ui` dashboard for
at-a-glance status/actions, plus dedicated TUIs for the heavy flows. Every command
also runs **non-interactive/scriptable** when piped or given flags (TUIs are
gated on a TTY).

### `clauderig ui` — hub dashboard (bubbletea)
At-a-glance: remote reachability, local/behind status, last sync, per-root file
state, device registry. Hotkeys dispatch to the focused TUIs.
```
 clauderig                          machine: johns-mbp (macOS)
 ────────────────────────────────────────────────────────────
  Remote    github.com/john/claude-sync      ✓ reachable
  Status    ● 3 local changes · 1 behind remote
  Last sync 2h ago · pushed from johns-mbp
  Roots
   ~/.claude                  142 files   ✓ clean
   ~/Library/…/Claude           6 files   ● 2 changed
  Devices
   johns-mbp (this) 2h ago   work-pc 1d   linux-box 3d
  [s] sync  [r] restore  [d] diff  [p] path-map  [c] config  [q] quit
```

### `clauderig init` — first-run wizard (huh, ~5 steps)
remote (create via `gh` / paste URL) → machine identity + home maps → roots &
retention → secrets confirm (shows what will be stripped) → install hooks? →
first sync. Idempotent; re-running reconfigures without destroying config.

### `clauderig sync` — progress (bubbletea spinner stream)
Streams the pipeline so redaction/secret-scan/rewrite are visible, not magic:
```
  ✓ Snapshot taken (rollback-able)
  ✓ Redacted 3 secret fields from settings.json
  ✓ Secret scan: clean
  ⠹ Rewriting 14 project slugs → portable form
  ⠹ Pushing → origin/main
  ✓ Synced · 8 changed · 0 conflicts
```

### `clauderig restore` — preview + safety (huh)
Shows path rewrites and the write/skip set **before** touching the tree, then the
non-empty-target choice (default abort under no-TTY):
```
 restore ─ preview onto work-pc (Windows)
  Path rewrites   $HOME/Git/rigsmith → C:\Users\John\Git\rigsmith  (+47 slugs)
  Write 142 files (config + 30d history) · skip sessions/, file-history/
  ~/.claude is non-empty:  (•) Back up to ~/.claude.bak and proceed  ( ) Abort
```

### Conflict resolution — **per-file select, with mergetool escape hatch**
True conflicts are rare (simultaneous editing out of scope). A huh prompt per
conflicting file:
```
 Conflict: settings.json (both changed)
  (•) Keep this machine's version
  ( ) Keep remote (work-pc, 1h ago)
  ( ) Open in $EDITOR
  ( ) Send all conflicts to git mergetool
```
The last option delegates the whole conflict set to the user's configured
`git mergetool` — no in-app diff viewer to build.

### Path map — **config file canonical + editor in `ui`**
The mapping lives in the config file (single source of truth that `pathmap`
reads). An editor pane in `clauderig ui` (`[p]`) adds/edits home + per-OS +
machine overrides with validation + cycle detection (halyard-style); the file
always wins. `clauderig doctor` previews resolved rewrites and flags unmapped
machines:
```
 clauderig doctor
  $HOME → /Users/john ✓   work-pc C:\Users\John ✓
  linux-box: UNMAPPED ⚠  (its sessions won't translate on restore)
```

## Prior art — what we lift, what's whitespace

Reviewed the three leading community tools (2026-06-12).

**`renefichtmueller/claude-sync`** (TS; pluggable backends, merge engine — the most
ambitious). **Lift:** content-aware per-file merge strategies (see Conflict
resolution); pre-sync snapshot + rollback; a sync **lock file**; a per-device
**registry** (last-sync timestamps → good `status`). **Its gaps = our edge:** it
copies the whole tree minus `.git`, so `.credentials.json` is **pushed in
plaintext by default** (encryption off by default, README never warns); its
`SelectiveSyncConfig` include/exclude is **defined but never wired** (you can't
actually exclude anything); **no path correction**; transcripts synced **uncapped**;
`latest-wins` keyed on **mtime** (clock-skew fragile).

**`elizabethfuentes12/claude-code-dotfiles`** (AWS author, but really a README
tutorial — 5 files, no binary). **Lift:** the empty-commit guard
(`git diff --cached --quiet` → skip); `git -C` discipline (never `cd`); the
idempotent **mirror-with-deletion** pattern from its `sync-to-kiro.sh` (Claude
commands → Kiro steering files, GC orphans) → generalize into pluggable
**exporters** (v2). **Instructive bugs:** its gitignore lists `credentials.json`
**without the leading dot**, so the real `~/.claude/.credentials.json` is never
matched and **gets committed** — the canonical case for *never matching secrets by
filename*; its `!`-allowlist lines are **inert** (it's actually a denylist); and
its two exclude lists (`gitignore` vs `git add`) had already **drifted**
(`skills/` in one, missing the other).

**`miwidot/ccms`** (bash, rsync-over-SSH, no git history). **Lift:** SHA256
integrity check; automatic backup before every pull. Otherwise a history-less
mirror — no path correction, manual excludes.

**Design rules these confirm:**
- **One allowlist as the single source of truth**, driving *both* what's copied
  and the generated `.gitignore` — so the two can never drift (the dotfiles bug).
- **Strip secrets by content/field + known paths + entropy tripwire**, never by
  fragile filename match (the `.credentials.json` miss).
- **Path correction and transcript retention are the real white space** — no
  reviewed tool does either.

## Open questions

1. ~~**Q1 — `sessions/*.json` exclusion.**~~ **RESOLVED (2026-06-12, spike): safe to
   exclude.** Evidence: (a) only 6 registry files vs 307 transcripts — resume can't
   be registry-driven; (b) the registry references sessionIds with no transcript on
   disk — it's a live-process list, not a resume index; (c) transcripts self-contain
   `cwd`/`timestamp`/`gitBranch` — all a picker needs; (d) **decisive isolated test**:
   with `CLAUDE_CONFIG_DIR` containing only one transcript and **no `sessions/`**,
   `claude --resume <id>` resolved the session (advanced past lookup to the auth
   gate), while a bogus id returned `No conversation found with session ID`. So
   `projects/<slug>/*.jsonl` is the resume source of truth. **Side-finding:** auth
   did **not** carry into a fresh config dir even on macOS (`Not logged in`) —
   empirically confirms the "strip secrets, re-auth per device" model.
2. ~~**Q2 — `file-history/` opt-in.**~~ **RESOLVED: killed.** 92 MB rewind cache,
   not needed for resume — permanently excluded, no opt-in flag.
3. ~~**Q3 — squash cadence.**~~ **RESOLVED: size-based** — squash the `history`
   branch when its packed footprint exceeds 2× retained working-tree size (floor
   ~500 MB). See Retention & repo shape.
4. **Q4 — `claude-code-sessions` resume fidelity.** *Approach decided (2026-06-12
   census), now **BUILT**.* The rewrite surface is broader than `cwd`/`originCwd` —
   also `planPath`, permission `ruleContent`, and added `directories[]` — so the
   rewriter is **value-based**: `pathmap.PortablizeJSONValues` (sync) rewrites any
   string under a known mapped prefix to `$HOME/…`; `pathmap.ResolveJSONValues`
   (restore) resolves `$`-templates to the target, leaving unmapped/system paths
   (`/tmp`) and non-path values untouched. Applied to every `.json` in both roots
   (so `settings.json` paths translate too). **Still deferred (non-blocking):** the
   empirical "does the Desktop app actually resume after rewrite" test, which needs
   driving the Electron app rather than the headless CLI trick used for Q1. (One
   known edge: a permission `ruleContent` with a leading `//` or globs may not
   prefix-match — best-effort; a stale rule simply fails safe on the new machine.)
