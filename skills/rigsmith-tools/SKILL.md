---
name: rigsmith-tools
description: >
  Use the rigsmith CLI family — rig (convention-first dev launcher: build/test/run/
  format/lint/typecheck/coverage/kill/worktree across .NET, Node, Go, Rust),
  changerig (changesets), shiprig (releases/publish), and clauderig (sync Claude Code
  config across machines + worktree/PR guard). Invoke whenever the work involves
  building/testing/running/formatting a project, managing changesets or changelogs,
  cutting or publishing a release, creating worktrees/branches, syncing Claude Code
  setup, finding or reopening a past Claude Code session, or working across several
  forked repos fused into one history (`rig stack`), publishing a manifest home,
  setting up a seed locally or in CI, reading a member's .rig-stack pointer, stack CI warning guards, speculative topic PR testing, topic PR dependencies,
  or .NET source signals (STACK_*, RigStackSource_*, RigStackAvailable_*, RIGSTACK001) —
  even if the user names a raw
  tool (go/dotnet/npm/cargo) instead of rig.
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
| `clauderig` | Claude Code config sync + guard | syncing `~/.claude` across machines, worktree/PR discipline |

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

## rig stack — several forked repos in one history

A **stack workspace** fuses upstream repos you maintain forks of into one git
history, each under its own directory (`pty-core/`, `term-core/`, …). A change
spans them in one commit and the build compiles against source; each project
still leaves as an ordinary PR to its own upstream. `rig.stack.jsonc` at the
workspace root names the repos.

```sh
rig stack setup [seed]                  # explicit seed opts into a manifest home; no arg preserves config
rig stack seed [dir]                    # export root files, or retry configured home publication with no dir
rig stack seed --publish acme/rigstack-app@main   # opt in and publish root-only commits
rig stack ci --seed acme/rigstack-app@main --member app # isolated committed-checkout CI
rig stack ci --manifest rig.stack.jsonc --member app # alternative committed manifest
rig stack ci-guard app/App.csproj       # observed .NET build warning policy
rig stack deps [--publish]              # check topic dependencies; optionally post statuses
rig stack deps --seed acme/rigstack-app@main [--publish] # read proposals from seed
rig stack home [pointer-file]           # read a member's .rig-stack reference
rig stack status                       # each repo's cursor vs its upstream tip
rig stack pull [repo]                  # take upstream's new commits (all repos by default)
rig stack send <repo> <new-branch>     # ALL that repo's changes → a branch on the fork
rig stack propose <repo> <b> --from <topic>  # ...or one topic branch (needs trackBranch)
rig stack init                         # scaffold the manifest; run again to import
rig stack pack <repo>                  # build that member's packages HERE (overlay in effect)
rig stack doctor --fix                 # install the josh engine if missing
```

A manifest home is a small seed repo holding the manifest, README and root
overlays. Use the same seed reference for local setup and CI: owner/repo@ref
means GitHub, while URLs and existing local paths are also accepted. For local
publication use a bare repo or a branch that is not checked out. Do not combine
a seed directory with --publish. Private seed access needs Git credentials
(a workflow-configured token in CI).

Pull publishes committed root updates even when no member moved; propose
publishes after recording its branch, or defers if the manifest was already
dirty. Publication uses an independent clone, never fused objects. If the seed
moved, reconcile separately; if access or publication fails, local work is kept.
A post-pull publication failure returns non-zero and explicitly says the local
pull succeeded. Retry with rig stack seed once the problem is fixed.

**You are usually working inside one.** Detect it: a `rig.stack.jsonc` at the
git top level, or directories that match its `repos` keys. If so:

- **Commit across projects freely.** That is the point — one commit may touch
  `pty-core/` and `term-control/` together. Do not split it "so each repo gets
  its own commit"; `send` does that split for you, correctly.
- **Never `dotnet pack` / `npm pack` a member by hand, and never from a checkout
  of a proposed branch.** Cross-member references resolve through the build
  overlay, which only exists here; a bare checkout's restore fails, sometimes
  with no message at all. Use `rig stack pack <repo>`.
- **Never `git push` from the workspace.** Let rig manage the seed home; it
  holds several rewritten upstream histories fused together. Work leaves through
  `rig stack propose` (a fork you contribute to) or `rig stack push` (a repo of
  your own, marked `owned`) — never through git directly.
- **Keep the worktree clean** before `init` and `pull` — they refuse a dirty tree
  anywhere, because an import stages everything and would swallow stray edits.
  `propose` is narrower: it refuses only uncommitted changes **under the member
  being proposed**, since those are the ones that would silently not be in what
  you send. Unrelated edits elsewhere do not block it.
- **Relay what `propose` says about pins.** It ends by naming any package the
  member gets from a sibling in the stackspace rather than from a feed. Those
  are the pins a plain checkout of the proposed branch cannot restore — so if
  someone is about to build or pack that branch outside the stackspace, that
  note is the answer to the failure they are about to hit. Do not treat it as
  an error: it is the normal shape of a stackspace proposal.

### CI and source-aware code

Use `ci` to build/test a committed member checkout against sibling sources in a
temporary stackspace; keep plain-clone CI too. Exactly one of `--seed` or
`--manifest` is required: seed accepts owner/repo@ref, URL, scp or a local path;
manifest reads a committed checkout file. CI never adopts or publishes a home
and never infers the seed from a member's `.rig-stack` pointer. Seed hosts obey
`--allowed-hosts`; private seeds use configured Git credentials and can be
unavailable to fork PRs. Summary/provenance records the sanitized seed and commit.
`stack deps --seed <ref>` reads proposal records from the same source; it is
exclusive with explicit `--manifest` and uses the same host allowlist.
For CI, `--member` selects the prefix (otherwise origin matching), `--build`
and `--test` run bash commands from it. `--upstream-tip` opts out of recorded
cursors; `--cache-history=false` disables trusted history reuse. Exit codes are
10 setup, 11 wire, 12 build, 13 test.

`--topic <name>` (action input `topic`) tests every open proposal PR head in
that manifest topic together. Missing, closed, untrusted or known conflicting
PRs fail setup. Head repositories must match declared fork/upstream repositories.
Use read-only `pull_request` jobs without secrets: code from every participating
PR runs. Name the job `stack topic`; the check covers only its triggering PR,
so run it in each member and rerun after sibling heads move. Topic mode disables
shared caches in the action and cannot combine with upstream-tip. See the
[stack guide](https://rigsmith.dev/rig/stack#testing-a-complete-topic) for trust,
cursor and provenance semantics. No merge train is installed.
The scrubbing guarantee applies to `rig stack ci --topic` and its Git, seed,
init, wire, guard, build and test subprocesses. The action skips its later
`rig stack deps --publish` step for topic runs so it cannot reintroduce a token
on that runner after untrusted code executes. Non-topic dependency reporting
still receives `GITHUB_TOKEN`; run any required topic dependency publication
in a separate fresh trusted metadata-only job.
Topic mode also needs `pull-requests: read` alongside `contents: read` to
read private PR metadata in the job's own repository. A job's `GITHUB_TOKEN`
does not gain access to private sibling repositories from these permissions.
Private sibling PRs need separately authorized metadata access; without it,
lookups are unavailable ("could not check") and topic setup fails rather than
testing a reduced set. Do not expose a broader token to PR build commands.
Topic CI removes token environment variables before children run and refuses
persisted checkout HTTP extraheaders; set `persist-credentials: false`. Use a
reviewed immutable seed (`--seed acme/stack-policy@<full-commit-sha>`, action
input `seed`) for an independently trusted proposal set. Topic tokens are scrubbed
before seed cloning too; use a public seed or separately prefetched local seed
for private policy, with preparation credentials removed before the build.

Use `ci-guard <project.csproj>` for an observed .NET build: the member's own new
warnings fail except declared stack-only warnings or existing tolerances;
sibling warnings remain visible and non-fatal unless `--strict`. Unattributed
NU/MSB infrastructure warnings fail closed. `ci --guard-project` replaces only
the build step; the separate test command has its own build policy. Guard the
test project and use `dotnet test <project> --no-build` with matching outputs.

Use `deps` to check committed topic `dependsOn` edges after proposals, merges or
cursor updates. `--publish` posts statuses only to PR head SHAs in
`GITHUB_REPOSITORY` (a member upstream). Pending/unavailable dependencies are
reported without failing the command; denied writes warn. It installs no sweep.

For .NET source-only APIs, use compile-time `#if STACK_ACME_CORE`: this assembly
includes that sibling's source directly or transitively. `RigStackSource_*`
properties exist only in targets after `RigStackSourceSignals`.
`RigStackAvailable_*` is early and stack-wide, suitable for project-file
conditions; it must never guard source-only APIs. `-p:UseStackSources=false`
disables swaps and all these signals. `RIGSTACK001` is a hard failure when SDK
reference preparation loses candidates: update rig or use a compatible SDK;
do not suppress it and silently omit guarded code/tests. See
[source signals](https://rigsmith.dev/rig/stack#source-signals-from-the-net-overlay)
for naming, conditional-reference limits and incremental/no-build behavior.

The local composite at `.github/actions/stack/` accepts `seed` or `manifest` (exactly one), `member`,
`rig-version`, `rig-sha256`, `allowed-hosts`, `build`, `test`, `guard-project`,
`strict`, `dependencies`, `topic`, `upstream-tip`, `cache-history`, and `cache-packages`.
Seed-mode history/package keys include a hash of the seed reference and the
run ID/attempt and job ID/matrix index so partial restores can save updated bundles; both caches remain
disabled for fork PRs. Dependency reporting accepts the seed too.
Pin rig and reviewed action SHAs. Use ordinary `pull_request`, read-only contents,
and checkout `persist-credentials: false`; never execute PR code under
`pull_request_target`. Optional status publication needs `statuses: write`.
See [action inputs and cache policy](https://github.com/rigsmith/rigsmith/tree/main/.github/actions/stack)
and [CI guidance](https://github.com/rigsmith/rigsmith/blob/main/docs/GITHUB-ACTIONS.md).

Dry runs: `ci`, `ci-guard`, `deps` (even without `--publish`), setup writers and
offline seed exports refuse `-n`; `home` is read-only. Seed publication previews
without cloning/pushing, retaining local safety checks. See the
[dry-run contract](https://rigsmith.dev/rig/verbs) before previewing other verbs.

### Four different things called "branch"

Read this before touching a manifest or writing a `propose` command. They are
unrelated to each other, and they live in three different repositories:

| | What it is | Where it lives |
|---|---|---|
| `upstreamBranch` | the branch of **upstream** a directory follows — what `pull` takes, what `propose` roots on | the manifest, per repo, default `main` |
| `<new-branch>` | a branch **you create on your fork** for one change — the pull request | the `propose` argument, named per change |
| `branchPrefix` | what `propose` prepends to that argument, default `stack/` | the manifest, workspace-wide or per repo |
| `--from <topic>` | a branch **of the stackspace itself**, holding one in-flight fix | your local stackspace; `stack-pr-<name>` by convention |
| `trackBranch` | a branch **of your fork** holding everything the prefix carries, so a rebuild is not short | the manifest, per repo; rig writes it when `--from` is used |

A manifest never names the branch `propose` creates, because that is a property
of the change rather than of the project. (`branch` is the old name for
`upstreamBranch` and is still read.)

The two prefixes are deliberately different spellings: `stack/` is a pull
request **on your fork**, `stack-pr-` is work in progress **here**.

### Sending work upstream

```sh
rig stack send pty-core read-timeout -m "Fix the read timeout"
# → pushes stack/read-timeout: one commit on your fork, rooted on upstream's
#   tip, containing only that project's files at their real un-prefixed paths.
#   Open the PR from the fork.
```

**It sends the whole prefix, not the change you have in mind.** The branch carries
everything the stackspace holds for that project — including a fix already waiting in
another pull request, whose changes would then show up in this one too. Fine while one
thing is in flight, wrong as soon as two are.

Keep each in-flight fix on its own **topic branch of the stackspace**, rooted on the
commit that imported that member; `main` merges them and stays the fused line you build
and test. Then propose one at a time:

```sh
git switch -c stack-pr-reader-wedge <the import commit>   # recommended naming
# ...fix, commit...
git switch main && git merge stack-pr-reader-wedge
rig stack propose term-core reader-wedge --from reader-wedge
```

A topic rooted on the import holds upstream plus its own change and nothing else, so
its tree is what upstream should see — no patch to replay, nothing to fail to apply as
histories intertwine. A topic branched off another unmerged fix contains it too, and
`propose` reports the commits it is sending so you see that. `stack-pr-<name>`
is a convention, not a rule: an exact branch name always wins, the conventional one is
the fallback.

`--from` requires `trackBranch` in the manifest, and keeps that branch current with the
whole divergence — a topic is only part of it, and `init` rebuilds from it, so without
that a rebuild (CI included) would silently build without the fixes you left out.
`rig stack status` says when `propose` would send a prefix's whole divergence, and lists the
`stack-pr-*` topics in flight.

Pass the **short name** — `read-timeout`, not `stack/read-timeout`. `send`
prepends `branchPrefix` (default `stack/`) so these branches stay apart from the
user's own work on the same fork — it identifies them, it does not reserve them. A name that already carries the prefix is left
alone, so re-sending with the full branch name still works. The output line
names the branch that was actually created; use that when opening the PR.

A workspace commit touching three projects becomes three `send` calls, one per
project. Sending twice to the same branch **updates** it, so review feedback is
a commit plus a re-send.

`send` **refuses when upstream has moved** past the recorded cursor: rooting a
stale tree on a newer tip would produce a PR that silently reverts whatever
landed in between. When you see that, `rig stack pull <repo>` and send again —
do not work around it.

Full guide: <https://rigsmith.dev/rig/stack>.

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

changerig matches canonical @changesets (v3) exactly, so two things surprise:
`version` with no pending changesets **exits 1** (check `status` first in a
script), and a `"private": true` package is **treated as ignored** — never
versioned, and a changeset mixing it with a public one fails — unless the config
sets `"privatePackages": { "version": true }`. An app that is private only to
stay off npm but still releases (an Electron app) needs that key.

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

**Resuming a failed release:** use the step it names (`Resume with: shiprig
release --from <step>`), never a later one — a `--from` past it is refused,
because the steps in between never ran (`publish` ships what `build` made).
Don't reach for `--force` to get past that. And never judge a release by a
piped exit code: `shiprig release … | tail` always "exits 0" (that was #419);
use `set -o pipefail` or no pipe.

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
a tripwire that fails loudly if one slips), and refuses any remote `gh` can't confirm
is private. A manual copy leaks secrets and breaks paths across machines.

**Relationship to this skill:** `clauderig guide` maintains a *brief, always-on*
"rigsmith tools" block in `CLAUDE.md`. This skill is the *deeper, on-demand*
reference — they complement each other; keep them consistent if you edit one.

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
- "sync my Claude setup", "make a worktree", "set up the guard" → **clauderig** / `rig worktree`
