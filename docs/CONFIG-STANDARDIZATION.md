# rigsmith config standardization

How configuration is loaded, named, located, and overridden across the family —
`rig`, `changerig`, `relrig`, `clauderig`, and the installer scripts. This is
the standard of record. It is **implemented** (see "What shipped" at the end).

Decisions that shaped it:

- **Full unification** — shared loader mechanics (`core/confkit`) + a uniform
  `config` command on every binary.
- **Hybrid global location** — new config homes under XDG; existing paths
  (`~/.rig.json`, `~/.clauderig/`) are grandfathered, not moved.
- **JSONC in `.json`** — one format everywhere; the `.json` extension stays.
- **No file consolidation** — each domain keeps its own file.
- **Clean break** — no back-compat aliases or fallbacks; the formats and env
  names changed, and the handful of repos get fixed by hand. (Pre-1.0, personal.)

---

## The four config files

Five binaries, four config files — each a separate domain, deliberately not
merged:

| File | Tool(s) | Domain |
|---|---|---|
| `.rig.json` (+ `~/.rig.json`) | rig | dev-loop |
| `.changeset/config.json` | changerig, relrig | versioning |
| `.changeset/release.jsonc` | relrig | release pipeline |
| `~/.clauderig/config.json` | clauderig | machine sync |

State/data files are **not** config and are untouched: `.changeset/*.md`,
`.changeset/pre.json`, clauderig's manifests/devices registries, and Claude
Code's own `.claude/settings.json` / `.claude/allow-main`.

---

## The standard

### Format & extension
JSONC everywhere. **Every** loader routes top-level file decode through
`core/jsonc`, so comments and trailing commas are accepted in all four files
(previously only `.rig.json` and `release.jsonc` allowed them — commenting
`config.json` or `~/.clauderig/config.json` was a silent parse trap). The
`.json` extension stays (matches `.rig.json`, keeps `$schema`/editor wiring);
`.jsonc` remains valid for `release.jsonc`. Writes that regenerate a whole
document (clauderig's typed save) still emit plain JSON.

### Canonical precedence ladder
Every tool, low → high:

```
built-in defaults  →  user/global file  →  repo/project file  →  environment  →  CLI flags
```

A tool may have no user/global tier — the ladder degrades gracefully.
`changerig`/`relrig` deliberately have none: changeset config (`baseBranch`,
package globs, ecosystem blocks) and the release pipeline are intrinsically
per-repo, so a machine-wide default would be a footgun. Their ladder is
`defaults → repo → env → flags`.

### Global location (hybrid)
- **New** config homes belong under `$XDG_CONFIG_HOME/rigsmith/` (fallback
  `~/.config/rigsmith/`).
- **Existing** paths are grandfathered and not moved: `~/.rig.json` stays;
  `~/.clauderig/` stays (it is also the git staging dir + registries — config is
  correctly co-located with its data).

### Env-var policy
- `<BINARY>_*` for tool behavior: `RIG_`, `CHANGERIG_`, `RELRIG_`, `CLAUDERIG_`.
- `RIGSMITH_*` reserved for family/install-level vars.
- External/standard vars used verbatim, never re-prefixed.
- Test gates follow the binary prefix with a clear suffix (`_E2E`, `_IT`, …).

### The `config` command (uniform)
Every binary has `config get | set | path | edit` (plus `show` where a tool
already had it). `set <key> <value>` edits the file in place via the shared
comment-preserving writer; `get` prints one key or all scalar keys; `path`
prints the resolved file path(s); `edit` opens `$VISUAL`/`$EDITOR`.

---

## Environment variables (master table)

| Var | Tool | Purpose |
|---|---|---|
| `RIG_GLOBAL_CONFIG` | rig | Override the user-wide `~/.rig.json` path (test seam) |
| `RIG_PWSH_PROFILE` | rig | Override the PowerShell profile path for `rig setup` (test seam) |
| `RIG_SELFUPDATE_REPO` | rig | GitHub repo slug `rig self-update` checks (was `RIGSMITH_REPO`) |
| `RIG_DOTNET_IT` | rig | Gate the .NET integration tests (test) |
| `CHANGERIG_NET_DLL` | changerig | Path to the net-changesets oracle DLL (was `NET_CHANGESETS_DLL`, test) |
| `CLAUDERIG_ALLOW_MAIN` | clauderig | Opt out of the base-branch guard for this session |
| `CLAUDERIG_E2E` | clauderig | Enable the end-to-end tests (test) |
| `RIGSMITH_INSTALL` | installer | Install prefix (default `~/.local`) |
| `RIGSMITH_DEV_BIN` | installer | Dev-install launcher dir (default `~/.local/bin`) |

Used verbatim (external/standard, never re-prefixed): `GITHUB_TOKEN`,
`GH_TOKEN`, `GITLAB_TOKEN`, `GL_TOKEN`, `NUGET_API_KEY`, `EDITOR`, `VISUAL`,
`SHELL`, `ZDOTDIR`, `XDG_CONFIG_HOME`, `PATH`, `HOME`. The release pipeline also
expands arbitrary `${env.NAME}` references inside `release.jsonc`.

---

## What shipped

- **WS1 — JSONC parity.** `core/config` and `clauderig/internal/config` now
  decode via `core/jsonc` (`Strip` once, then `encoding/json`). Regression tests
  added.
- **WS2 — `core/confkit`.** New zero-dependency package holding the shared
  mechanics: a comment-preserving JSONC `Writer` (configurable `$schema`) and a
  `Truthy(env)` helper. `rig`'s writer now delegates to it; the base-branch
  guard uses `Truthy`. Root discovery stays per-tool by design — the finders
  have different precedence (rig: `.rig.json` > manifest > `.git`; changeset:
  `.changeset` > `.git`), so a shared abstraction would obscure, not dedupe.
- **WS3 — precedence ladder.** Documented above; `rig` already conformed, the
  others gain consistent env handling with no spurious global tier.
- **WS4 — env renames (clean break).** `RIGSMITH_REPO` → `RIG_SELFUPDATE_REPO`;
  `NET_CHANGESETS_DLL` → `CHANGERIG_NET_DLL`. No fallbacks.
- **WS5 — uniform `config` command.** Added to `rig`, `changerig`, `relrig`;
  `clauderig`'s `set-prune` / `set-autorestore` / `set-worktree-open` /
  `set-worktree-opener` / `set-remote` collapsed into `config set <key> <value>`
  (the `remote` key keeps the private-repo gate).
- **WS6 — docs.** This file, plus READMEs and the clauderig command docs.

## Non-goals

- Merging config files — each domain keeps its own.
- Moving grandfathered global paths (`~/.rig.json`, `~/.clauderig/`).
- A machine-wide changeset/release config tier.
- A shared *typed* config struct or merge — each tool keeps its own schema and
  merge rules; only the file-level mechanics are shared.
