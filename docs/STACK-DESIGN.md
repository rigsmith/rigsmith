# stack: a fused workspace of upstream forks

> Status: **in progress** (proposed 2026-08-25; first slice on `feat/ws` (verb since renamed `stack`) the
> same day — manifest, engine install, ephemeral proxy, init/pull/send/status/
> doctor). Direction settled: josh integration style (one fused history, not
> sibling clones). Adds a `rig stack` verb family: a
> workspace repo whose children are *prefixes in one real git history*, imported
> from and synced back to their upstream repos through
> [josh](https://josh-project.dev)'s reversible filters, driven the way
> [rust-lang/josh-sync](https://github.com/rust-lang/josh-sync) drives it — an
> ephemeral localhost `josh-proxy` plus plain git. Designed against a real
> reference workspace: three forked .NET repos — a pseudoterminal, a terminal
> emulator core, and the UI control that consumes both.

## The job

The recurring shape: **downstream contributor to a family of related repos.**
Three forks that only make sense together — a PTY library, a terminal emulator
core, and the Avalonia control that consumes both as NuGet packages. Two hard
requirements that every existing tool trades against each other:

1. **Atomic iteration.** One commit may span an API change in the PTY library
   and the consumer fix in the terminal control. One history, bisectable,
   with the workspace's own branches and worktrees.
2. **Clean upstream exits.** Any slice of that work must leave as an ordinary
   PR from an ordinary fork branch — correct parents, no rewritten-hash
   archaeology, upstream never knows the workspace exists.

Submodules and manifest tools (vcstool, west, repo…) satisfy 2 by giving up 1.
git subtree attempts both and fails 2 (`subtree split` drift, garbage hashes).
git-subrepo gets close but is history surgery in bash with known `git worktree`
friction. josh's reversible filter algebra is the only engine that delivers
both by construction — and rust-lang has been running exactly this shape in
production (Miri, rust-analyzer, stdarch ↔ `rust-lang/rust`) since mid-2026.

## What josh-sync settles (read its `src/josh.rs` — it's short)

The integration surface is **subprocess + URL + git**. No FFI, no library, no
server anyone administers:

- **The tool owns the engine.** josh-sync pins `JOSH_VERSION` (`r26.07.19`) as
  a constant and installs it itself:
  `cargo +stable install --locked --git https://github.com/josh-project/josh
  --tag <version> --root <app-data-dir> josh-proxy` (crate `josh-proxy` → bin
  `josh-proxy`; crate `josh-cli` → bin `josh-filter`). Users never install
  josh; version skew between engine and history can't happen.
- **Ephemeral proxy.** Spawn
  `josh-proxy --local <cache-dir> --remote=https://github.com --port=<p>
  --no-background` with output silenced; poll a TCP connect every 10ms (1s
  budget); SIGINT and wait on drop. Total lifecycle ~ a process handle.
- **Filter-in-the-URL.** All history math happens by fetching/pushing plain
  git against
  `http://localhost:<p>/<owner>/<repo>.git@<sha><urlencoded-filter>.git`.
- **Commit the sync cursor.** Last-synced upstream SHA lives in a committed
  file; a pull whose upstream SHA hasn't moved exits `NothingToPull`.
  Idempotent, reviewable, bisectable.
- **Push means a branch on *your fork*.** Extracted commits land on the
  contributor's fork; the PR happens from there. Upstream is never pushed.
- **Automate only the boring direction.** Reusable CI workflow crons the pull
  direction and opens sync PRs; outbound stays a deliberate human act.

Their admitted wart: the pull direction generates merge commits in the
extracted history. Named here so we choose merge strategy explicitly per child
rather than inheriting it.

## The model

A stack workspace is **one ordinary git repo**. Each upstream project is a
prefix (`pty-core/`, `term-core/`, …) whose contents were imported through
josh's `:prefix=` filter, plus workspace-owned glue at the root: the manifest,
the ecosystem overlay (`.slnx` + `Directory.Build.targets` for .NET — see
appendix), CI. Branches, worktrees (`rig worktree` composes for free — they
are just branches of one repo), stashes, bisect: all normal git.

```jsonc
// rig.stack.jsonc  (discovery via cfgfind; also embeddable as "ws" in .rig.json)
{
  "$schema": "https://rigsmith.dev/schemas/rig-stack.json",
  "josh": "r26.07.19",              // engine pin; overrides rig's built-in default
  "repos": {
    "pty-core": {
      "upstream": "github.com/acme/pty-core",   // host/owner/name — no scheme, no .git
      "fork":     "github.com/you/pty-core",
      "upstreamBranch": "main"
    },
    "term-core":    { /* … */ },
    "term-control": { /* … */ }
  },
  // Machine-written pull cursors, committed with each pull's merge commit.
  // A separate top-level map, not a field per repo: pulls rewrite this one
  // value through the comment-preserving jsonc editor (which reaches depth ≤2)
  // while the human-authored entries above stay byte-for-byte untouched.
  "lastSync": { "pty-core": "8f3c2ab…" }
}
```

### Verbs

| verb | does |
|---|---|
| `rig stack init` | scaffold the manifest; for each repo, import upstream history under its prefix (proxy fetch through `:prefix=<child>`, merge, set cursor); scaffold the ecosystem overlay |
| `rig stack pull [child]` | fetch upstream through the filter; `NothingToPull` if the cursor matches; else merge (strategy per child), update cursor. The CI-cronnable direction |
| `rig stack send <child> <new-branch>` | commit `<child>/`'s tree onto that project's upstream tip and push it to the **fork** as `branchPrefix + <new-branch>` (default `stack/`): one commit, whose diff is exactly what the workspace changed. The deliberate direction |
| `rig stack status` | per child: upstream commits since cursor, local commits touching the prefix not yet sent, cursor SHA |
| `rig stack doctor` | engine installed + version matches pin, remotes reachable, manifest sane; `--fix` installs/updates josh (cliguard requires `--fix` on any doctor) |

`rig build` / `rig test` at the root need nothing new: the workspace root *is*
a repo with a `.slnx` — existing ecosystem detection already resolves it.

### Engine management

rig pins a default josh version as a constant (overridable per-workspace via
the manifest's `josh` key) and installs to
`~/.local/share/rigsmith/josh/<version>/bin/`. `stack doctor --fix` performs
install/update; every verb that needs the engine triggers the same path on
first use.

**Distribution** (the fresh-machine problem): upstream josh ships source and
Linux Docker images only — no binaries, and no stated Windows support. A
machine with only the rig binary therefore can't get an engine from upstream.
So rig's own release pipeline builds the pinned version per platform
(linux/macos/windows matrix running the josh-sync cargo recipe) and publishes
the binaries checksummed alongside rig's releases; `doctor --fix` *downloads*
the prebuilt for GOOS/GOARCH and only falls back to a local
`cargo install --tag <pin>` when no prebuilt exists (the current first-slice
behavior, requiring cargo, with an honest it-takes-minutes message). This
also makes Windows support a CI fact rather than a user-machine gamble:
until the windows job compiles josh green, `rig stack` on Windows reports
"engine not available prebuilt — use WSL or install rust", instead of
implying a toolchain dance that may dead-end.

### Proxy lifecycle (Go)

`os/exec` spawn with the four flags above; random free port (not josh-sync's
fixed 42042 — two rig invocations must not collide); readiness = TCP poll;
teardown = SIGINT then wait with timeout, SIGKILL fallback. One proxy per verb
invocation, never a daemon. `--local` cache under
`~/.cache/rigsmith/josh/<workspace-hash>/`.

### How `send` builds a branch, and why not with josh

Reverse-filtering the workspace's commits through josh is the obvious route, and
it works: `-o base=<branch> -o create -o edit` produces correctly re-rooted
commits on the fork. It is not what ships, because the branch it produces
carries the workspace's own history — its root commit, and its imports of
*other* projects — into a pull request where none of that means anything.

What ships is simpler and needs no engine at all. A workspace already stores
each project as a subtree, so `HEAD:<child>` **is** the tree upstream wants,
with the prefix already absent inside it. `send` therefore:

1. resolves `HEAD:<child>` — the tree,
2. asks the upstream for its branch tip and fetches that commit's objects,
3. `commit-tree`s the tree onto that tip with a message,
4. pushes that commit to the fork's `<branch>`.

The result is one commit whose diff is exactly the workspace's changes to that
project, rooted on current upstream, with no sign that the repo is fused with
anything. It also matches what projects with a one-commit-per-PR convention
(josh's own included) actually want.

The trade is granularity: several workspace commits touching one project arrive
as one. That is the right default for a pull request; preserving them is what
the josh path is for, and it can return as `--preserve-history` if anyone wants
it.

## Wiring into rig (from the codebase survey, 2026-08-25)

- **Verb name `stack` — not `workspace`, not `josh`.** It names the thing (a
  stack of upstream forks fused into one history), not
  the engine (rig's build-not-dotnet rule) and not "workspace", which already
  means intra-repo package discovery in this package
  (`internal/rig/cli/workspace.go`, `detect.hasWorkspaceManifest`). Files:
  `stack.go`, `stackmanifest.go`, `stackjosh.go`, `stack_test.go` — the
  `workspace*` namespace stays untouched. (`ws` was the working name of the
  first slice; rejected as bland and workspace-adjacent.)
- Register `newWsCmd()` via `extraCmds()` in `internal/rig/cli/extras.go` —
  the documented home for heavier standalone commands.
- **cliguard compliance** (hard-fail CI in `internal/cliconsistency`): the
  group gets a `RunE` driving `climenu` (a bare group with children fails the
  `group-menu` rule); no `--list` flags; reserved shorthands
  (`-n/-y/-f/-i/-k/-a/-w/-m`) respected; `stack doctor` has `--fix`.
- Manifest discovery: a `cfgfind.Spec` (the shiprig `releaseConfigSpec`
  pattern) — probes `rig.stack.jsonc`/`.json` at the workspace root, optionally
  `RigPath`/`RigKeys` for inline-in-`.rig.json`, loud error on duplicates.
  Parse `core/jsonc`, write `core/confkit.Writer` with a new
  `site/public/schemas/rig-stack.json`.
- Git operations through `core/gitrepo` (`Clone/Fetch/FetchMerge/AheadBehind/
  Conflicts…`) — it is "a thin shell over system git", which is exactly what
  the josh pattern wants. Nothing new in core except possibly a
  `gitrepo.FetchURL`-style helper if fetching from a raw URL (no named remote)
  isn't covered.
- Tests: white-box `_test.go` beside the code, prose-named `t.Run` subtests,
  per-package `mustGit(t, dir, …)` over `t.TempDir()` (the
  `core/gitrepo` tests are the model). The proxy runner is tested against a
  fake `josh-proxy` shell script that opens the port; filter round-trip tests
  are gated behind the real binary's presence (skip when absent).
- **Relationship to roadmap.md's "worktree hub (own binary)" item:** that hub
  is a *dashboard* over parallel dev (worktrees, PR status, agent ownership).
  `stack` is *state and sync* — manifest, import, cursor, send. Different concern;
  if the hub materializes it reads stack workspaces like any other repo. The
  hub's open question "where does cross-repo state live" gets an answer here:
  in the workspace repo, committed.

## Open questions

1. **Auth on `send`.** The proxy fronts `https://github.com`; pushing the
   fork branch through it means git credentials for `http://localhost:<p>`
   forwarded by josh (rustc contributors do this with PATs). John's flow is
   SSH. Two candidate answers, spike decides: (a) credential passthrough with
   a scoped PAT; (b) reverse-filter locally via `josh-filter`, then plain
   `git push git@github.com:…` over SSH — no credentials near josh at all.
   (b) is the better shape if the filter invocation is tractable.
2. Merge strategy per child on `pull` (merge vs squash vs rebase-ish), given
   the known merge-noise wart. Default merge, per-child override in manifest?
3. `stack init` adopting an existing non-fused workspace (the sibling-clone
   layout people already have): re-import and keep the overlay, or not worth
   the code?
4. CI template (`stack init --ci github`) in v1 or after the verbs settle?

## Appendix: the MSBuild overlay

Verified against the reference workspace (2026-08-25): none of its three
upstream repos owns a root `Directory.Build.targets`, so a workspace-root one
reaches every child project via MSBuild's walk-up and swaps the cross-repo
`PackageReference`s for `ProjectReference`s with **zero upstream-file edits**.
Ordering matters (conditions read the package refs before `Remove` deletes
them); conditioning on `AnyHaveMetadataValue('Identity', …)` rewires only
actual consumers.

```xml
<Project>
  <ItemGroup Condition="'$(UseWorkspaceProjects)' != 'false'">
    <ProjectReference Include="$(MSBuildThisFileDirectory)pty-core/src/Pty.Core/Pty.Core.csproj"
                      Condition="@(PackageReference->AnyHaveMetadataValue('Identity', 'Pty.Core'))" />
    <PackageReference Remove="Pty.Core" />
  </ItemGroup>
</Project>
```

Checked with `dotnet msbuild -getItem:ProjectReference,PackageReference`:
default evaluation swaps `Pty.Core`/`Term.Core` for project refs;
`-p:UseWorkspaceProjects=false` restores the against-real-packages build that
upstream CI sees. The walk-up ignores git boundaries, so building a child from
inside its folder also gets the overlay — right for the dev loop; use the flag
for pristine verification. Node/Go equivalents (workspaces/overrides,
`go.work`) slot into the same "overlay scaffolded by `stack init`" position.

### Declaring the swaps once (2026-08-26)

The form above repeats each package name three times per dependency — in the
path, in the condition, and in the `Remove`. That is fine for two libraries and
tiresome for ten, and every repetition is a place to typo a name into a silent
no-op.

The obvious fix, `%()` batching over a declared list, does **not** work: item
batching is only available inside a `Target`, and a `Directory.Build.targets`
puts its `ItemGroup`s at project level. The batched `Include` expands to nothing
and reports no error, which is the worst of both.

Set arithmetic gets there without batching. `Exclude` matches on identity, so
the sources a project does not reference can be subtracted, and the complement
is exactly the set to swap:

```xml
<Project>
  <ItemGroup>
    <StackSource Include="Pty.Core"  Path="pty-core/src/Pty.Core/Pty.Core.csproj" />
    <StackSource Include="Term.Core" Path="term-core/src/Term.Core/Term.Core.csproj" />
  </ItemGroup>

  <ItemGroup>
    <_StackAbsent  Include="@(StackSource)" Exclude="@(PackageReference)" />
    <_StackPresent Include="@(StackSource)" Exclude="@(_StackAbsent)" />
    <ProjectReference Include="@(_StackPresent->'$(MSBuildThisFileDirectory)%(Path)')" />
    <PackageReference Remove="@(StackSource)" />
  </ItemGroup>
</Project>
```

Item *transforms* are fine at project level — it is only batching that is not —
so `@(_StackPresent->'…%(Path)')` evaluates as intended.

Beyond brevity this removes a real trap. Hand-written conditions get written
against `%(Filename)`, which MSBuild splits at the **last dot**: `Pty.Core.Native`
has the `Filename` `Pty.Core`, so such a condition matches the wrong package and
looks like it worked. The set form never names a metadata field, so it cannot go
wrong that way.

Verified against four projects: one referencing `Pty.Core`, one referencing
`Pty.Core.Native` only, one referencing both, one referencing neither. Each got
exactly its own swaps and no others.

This is the shape `rig stack adopt` should generate, since it is the one a human
can extend by adding a line.
