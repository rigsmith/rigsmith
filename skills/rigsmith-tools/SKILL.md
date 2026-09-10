---
name: rigsmith-tools
description: >
  Use the rigsmith CLI family — rig (convention-first dev launcher: build/test/run/
  format/lint/typecheck/coverage/kill/worktree across .NET, Node, Go, Rust),
  changerig (changesets), shiprig (releases/publish), and clauderig (sync Claude Code
  config across machines, v2 queued sync + worktree/PR guard). Invoke whenever the work involves
  building/testing/running/formatting a project, managing changesets or changelogs,
  cutting or publishing a release, creating worktrees/branches, or syncing Claude
  Code setup, preparing queued requests from hook payloads, managing its saved sync
  queue, or using manual queue sync with
  confirmed acknowledgement, dry-run previews or transcript flushing. Queued workers
  capture eligible requested sessions/subagents completely while unrelated plain
  transcripts keep normal throttling. Invoke even if the user names a raw tool (go/dotnet/npm/cargo) instead of rig.
allowed-tools: Bash(rig:*), Bash(rig-dev:*), Bash(changerig:*), Bash(changeset:*), Bash(shiprig:*), Bash(shiprig-dev:*), Bash(clauderig:*), Bash(clauderig-dev:*), Bash(command -v:*), Bash(which:*)
---

# rigsmith tools

A family of convention-first, zero-runtime-dependency CLIs (single static Go
binaries). The same verb works in any ecosystem — each tool detects the repo and
runs the right native command, sharing one project-detection engine.

| Tool | Role | Reach for it when |
|------|------|-------------------|
| `rig` | dev launcher | building, testing, running, formatting, worktrees, branches |
| `changerig` (alias `changeset`) | changesets | recording a user-facing change, bumping versions, writing CHANGELOG |
| `shiprig` | releases | publishing to registries, tagging, the release pipeline |
| `clauderig` | Claude Code config sync + guard | syncing `~/.claude` across machines, v2 queue/retry/drain, worktree/PR discipline |

**First, confirm they're installed:** `command -v rig`. If missing, install from a
rigsmith checkout with `rig source-install` (stable binaries) or `rig dev-install`
(recompile-on-run `*-dev` launchers), or `curl -fsSL https://rigsmith.sh | sh`.
If a stable binary is absent but a `*-dev` one exists, use that (`rig-dev`, etc.).

## Why these over running the native tool by hand

Reaching for `rig build` instead of `go build ./...` / `dotnet build` / `npm run
build` is not just a shortcut:

- **One verb, every repo.** No remembering which ecosystem you're in or its flags.
  The same `rig test` / `rig coverage --min 80` works in .NET, Node, Go, and Rust.
- **Shared, correct detection.** Root anchoring (`.rig.json` > solution/workspace
  manifest > git root), `exclude` globs, and `defaultProject` are resolved once and
  shared by every verb — a hand-typed command skips all of that.
- **Env layering done right.** `.env`/`.env.local` < ambient < `.rig.json env` <
  command, applied consistently. Hard to reproduce by hand.
- **Things that are genuinely fiddly by hand:** coverage gates with in-process
  cobertura→HTML for .NET (`rig coverage --open`), killing dev processes by
  port/pattern (`rig kill`), and the version-cascade + changelog rendering that
  `changerig version` owns (see below). Don't reimplement these inline.

When unsure how a repo builds, run `rig info` first — it prints the discovered
root, primary ecosystem, `.rig.json`, per-ecosystem dev commands, and packages.

## rig — the inner dev loop

```sh
rig info                      # what rig discovered — run first when unsure
rig build | test | run | format | lint | typecheck
rig coverage --min 80 --open  # tests behind a coverage gate, open the report
rig watch test                # re-run a verb on change (also: rig w t, prefix-match rig cove)
rig kill --port 5173          # kill dev processes by project / pattern / port
rig add <pkg> | remove | outdated | upgrade   # package management, native per ecosystem
rig install | ci | clean | rebuild            # restore / clean / rebuild
rig doctor                    # environment checklist (SDK pins via global.json)
rig cd <fuzzy>                # print a project dir (pair with a shell wrapper)
rig -n build                  # --dry-run: print the command, don't run it
rig -q build                  # --quiet: hide the `→ command` echo
```

Worktree & branch management (used by the PR-discipline workflow below):

```sh
rig worktree new <branch>     # sibling checkout + new VS Code window (alias: rig wt)
rig worktree list | open | rm | prune
rig branch list | rm | prune  # local branches (alias: rig br; --gone for gone-upstream)
rig prune                     # one sweep: reap merged worktrees, then their branches (alias: tidy)
```

`rig` needs **zero config**. An optional `.rig.json` (JSONC, repo root) supplies
only what can't be inferred — read `rig config path` / `rig config get <key>`, and
write with `rig config set` (comment-preserving).

### Before inventing a custom verb — check what already exists

Custom verbs live under `commands:` in `.rig.json`, but reach for one **only after**
ruling out everything built in. A custom name that collides with a built-in verb is
silently ignored anyway, so duplicating one is wasted effort. In order:

1. **Built-in verbs** — `build test run format lint typecheck coverage kill add
   remove outdated upgrade install ci clean rebuild global dlx publish doctor cd
   watch worktree branch prune init config info ui`. One of these usually fits.
2. **Surfaced scripts** — in a Node repo every `package.json` script is already a
   `rig <script>` verb; in a Go workspace, `go.work` mains under `scripts/`/`cmd/`
   surface as bare `rig <name>` verbs. Check `rig info` before adding anything.
3. **Other rig tools** — versioning/release belongs to `changerig`/`shiprig`, not a
   custom `rig` command; Claude-config/worktree chores belong to `clauderig`.

Only when none of the above covers the task, add it:

```jsonc
// .rig.json
{ "commands": {
    "deploy": "./deploy.sh",                    // shell string
    "bench": ["go", "test", "-bench", "."],     // argv
    "open":  { "os": { "macos": "open .", "windows": "explorer ." } }
} }
```

Custom commands honor `--dry-run`, forward extra args, and take `env`/`cwd`/
`description`.

## changerig — changesets (the changelog source of truth)

When a change is user-facing, record it **in the same PR**:

```sh
changerig add -p <pkg> --bump <patch|minor|major> -m "<summary>"   # interactive with no flags
changerig status --verbose       # the pending release plan
changerig version                # bump manifests + write CHANGELOG.md, cascading to dependents
changerig ui                     # interactive menu
```

**Why not hand-edit versions/changelogs:** `version` runs the shared engine —
it parses changeset files, **cascades** bumps to dependents (a dependent gets a
patch bump when its dependency releases), applies linked/fixed/lockstep grouping,
stamps every ecosystem's manifest, and renders `CHANGELOG.md`. Editing version
numbers or changelog entries by hand drifts from this and silently misses
dependents. Let `version` own them.

## shiprig — releases

Everything `changerig` does, plus publish/tag/pre orchestration. Releasing is
usually CI's job; locally:

```sh
shiprig status | version         # preview / apply the release plan
shiprig publish                  # registries + tags — idempotent, confirm-gated on a TTY (--yes for CI)
shiprig release                  # the configurable step pipeline (.changeset/release.jsonc)
```

Pipeline order: `version → commit → publish → tag → push → release → artifacts`.

In a `rig stack` stackspace, `shiprig` stamps no member manifest (the versions go
to `.changeset/versions.json` and `${version.<pkg>}`), and `tag`/`push`/`release`
are skipped automatically — do not add `order` omissions or `--no-git-tag` for
that. `shiprig version --no-stamp` computes and records without writing manifests
anywhere; a MinVer project shows as "no version in the tree" until a release
records one.

> **Do not run `publish` / `release` unless explicitly asked** — they push tags and
> hit live registries. `publish` is idempotent and confirm-gated, but treat it as
> outward-facing: confirm first.

## Setting up a repo that has no changeset/versioning config yet

If `.changeset/` is absent (no `changerig`/`shiprig` setup), **ask the user which
changelog style they want before running `init`** — it sets `versioning.source` in
config and is awkward to switch later:

- **Changesets (default).** Each PR adds an intent file via `changerig add`; the
  changelog is built from those files. Explicit, reviewable, decoupled from commit
  messages. → leave `versioning.source` unset (or `"changesets"`).
- **Conventional commits.** Changesets are synthesized from `feat:`/`fix:`/… commits
  since the last release; no per-PR file. → set `versioning.source: "commits"`
  (and optionally `versioning.scopes` to map a commit scope to a package).
- **Both.** Union of on-disk changesets and commit-derived ones. → `"both"`.

Then `changerig init` (or `shiprig init`) scaffolds `.changeset/`. Don't assume —
the choice changes the whole contribution flow.

## clauderig — sync Claude Code setup + the worktree guard

Syncs `~/.claude` (config, skills, session history) to a **private** git repo with
cross-OS path correction and secret stripping, and restores it on any machine.


New configs default to chunking on; omitted keys in existing configs mean auto
(follow the repo). For existing backups, upgrade all participating clients before running
`clauderig config set chunkTranscripts true` and `clauderig sync`. The next sync
migrates existing backups; restore reconstructs native JSONL. Set false and sync
to convert back (very large native blobs may exceed host limits), or set auto to
follow the repo. Complete staged-text secret scanning is always on. Set
`redactTranscripts true` to scrub supported signatures from staged conversations —
transcripts, tool results and memory notes — first;
never edit live transcripts to work around a publication refusal.

```sh
clauderig init                   # wizard: private repo, machine name, hooks (SessionStart/Stop/SessionEnd)
clauderig sync                   # snapshot → redact secrets → rewrite paths → commit → push
clauderig restore                # pull → rewrite paths for this OS → merge (keeps local secrets)
clauderig status | doctor        # state / health-check (doctor --fix repairs)
clauderig guide install          # install the CLAUDE.md blocks (worktree discipline + rigsmith-tools)
clauderig mcp list | add <name> <cmd...>   # manage MCP servers (mirrors `claude mcp`)
clauderig desktop list | open <name> | prune [<name>] [--vm|--all] [--dry-run] [--yes]   # Desktop profiles per account; prune reclaims caches / the Cowork VM image / its whole bundle — no name means every profile, and --vm/--all need --yes off a terminal
```

**Why not copy `~/.claude` by hand:** clauderig re-derives project-directory slugs
and path values for the target OS, strips secret-bearing fields before commit (with
a tripwire that fails loudly if one slips), and refuses any GitHub/GitLab remote
the provider-aware verifier cannot confirm is private. Git uses configured credentials;
privacy checks use `gh`/`glab` or `GITHUB_TOKEN`/`GH_TOKEN` and `GITLAB_TOKEN`/`GL_TOKEN`
when the matching CLI is absent. For GitLab/token-only setup use `config set remote
<url>` or `init --yes --remote <url>`; the existing interactive wizard and doctor
still have older `gh`-availability gates. A manual copy leaks secrets and breaks paths across machines.

**Relationship to this skill:** `clauderig guide` maintains a *brief, always-on*
"rigsmith tools" block in `CLAUDE.md`. This skill is the *deeper, on-demand*
reference — they complement each other; keep them consistent if you edit one.

### Explicit queued sync (v2 preview)

For requested queue work, confirm the chosen binary supports `clauderig queue
--help`. Ordinary sync and hooks remain synchronous; these commands do not install
or start a background service. Initialize ordinary sync history before queue init.
The queue requires a verified private HTTPS GitHub/GitLab remote and uses existing
Git credentials plus the provider-aware privacy check independently of doctor.

Choose an existing private directory for request files, outside source, staging,
runtime and all Desktop profile/data trees. Use the same `--dir` and repeated
`--profile <name>` selection on every command; default runtime is
`~/.clauderig/queue-runtime`, default profile selection is empty.

```sh
clauderig queue init
clauderig queue prepare --session <session-id> --output <private-request-file> --flush
clauderig queue enqueue <private-request-file>
clauderig queue status              # JSON progress and capacity
clauderig queue sync --flush        # manual sync; acknowledge fully covered current-account requests
clauderig queue run                 # supervised foreground worker
clauderig queue retry <batch-id>     # only after repairing a blocked batch
clauderig queue drain               # stop producers first; process ready work until idle
```

`prepare` reads producer identity once and saves one event ID/time, identity and
runtime scope, without transcript bytes. `--flush` includes every changed tail;
omit it for normal capture. Use `--unknown-identity` only when explicit unknown
attribution is intended. **Retry enqueue with the same saved file**, especially
after an uncertain result; never rerun prepare, edit its checksum/identity or
change its timestamp to retry. Keep the file through completion. Enqueue and
workers use saved attribution, never the worker's current login.

A worker’s first interrupt stops after its current batch; for `queue sync`, it
lets the single sync finish. A second cancels and waits for supervised cleanup. Blocked, delayed or interrupted drain returns nonzero without
dropping accepted work. Inspect status and repair the cause before retrying;
never reset/copy runtime children or bypass process fences/profile isolation.
Keep paths/links stable and Windows files under a private inherited ACL. Ordinary
sync does not acknowledge pending queue events. Use `queue sync` for explicit
manual acknowledgement: its profile selection must match every profile ordinary
sync discovers. `--dry-run` stages/scans without acknowledgement; `--flush`
includes all changed tails and does not read hook stdin. Privacy/shared-history
checks apply to previews too. Other identities and incomplete/attempted work stay
queued; inspect status. Opt-in hook routing and rollout rollback remain pending. Actual OS reboot/hibernation
validation remains a general-release gate.

### Worktree & PR discipline (the `clauderig guard` hook)

In a repo with the guard installed (`clauderig project install`), a PreToolUse hook
enforces:

- **Never use EnterWorktree/ExitWorktree, never `cd` out of the repo root** — both
  move the session's working directory and scramble Claude Code chat history (keyed
  to the folder path). Use absolute paths, `git -C <dir> …`, or a subshell
  `(cd <dir> && …)` instead.
- **Don't write code on `main`/`master`.** Run `rig worktree new <branch>` first — it
  makes a sibling checkout opened in a new VS Code window for review; this window
  stays put. Edit by absolute path, `git -C <worktree> …`, then push and open a PR.
- **Docs/root config may go on the base branch** — `*.md`, `docs/`, `.github/`, and
  top-level config (`*.toml`/`*.yml`/`*.json`, `LICENSE`, `.gitignore`).
- **Override** only when you must change code on base: `export CLAUDERIG_ALLOW_MAIN=1`
  or `touch .claude/allow-main`.

## Quick reference: which tool

- "build / test / run / format it", "what is this repo" → **rig**
- "record this change", "bump the version", "update the changelog" → **changerig**
- "publish", "cut a release", "tag" → **shiprig**
- "sync my Claude setup", "enqueue/retry/drain Claude sync", "make a worktree", "set up the guard" → **clauderig** / `rig worktree`

For v2 hook-input testing, `clauderig queue prepare --hook --output request.json`
reads one complete Stop/SessionEnd JSON document from stdin (EOF within 2 seconds,
128 KiB maximum). Preparation alone saves intent; enqueue the same file afterward
and on every retry. Use a new private file only for a new event. Do not install
this preparation-only command as an automatic hook. SessionEnd saves selected
flush intent for its transcript; malformed/empty input never requests all-flush.
Queued workers fully capture requested sessions and subagents. Selected paths
also flush their subagents; unrelated plain transcripts keep normal throttling
unless any batched request asks for all-flush. Chunked tails always flush.
Previously backed-up subagents remain retained if removed from the source.
The worker still freezes the configured source tree; saved artifacts replay
unchanged. Saved flush intent also governs manual-sync coverage.
It conflicts with `--session`/`--flush`, observes identity once, and sends success
to stderr. Installed hooks remain synchronous until the opt-in installer lands.
