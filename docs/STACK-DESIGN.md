# stack: a fused stackspace of upstream forks

> Status: **in progress** (proposed 2026-08-25; first slice on `feat/ws` (verb since renamed `stack`) the
> same day — manifest, engine install, ephemeral proxy, init/pull/send/status/
> doctor). Direction settled: josh integration style (one fused history, not
> sibling clones). Adds a `rig stack` verb family: a
> stackspace repo whose children are *prefixes in one real git history*, imported
> from and synced back to their upstream repos through
> [josh](https://josh-project.dev)'s reversible filters, driven the way
> [rust-lang/josh-sync](https://github.com/rust-lang/josh-sync) drives it — an
> ephemeral localhost `josh-proxy` plus plain git. Designed against a real
> reference stackspace: three forked .NET repos — a pseudoterminal, a terminal
> emulator core, and the UI control that consumes both.

## The job

The recurring shape: **downstream contributor to a family of related repos.**
Three forks that only make sense together — a PTY library, a terminal emulator
core, and the Avalonia control that consumes both as NuGet packages. Two hard
requirements that every existing tool trades against each other:

1. **Atomic iteration.** One commit may span an API change in the PTY library
   and the consumer fix in the terminal control. One history, bisectable,
   with the stackspace's own branches and worktrees.
2. **Clean upstream exits.** Any slice of that work must leave as an ordinary
   PR from an ordinary fork branch — correct parents, no rewritten-hash
   archaeology, upstream never knows the stackspace exists.

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

A stackspace is **one ordinary git repo**. Each upstream project is a
prefix (`pty-core/`, `term-core/`, …) whose contents were imported through
josh's `:prefix=` filter, plus stackspace-owned glue at the root: the manifest,
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
| `rig stack propose <child> <new-branch>` | commit `<child>/`'s tree onto that project's upstream tip and push it to the **fork** as `branchPrefix + <new-branch>` (default `stack/`): one commit, whose diff is exactly what the stackspace changed. The deliberate direction |
| `rig stack status` | per child: upstream commits since cursor, local commits touching the prefix not yet sent, cursor SHA |
| `rig stack doctor` | engine installed + version matches pin, remotes reachable, manifest sane; `--fix` installs/updates josh (cliguard requires `--fix` on any doctor) |

`rig build` / `rig test` at the root need nothing new: the stackspace root *is*
a repo with a `.slnx` — existing ecosystem detection already resolves it.

### Engine management

rig pins a default josh version as a constant (overridable per-stackspace via
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
`~/.cache/rigsmith/josh/<host>/`, keyed by the upstream host rather than
by the stackspace, so several stackspaces fusing the same forge share objects.

### How `propose` builds a branch, and why not with josh

Reverse-filtering the stackspace's commits through josh is the obvious route, and
it works: `-o base=<branch> -o create -o edit` produces correctly re-rooted
commits on the fork. It is not what ships, because the branch it produces
carries the stackspace's own history — its root commit, and its imports of
*other* projects — into a pull request where none of that means anything.

What ships is simpler and needs no engine at all. A stackspace already stores
each project as a subtree, so `HEAD:<child>` **is** the tree upstream wants,
with the prefix already absent inside it. `propose` therefore:

1. resolves `HEAD:<child>` — the tree,
2. asks the upstream for its branch tip and fetches that commit's objects,
3. `commit-tree`s the tree onto that tip with a message,
4. pushes that commit to the fork's `<branch>`.

The result is one commit whose diff is exactly the stackspace's changes to that
project, rooted on current upstream, with no sign that the repo is fused with
anything. It also matches what projects with a one-commit-per-PR convention
(josh's own included) actually want.

The trade is granularity: several stackspace commits touching one project arrive
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
- **The noun is `stackspace` (2026-08-27).** The verb decision above kept
  `workspace` out of the *command* namespace and then the prose used it for the
  fused thing anyway, which put it back in collision with
  `internal/rig/cli/workspace.go` — and with npm workspaces, `go.work` and
  VS Code, which a reader arrives already knowing. "Stackspace" is a coinage,
  and coinages cost a definition each time; this one buys a word that means
  exactly one thing in this tool and nothing anywhere else. Renamed across the
  code, the schema descriptions, error strings, both READMEs and the docs.

- Register `newWsCmd()` via `extraCmds()` in `internal/rig/cli/extras.go` —
  the documented home for heavier standalone commands.
- **cliguard compliance** (hard-fail CI in `internal/cliconsistency`): the
  group gets a `RunE` driving `climenu` (a bare group with children fails the
  `group-menu` rule); no `--list` flags; reserved shorthands
  (`-n/-y/-f/-i/-k/-a/-w/-m`) respected; `stack doctor` has `--fix`.
- Manifest discovery: a `cfgfind.Spec` (the shiprig `releaseConfigSpec`
  pattern) — probes `rig.stack.jsonc`/`.json` at the stackspace root, optionally
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
  if the hub materializes it reads stackspaces like any other repo. The
  hub's open question "where does cross-repo state live" gets an answer here:
  in the stackspace repo, committed.

## Open questions

1. ~~**Auth on `propose`.**~~ *Settled — both halves, and differently.*

   `propose` never touches the proxy: it builds the branch locally with
   `commit-tree` and pushes to the fork with plain git, so the user's existing
   remote credentials apply and nothing goes near josh. That is candidate (b),
   and it fell out of the `propose` design rather than needing a spike.

   Fetching is candidate (a), because it has to be: the proxy is what talks to
   upstream, so a private repo is only reachable if the proxy is given
   credentials. josh-proxy already supports this — it takes HTTP Basic from its
   client and uses it for the upstream fetch — and rig was presenting none, so
   every private upstream answered 401 (`auth=Handle { value: None }` in the
   engine log). rig now resolves the credential through `git credential fill`,
   so whatever the user already configured answers, and passes it as a
   URL-scoped `http.extraHeader` in the environment. Not a PAT rig stores, and
   not in argv: `git -c` would put the token in every process listing, and the
   git runner quotes its arguments into the error it returns on failure.
2. Merge strategy per child on `pull` (merge vs squash vs rebase-ish), given
   the known merge-noise wart. Default merge, per-child override in manifest?
3. `stack init` adopting an existing non-fused stackspace (the sibling-clone
   layout people already have): re-import and keep the overlay, or not worth
   the code?
4. CI template (`stack init --ci github`) in v1 or after the verbs settle?

## `push`: exporting a member with its history (2026-08-26)

### The gap

`propose` builds **one commit** from a prefix's tree at HEAD, rooted on the upstream
tip, and pushes it to a fork:

```go
tree, _ := repo.RevParse(ctx, "HEAD:"+name)
// "Root it on the upstream tip so the branch's one commit shows only what changed"
```

That is right for a pull request to somebody else's project — a reviewer wants
the change, not your afternoon. It is wrong for a repo you own. Your app's own
history flattens to a single commit every time it leaves the stackspace, and the
messages, the bisect points and the authorship go with it.

Today that is survivable, because the layout most people are told to use keeps
their app *outside* the stackspace, where it is pushed with ordinary git. If the
stackspace becomes the one supported path — everything inside, one overlay, no
topology question — then every commit to your own repo goes through `propose`, and
the squash stops being an acceptable trade.

**So this verb is a prerequisite for standardising on the single-repo layout,
not a follow-up to it.**

### Mechanism

`:prefix=<name>` nests a repo under a directory; `:/<name>` extracts it. They are
exact inverses, which josh states in its own optimiser:

```rust
// Check for special case: Subdir + Prefix that cancel
[Op::Subdir(p1), Op::Prefix(p2)] if p1 == p2 => …
```

So the export is: apply `:/<name>` to the stackspace history, take the resulting
ref, push it to the member's own repo. No `commit-tree`, no squash — the filtered
history *is* the member's history.

A commit that touches three prefixes becomes one commit in each of the three
repos, carrying the same message. That is the desired behaviour and worth saying
out loud: an atomic change in the stackspace lands as a matching commit in every
repo it touched, which is as close to atomic as separate repos allow.

### The identity question, settled by spike

**The filtered commits must be byte-identical to upstream's for shared history**,
or the push is not a fast-forward and the member's repo gets a parallel history
instead of a continuation. The inverse-filter property gives matching *trees*;
commit ids also depend on parents, and rig imports by **merging unrelated
histories**, so every stackspace commit near an import is a merge whose other
parents belong to other prefixes. Whether filtering collapses those back to
upstream's linear shape and reproduces its ids was the one thing that could have
sunk the design.

Spiked against a real three-member stackspace. It holds.

Filtering a member with no local work reproduces the cursor exactly:

```
$ josh-filter ':/live-markdown' --update refs/heads/extracted HEAD
3c76f5629d1c7cb78d51cd4d8cf36d9c6c1bf42f      <- the recorded cursor, byte for byte
```

Filtering a member carrying one stackspace commit continues upstream's history
rather than restating it:

```
$ josh-filter ':/<app>' --update refs/heads/out HEAD
$ git log --format="%h %p  %s" refs/heads/out
0b001bd3 ee55c4a6  build against the fused sources     <- the stackspace commit, message intact
ee55c4a6 8d0bdf66  Route the wheel once per document   <- upstream's tip, unchanged id
```

The new commit's parent *is* upstream's tip, so the push fast-forwards. The
message survives, which is the entire point the squash gives up.

A commit spanning two members splits correctly. One stackspace commit touching
both the stackspace-root overlay and the app's own file produced, in the app's
filtered history, a commit containing only the app's half, with the prefix
stripped. A stackspace commit touching *no* file under a member produced no
commit in that member at all — dropped as empty, which is what open question 2
predicted.

### Semantics

- **Which members qualify.** Only one you own, where the export target is the
  member's own tracked branch rather than a PR branch. That is a manifest fact
  rig cannot infer: `upstream == fork` is suggestive but a legitimate fork
  arrangement can look the same. A per-repo `"owned": true` states it; the
  target is that repo's `upstream` on its `upstreamBranch`, the same pair `pull`
  follows, so there is no second remote or branch to configure.
- **The cursor must advance.** This is the difference from `propose` that is easy to
  miss. `propose` pushes a branch nobody tracks, so the cursor is untouched. `push`
  moves the member's *tracked* branch — so immediately afterwards upstream has
  moved, `status` would say "upstream moved", and `pull` would try to merge in
  history the stackspace already contains. `push` therefore advances the cursor to
  what it just pushed, in the same commit that records it, exactly as `pull`
  does.
- **Fast-forward only.** Refuse otherwise. A member whose upstream moved since
  the cursor is the existing `propose` guard and applies unchanged: pull first.
- **`propose` stays.** Two verbs, because they answer different questions — "propose
  this to someone" and "this is mine, take it". Collapsing them into one with a
  flag would hide which one you are getting.

### Naming

`rig stack push <name>` parallels `git push` and reads correctly: push this
member to its own repo. The tension is that the page tells you never to give the
stackspace a remote and push it — but that is about pushing *the stackspace*, and
this pushes a member, so the distinction is the one the reader needs anyway.
`export` and `sync` were considered; `export` suggests a file, and `sync`
suggests bidirectional.

### Prerequisites

`josh-filter` is a separate binary from `josh-proxy`, and rig downloads only the
proxy. The pinned release already publishes it for all six platforms —
`josh-filter-<version>-{linux,macos}-{arm64,x64}` and
`windows-{arm64,x64}.exe` — so this is extending the existing fetch and its
pinned checksums, not new pipeline work. The source-build fallback needs a second
`cargo install` target alongside `josh-proxy`.

### Open questions

1. Does `push` need a range, or is "everything since the cursor" always right?
   A member you own has no reason to hold back commits, but a partial push would
   let you keep work in progress local.
2. ~~What happens to a stackspace commit that touches only *other* prefixes?~~
   Confirmed by the spike: josh drops it as empty. The member's history therefore
   has gaps relative to the stackspace, which is correct and invisible in practice,
   but means the two are not one-to-one — any future "which member commit came
   from which stackspace commit" tooling has to accept that.
3. Should `pull` after `push` be a no-op automatically, given the cursor already
   advanced? Probably, and it falls out of the cursor rule above — but it needs a
   test, since the alternative is a merge of a history with itself.

## Appendix: the MSBuild overlay

Verified against the reference stackspace (2026-08-25): none of its three
upstream repos owns a root `Directory.Build.targets`, so a stackspace-root one
reaches every child project via MSBuild's walk-up and swaps the cross-repo
`PackageReference`s for `ProjectReference`s with **zero upstream-file edits**.
Ordering matters (conditions read the package refs before `Remove` deletes
them); conditioning on `AnyHaveMetadataValue('Identity', …)` rewires only
actual consumers.

```xml
<Project>
  <ItemGroup Condition="'$(UseStackSources)' != 'false'">
    <ProjectReference Include="$(MSBuildThisFileDirectory)pty-core/src/Pty.Core/Pty.Core.csproj"
                      Condition="@(PackageReference->AnyHaveMetadataValue('Identity', 'Pty.Core'))" />
    <PackageReference Remove="Pty.Core" />
  </ItemGroup>
</Project>
```

Checked with `dotnet msbuild -getItem:ProjectReference,PackageReference`:
default evaluation swaps `Pty.Core`/`Term.Core` for project refs;
`-p:UseStackSources=false` restores the against-real-packages build that
upstream CI sees. The walk-up ignores git boundaries, so building a child from
inside its folder also gets the overlay — right for the dev loop; use the flag
for pristine verification. Node/Go equivalents (npm workspaces/overrides,
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

  <ItemGroup Condition="'$(UseStackSources)' != 'false'">
    <_StackAbsent  Include="@(StackSource)" Exclude="@(PackageReference)" />
    <_StackPresent Include="@(StackSource)" Exclude="@(_StackAbsent)" />
    <ProjectReference Include="@(_StackPresent->'$(MSBuildThisFileDirectory)%(Path)')" />
    <PackageReference Remove="@(StackSource)" />
  </ItemGroup>
</Project>
```

Item *transforms* are fine at project level — it is only batching that is not —
so `@(_StackPresent->'…%(Path)')` evaluates as intended. The escape hatch stays
on the outer `ItemGroup`, or the pristine against-real-packages build the
section above promises would no longer be reachable.

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

### Reference assemblies (2026-09-04, #258)

The swap has one more silent failure. A consumer that reads another member's
internals through a publicizer (`IgnoresAccessChecksToGenerator`) compiles fine
against the package and fails fused with a wall of `CS0122`, on members that did
not change. The publicizer rewrites the implementation assembly; a project
reference hands the consumer the *reference* assembly the SDK produces by
default, which has internals stripped and was never publicized. A package
without a separate `ref/<TFM>` asset — most of them — hands the consumer its
implementation assembly instead, which is why the same code built before.

`wire` therefore writes `ProduceReferenceAssembly=false` into the overlay, under
the same `UseStackSources` switch as the swaps. Every project under the overlay
gets it, not only the redirected ones: matching the current project against the
redirect paths in a condition means separators and casing that differ by
platform, and a match that fails does so silently — the failure mode the overlay
exists to prevent. Reference assemblies are an incremental-build optimisation
(a dependency's implementation-only change does not recompile its consumers),
so turning them off costs some rebuilding inside the stackspace; that is the
accepted trade for a graph that is correct across the member boundary. A generated
overlay that no longer matches what rig would write is reported by `doctor` as
out of date, so a stackspace wired before this change learns to re-run `wire`;
an overlay that was never written is reported the same way, since a check
that stays quiet about it would leave an unwired stackspace looking healthy —
as is one left over once no reference crosses between members any more, for
which `doctor` asks every ecosystem whether it has links or not.
