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
| `rig stack rm <child>` | the inverse of add, for a repo that turns out not to belong (fusable but not buildable fused is discovered after import): drop its manifest entry and cursors, delete its prefix from the tree (`--keep-tree` to leave it as an ordinary directory), rewrite the overlay from the remaining members — removing rig's own overlay file when nothing crosses any more — and commit. Refuses while the prefix holds unsent or uncommitted work or files git ignores, or when that cannot be determined; `--force` overrides those. Uncommitted changes elsewhere in the stackspace are refused regardless, and so is a dirty prefix with `--keep-tree`, since the removal commit would sweep those edits in |
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
   engine log). rig now resolves the credential and passes it as a URL-scoped
   `http.extraHeader` in the environment. Not a PAT rig stores, and not in argv:
   `git -c` would put the token in every process listing, and the git runner
   quotes its arguments into the error it returns on failure.

   *Revised 2026-09-07: `gh auth git-credential` is asked first and
   `git credential fill` second. 401 is not the only way a private upstream
   fails — when the client presents a credential that authenticates but cannot
   read the repo, josh returns an **empty history** rather than an error. A
   stale helper entry (an old token still in the keychain) produces exactly
   that, and the import path treated it as success: it recorded the cursor,
   made no import commit, and left `status` reporting a member up to date
   against a directory that did not exist.

   The forwarded header also turned out to cover less than it looked like it
   did. It gets josh as far as *authorising* the request; the upstream fetch
   behind it is a `git fetch` josh spawns, which authenticates on its own and
   inherits only josh's environment — so it had been quietly relying on an
   ambient credential helper the whole time. `ls-remote`, which runs before the
   engine exists, carried no credential at all. Both now get one, scoped to the
   forge, by the same GIT_CONFIG_* mechanism.

   Four changes, because no one of them closes it — gh first so the maintained
   token wins, the credential on `ls-remote`, the credential in the engine's own
   environment, and a hard refusal when a fetch arrives with no tree under the
   prefix. The refusal happens before the merge, so a rejected import leaves no
   commit to unwind.*
2. Merge strategy per child on `pull` (merge vs squash vs rebase-ish), given
   the known merge-noise wart. Default merge, per-child override in manifest?
3. `stack init` adopting an existing non-fused stackspace (the sibling-clone
   layout people already have): re-import and keep the overlay, or not worth
   the code?
   *Partly answered by reconstitution (2026-09-04, #260): `init` now rebuilds
   any member whose cursor is recorded but whose directory is absent, at that
   cursor — so a clone of the root files alone (`rig stack seed`) becomes a
   working stackspace. Adopting sibling clones that were never fused is still
   open.*
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

### Conflicts outside the prefix (2026-09-04, #261)

A pull of one member produced eighty conflicts, every one under a *different*
member's prefix, and the error named the prefix that was asked for. Two
findings. The message was simply wrong: `stackPullOne` hardcoded the prefix
rather than asking git which paths were unmerged; it now lists them. The
conflicts themselves are impossible for a well-formed pull — a `:prefix=<name>`
history carries nothing outside `<name>/` — and a `UD` status (ours modified,
theirs deleted) needs the merge base to hold the path, so the fetched history
must share a full stackspace commit as an ancestor. How one got there is not
established (the stackspace had been hand-repaired), but the right answer for
such paths does not depend on it: a filtered history has no authority outside
its prefix, so `pull` settles them as the stackspace's own version, reports
the count by directory and what it implies, and commits the merge when nothing
inside the prefix remains. Conflicts inside the prefix stay the user's.

### Reconstitution and seeds (2026-09-04, #260)

A stackspace was a local artifact: moving it meant copying the fused repo,
because nothing smaller reproduced it, and the pieces that would have — the
root files deliberately kept out of every prefix — had nowhere else to live.
Three decisions:

- **The stackspace's own files are whatever sits at the root outside every
  prefix.** No new directory convention: that set is already what the
  stackspace's own commits carry, and `rig stack seed <dir>` exports exactly it
  (`git archive` of HEAD's root entries minus the member prefixes) as a fresh
  one-commit repository. A few kilobytes, no upstream history, and nothing
  derived — the manifest already records what each prefix held. It refuses a
  member holding commits that have not left, as `rm` does: a rebuild holds the
  cursor or the proposed branch, and such commits are in neither.
- **`propose` leaves a local record that the work has left.** Nothing in the
  stackspace's own history says a prefix was proposed, so `status` and `rm`
  kept reporting proposed work as unsent. `propose` now keeps the commit it
  pushed under `refs/rigsmith/propose/<name>`; a prefix holding exactly that
  tree has nothing left to send. `rm` drops the ref with the member.
- **`init` reconstitutes.** A member with a cursor but no directory is imported
  *at the cursor* rather than at upstream's tip, so the rebuilt stackspace
  matches what was left. `stackPullOne` gained an `at` override for it.
- **Fork members can be imported from the fork.** `propose` leaves work on a
  `stack/…` branch of the fork while the cursor names the upstream commit it
  was based on, so a rebuild from upstream would drop it. `trackBranch` names a
  fork branch to import from explicitly; without it, a rebuild checks the branch
  the member was last proposed to (`lastPropose`) and uses it while it exists.
  The prefix is fetched through the proxy from the fork's path, and the cursor
  is set to `merge-base(branch, upstream tip)` over the unfiltered objects — an
  upstream commit, so `status`, `propose`'s guard and `pull` keep measuring
  against upstream. josh filters are a pure function of the commit graph, so the
  prefixed history of that base is the same object whichever remote it came
  through, and a later pull of upstream merges against a shared ancestor.

### Republished ids (2026-09-04, #259)

`wire` keys the overlay on each project's real `PackageId`, deliberately —
guessing from filenames redirects the wrong package. That leaves no way to
express a fork that republishes under its own id (`Acme.Foo` for `Foo`, to a
private feed, so the app builds without the stackspace): the app references an
id no project declares, and inside the stackspace it resolves from the feed,
builds, and never tests the fused code. The manifest carries it, since the
manifest is outside every prefix and nothing there leaves in a PR:
`publishesAs: {"Foo": "Acme.Foo"}` per member, or `publishPrefix: "Acme."` for
every id a member produces. `stackRedirects` resolves a reference to a
republished id back to the project producing the original and emits the
redirect under the id the consumer wrote, which is what MSBuild matches. A rule
naming a package the member does not produce is reported, not silently ignored.

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

### Releasing from a stackspace (2026-09-05, #268, #269)

A stackspace is also where its fused build is released from, and the release
engine assumed every package's version lives in its manifest. Under a member
prefix that is the one place it must not be written: the file leaves in a
`propose`, and the upstream gets a version bump it never asked for. Decisions:

- **shiprig reads the stack manifest** (`core/stackspace`, the same two
  locations `rig stack` reads) and treats every path under a member prefix as
  not this repository's to write. `version` still computes, cascades, consumes
  changesets and writes changelogs; it stamps only the stackspace's own
  packages. `tag`, `push` and the forge `release` are skipped in the plan with
  the reason — a tag on a fused history names nothing, and the history must
  never reach a remote. A step the config replaced with its own `run` is the
  user's and runs.
- **A version that is not written anywhere is recorded.**
  `.changeset/versions.json` (`core/versionstate`) holds the number a release
  computed for any package it did not stamp — a member, a `--no-stamp` run,
  `versioning.stamp: false` — and discovery reads it as the current version
  when the manifest is not the version's home. Without it a resumed pipeline
  (`--from pack`) had nothing to read back, and the next plan bumped from a
  stale number.
- **A packable project with no version in the tree is a package.** MinVer and
  CI-stamped projects declare no number; discovery used to skip them as not
  packages. `IsPackable`, a `PackageId` or a MinVer reference now counts, with
  an empty version the record fills in.
- **A member's changelog is a section of the root `CHANGELOG.md`**, since its
  own directory is not the stackspace's either. One file, `# <package>`
  sections, newest entry on top within each.

### Proposing one fix out of several (2026-09-07)

`propose` commits `HEAD:<prefix>` onto the upstream tip — the prefix's **whole**
current tree. It was never a per-change extraction, and nothing said so, which is
how a stackspace carrying two fixes for one project produces a second pull request
showing the first one's changes.

The two properties are in tension and both are wanted:

- The proposed branch is what a rebuild reconstitutes from (`lastPropose`), so it
  has to hold **everything** the prefix carries or CI publishes without the rest.
- A pull request has to hold **only its own change**, or a maintainer is reading a
  diff nobody asked them to review.

One branch cannot be both, so `--from <branch>` splits them: in-flight fixes live on
topic branches of the stackspace, rooted on the commit that imported that member,
and `main` merges them and stays the fused line you build and test.

**Why a branch and not a range of commits.** The first attempt selected commits
(`--commits HEAD~1..HEAD`) and replayed their prefix-scoped diffs onto the upstream
tip through a scratch index. It worked, and it was the wrong shape twice over. A
range is *positional* — `HEAD~1..HEAD` means something different after the next
commit, and nothing a week later. And reconstructing one change out of an
intertwined history is a patch that stops applying exactly when it is most needed:
the more fixes a stackspace carries, the more they touch the same files, so the
common case degrades into "does not apply" — in the very scenario the feature exists
for.

A topic rooted on the import keeps the isolation **by construction** instead of
reconstructing it. Its prefix tree already *is* upstream plus that one change, so:

- the proposed tree is `<topic>:<prefix>`, taken as-is. No replay, no scratch index,
  nothing that can fail to apply. `TreeWithPrefixCommits` went away with the
  approach that needed it.
- a topic branched off a line already carrying another unmerged fix **contains** that
  fix, and `propose` reports the commits it is sending rather than refusing. There is
  no check to be had here: the first attempt compared the tree at
  `merge-base(topic, HEAD)` to upstream's, which is wrong twice over — once the topic
  is merged into the integration line that merge-base *is* the topic tip, and more
  fundamentally, which commits constitute a change is precisely what the branch
  encodes. rig cannot distinguish "that came along by accident" from "that is part of
  my change"; only the author can, and a listed subject they did not expect is what
  tells them.
- a cross-cutting change is one topic proposed to each member it touched, which is
  what a stackspace is for. A range would have to be retyped per member and mean the
  same thing each time.

**Naming them.** `stack-pr-<name>` is a *recommended* convention rather than a rule.
`--from` takes any branch and an exact match always wins; the conventional name is
only the fallback when what it was given is not itself a branch, so
`--from reader-wedge` finds `stack-pr-reader-wedge` without it being typed. `status`
lists branches named that way, which is what makes "what is in flight" something you
read rather than remember. The prefix is deliberately **not** `branchPrefix`'s
`stack/`: those name branches on the *fork* and mean a pull request, while these are
work in progress here, and one spelling for both would invite reading either as the
other.

The rest is unchanged from the first attempt:

- `trackBranch` holds the whole divergence, and `propose --from` **pushes it on
  every send**. It stops being a branch the user maintains and becomes one rig
  writes.
- `--from` **refuses without `trackBranch`**, rather than leaving the rebuild short.
  That failure is invisible from the machine where the work was done: the worktree
  still has every fix, and only a rebuild elsewhere is missing them.
- The integration push is recorded under `refs/rigsmith/integration/<name>`,
  alongside `refs/rigsmith/propose/<name>`. `stackUnsentWork` accepts either, or
  `seed` would refuse a stackspace whose unproposed commits are safely on the fork,
  and `status` would call them unsent.

`status` now says that `propose` sends the prefix's whole divergence, because that
behaviour is otherwise discovered by a maintainer asking why a diff touches something
unrelated. Said rather than counted: a count needs a range, and every range against the
integration line is wrong here — the newest import marker sits on top of the fixes, so
`marker..HEAD` omits all of them, and a topic merged into the line makes
`marker..topic` empty. Whether a topic touches a member is answered by comparing prefix
TREES, which no merge can distort.

**Staleness after a pull.** A pull moves the prefix on and a topic branch does not
come with it, so the topic's tree is upstream as it used to be. The whole-prefix path
is protected by the stale-cursor guard, which `--from` cannot rely on: `HEAD` gets
pulled and a topic does not, so the cursor equals the tip and the guard sees nothing
wrong. `propose --from` therefore requires the cursor to be an ancestor of the topic,
and `pull` and `status` both name the topics that are not.

**Why they are not re-rooted automatically.** The obvious target is the commit the
pull just made, and it is wrong: `stackPullOne` merges into `HEAD`, and `HEAD` has
already merged the topics, so re-rooting a topic onto that commit folds the very fix
it isolates back into it — and the same is true of any base derived from the
integration line. Correct re-rooting means replaying the topic's own diff onto
upstream's new tree with none of the integration line's fixes: a synthesised clean
base, not a rebase onto an existing commit. That is a separate piece of design and
deliberately not a flag on `pull`, because a flag that is right only when a prefix has
exactly one unmerged topic — and silently wrong otherwise — is worse than the manual
step it replaces.


### Scheduled upstream canary

`rig stack canary --seed acme/rigstack-app@main --report canary.json`
resolves eligible upstream branches at tip, rebuilds and wires a temporary
stack, then runs `--build` and `--test` from its root. Commands use bash.
`--members app,core` restricts following. Tags, `upstreamCommit` pins and
fork-tracking (`trackBranch`) members are excluded from movement; fork-tracking
members retain their configured fork source. A full cursor and a fast-forward
upstream range are required. Nothing new produces an empty report and no writes.

Copy `examples/github-workflows/stack-canary.yml`, select a released rig version
containing this verb and set the seed and build commands. The cron expression
controls frequency. `--mode batch` is the default: one build and one PR for all
updates. Red batches list candidate members and ranges without claiming a
single culprit. `--isolate` adds a full build per moved member after a red batch;
`--mode separate` always tests and proposes each member independently. This
costs one rebuild per member and does not prove combined compatibility.
Bisect is deliberately skipped: no implicit unbounded build loop is run.

The build job has only `contents: read` and checkout `persist-credentials: false`.
It receives no write secret. A fresh hosted publish job consumes only the JSON
report and a trusted pinned rig installation, with `contents: write`,
`pull-requests: write`, and `issues: write`. It runs
`rig stack canary --seed acme/rigstack-app@main --publish report/canary.json`;
it runs no project commands and restores no build cache. Do not reuse a
self-hosted workspace between these jobs. Environment scrubbing of `*_TOKEN`
and `*_PAT` in build children is defense in depth, not a sandbox: arbitrary
upstream code can leave files behind, which is why the separate runner matters.
Private seeds require Git credentials in **both** jobs. Checkout uses
`persist-credentials: false`, and `GITHUB_TOKEN` alone does not configure Git:
configure a read-only Git credential helper in the build job, and independently
configure Git access to reload the seed on the fresh publish runner. Missing
build-job seed credentials fail setup before a report; missing publish-job seed
credentials fail seed reload before any publication. Never give the build job
a home write token. See [private seed access](https://rigsmith.dev/rig/stack#one-manifest-home).

For workflow-token PR creation, enable Settings → Actions → General →
**Allow GitHub Actions to create and approve pull requests**, or supply a token
with home PR write access. `pull-requests: write` alone does not override that
repository policy. If PR creation is denied after branch creation, the summary
names this setting and warns that a `rig-canary/` branch may remain. The next
run updates that same stable branch and retries PR creation.

The publish API token must also read every upstream for ancestry comparisons;
Git fetch credentials do not grant API access. Public upstreams work with the
example token; private upstreams in another repository require a suitable
publish-only token with upstream read and home contents/PR/issue write access.
Missing API access skips that result with a summary and success status; other
results still proceed. Invalid ancestry is a hard error, checked across all
available results before publication writes. Missing token skips publication.
Each result's publication success or failure appears in the summary; a failed
PR or issue does not stop later results.

Setup and configured command outcomes are separate. Filesystem errors,
materialization, manifest parsing, tool lookup, `git init`, `rig stack init`
and `rig stack wire` failures are **inconclusive**: the command fails before
writing a report, so the workflow skips publication and preserves existing
PRs/issues. Cancellation or failure to start a command is also inconclusive.
Only a configured `--build` or `--test` process exiting non-zero produces red.
This deliberately includes deterministic import/filter/wiring failures at a
new tip in the inconclusive category: init/wire expose no typed distinction
between transport/engine trouble and source incompatibility. Rig does not
classify arbitrary diagnostics by text or blame a member on that evidence.
Inspect the failed phase and reproduce locally; a future typed setup diagnostic
could support narrower attribution. Isolation setup errors likewise abort the
whole run without a partial report.

The report is attacker-controlled: upstream code can replace `Green`, member
selection and attribution. The publisher cannot verify that a build ran, passed,
or tested these exact commits. PR bodies say "reported green by the build job";
this is a claim from that job, never proof or automatic merge authorization.
The publisher independently verifies the seed base, allowed cursor deltas, and
that every target advances the cursor and is an ancestor of the current tracked
upstream branch. The branch comes from the fresh seed manifest, never the report.
A fork-network SHA that merely compares ahead of the cursor is insufficient.
Human review and trusted PR checks remain necessary before merging.

The workflow explicitly requires `needs.build.result == 'success'` for publication.
A red canary records its failure in a report and exits successfully so it can
open an issue. Setup failures, failed/missing artifact uploads and cancelled
build jobs skip publication, leaving existing PRs/issues unchanged. Success of
the job only establishes workflow completion, not the truth of `Green`.

The example installs a reviewed release with `go install module@version` on each
fresh runner, with public module verification through `sum.golang.org` enabled.
Go checks module source hashes against that checksum database; it builds the
pinned trusted tool source rather than authenticating a prebuilt release archive.
This is a source-build alternative to the checksum-verified binary installer;
it does not use the upstream checkout or build-job executables. See the
[Go module authentication reference](https://go.dev/ref/mod#authenticating).

Publication requires the same seed commit, validates the cursor-only deltas,
and reloads the manifest from an independent read-only seed clone. It uses
`stackCheckHome` over reachable history and creates a one-parent commit via
GitHub's Git data API, with only `rig.stack.jsonc` replaced in the seed tree.
No fused objects or build-job files enter that commit. Root overlays and README
remain the seed's. The stable `rig-canary/<group-hash>` branch (one fixed batch group, or one group per member) is reset to
one commit on the seed base each update; serialize workflow runs to avoid races.
PRs are updated by branch; failure issues by a stable body marker. Recovery
closes the matching issue when the green PR is published. A merged PR advances
the seed, so an unchanged next run does nothing. Use a dedicated branch namespace.

Reports list member labels, full ranges and commit hashes rather than untrusted
commit subjects or subprocess output. This avoids Markdown mentions, leaked
command diagnostics and workflow command injection. Build output is suppressed;
reproduce failures locally using the listed ranges. No tokens enter the report.
Without `GITHUB_TOKEN`, publication reports a summary and succeeds; unavailable
API permissions likewise produce a summary without replacing the build result.
Setup errors and stale seed reports fail. Build/test failures are recorded in
the report so the later job can publish the issue. `-n` previews without builds,
report files, API writes, commits or branch updates (reading the seed is allowed).

### Manifest home (default)

Keep one small seed repository as the manifest home: the committed
rig.stack.jsonc, README and root overlay files, outside every member prefix.
Publish with `rig stack seed --publish acme/rigstack-app@main`. The existing
`rig stack seed <dir>` offline export remains available. In a fresh empty
directory, use `rig stack setup acme/rigstack-app@main`; local development and
CI pass exactly the same seed string. Setup without an argument still works
in an existing clone. URLs, scp-style remotes and local paths are also accepted.
The owner/repo shorthand means GitHub; its optional @ref names a branch or tag
(including branch names with slashes). Tags are read-only homes: automatic
publication prints one skip line and succeeds before reconstruction checks;
explicit publication to the pinned home errors. Doctor uses the pinned commit.

Only an explicit seed argument opts into a manifest home. No-argument setup
leaves the home configuration and origin unchanged, including on older fused
stacks. After a manual clone, opt in with `rig stack seed --publish <seed>`
only when the committed root files match the existing seed. If setup or local
edits changed them, initial adoption refuses: use `rig stack setup <seed>` in
a fresh empty directory, reconcile the desired root edits there, commit, then
publish from that configured checkout. --force does not bypass this refusal.

Explicit setup remembers the remote, branch and unfused tip in local git config
(`rig.stackHomeURL`, `rig.stackHomeBranch`, `rig.stackHomeBase`,
`rig.stackHomePinned`). Pinned state is a separate boolean, never a reserved
branch name. Setup stores the raw origin URL, before insteadOf expansion, so
CI credential rewrites cannot be copied into this metadata. All adoption paths
disable origin's push URL when its repository identity matches the home; an
unrelated origin is left unchanged. An unconfigured checkout
keeps its existing behavior. Pull and propose publish the committed root files
after recording their changes locally. A proposal made with an already-edited
manifest defers publication until that file is committed. Other dirty files
also defer automatic publication successfully; explicit publication refuses a
dirty checkout so prefix exclusions match the committed manifest. A retry is `rig stack seed` without
arguments. Publication never imports objects from the fused checkout: a temporary
independent clone receives only a git archive of HEAD outside member prefixes,
then creates one ordinary commit parented on the seed tip. No-op exports create
no commit. An existing home cannot be replaced on first publication: if its
files differ, rig requires setup from that seed and reconciliation first. Every reachable seed commit is checked for member trees before and
after writing. Doctor clones the configured home URL and checks the configured branch (or pinned seed revision); ordinary
unconfigured stacks incur no seed network request. A remote that cannot be
checked produces a neutral access warning; a detected member tree is a failure
finding. The scan recognises only members currently in the manifest: an old
tree belonging to a removed member is not detected. Member destinations matching the
manifest home are refused. Git URL rewrite rules (including pushInsteadOf)
are applied before comparing member push destinations to the home.

The remembered base is scoped to both home URL and branch. Explicit publication
to another branch of the same URL does not inherit that base or retarget the
configured home state.

If the seed tip differs from the remembered tip, publication refuses; there is
no implicit merge or force push. Reconcile the competing manifest in a separate
seed clone, then set up from that revision before publishing again. Permission
failures and races fail clearly, retaining the local pull/proposal and its
committed manifest. A home publication failure returns non-zero even though
the pull succeeded locally: the error explicitly says the pull succeeded and
the home was not updated. Scripts must treat this as a publication failure,
not retry or roll back the member merge. Retrying does not undo local work. No confirmation is needed:
publication is an ordinary fast-forward push. There is deliberately no overwrite
mode; destructive replacement would require interactive confirmation and refusal
in noninteractive runs.

A member can commit a UTF-8 `.rig-stack` file containing one seed reference,
for example `acme/rigstack-app@main`, followed by a newline. Read it with
`rig stack home [pointer-file]`; this prints the reference for tools to consume.
Pointers are maintained by the member owner; setup never edits member sources.
A member README pointer is optional prose and is not generated or parsed.


Explicit seed export/publication validates that every present member is
reconstructible: unsent work or an unknown import baseline refuses publication,
and a proposed tree is checked against its recorded fork branch. --force
overrides this reconstruction safeguard for directory export, seed --publish,
or an explicit configured-home retry. It never overrides dirty-tree refusal,
fused-history checks, a moved seed, or differing root files on first adoption.

Automatic publication after pull/propose treats local member work as normal:
it prints a warning on stdout for each member it cannot reconstruct, then
publishes the recorded cursors and proposals. Such a seed reproduces those
recorded revisions, not unpublished local edits. A dirty tree instead defers
automatic publication with a stdout message and success status; commit or stash
the unrelated changes, then retry with rig stack seed. Pull still requires a
clean tree before its local operation; only the publication phase defers.

Home publication commits are unsigned internal snapshots: rig overrides
commit.gpgsign=false in the temporary clone, even when the user's global
configuration requests signing. This avoids signing prompts and unavailable
keys under the temporary rig identity; member commits retain their own policy.
The independent clone also uses an empty local hook directory for generated
commit and push operations, so global client hooks cannot rewrite or block a
snapshot. The remote's receive hooks and branch protections still apply.

Seed operations use plain git credentials for HTTPS and SSH. The owner/repo
shorthand resolves to a GitHub HTTPS remote, so private seeds in CI need a token
configured by the workflow (for example through a git credential helper).
This differs deliberately from private upstream imports: those pass the gh-first
CredentialFor/stackAuthForURL result through the josh HTTP transport, which cannot
use the caller's git credential helper directly. Seed clone and push have no
josh transport and retain git's own credential, SSH and workflow token policy.
A local gh login alone is insufficient unless git is configured to use it
(for example with gh auth setup-git).


Existing relative paths take precedence over GitHub shorthand and are resolved
to absolute paths before storing the home. Use an explicit ./ prefix for a
local path that does not yet exist. Local setup can clone a non-bare seed, but
local manifest homes must be bare repositories for publication. Rig never
changes another working tree or weakens Git's receive protection.
A seed directory argument and --publish are mutually exclusive.

Publication under -n/--dry-run prints the destination and root files it would
publish, then stops before any seed clone, commit, push or home configuration.
This applies to explicit seed publication and the publication steps after
pull/propose; pull's own preview behavior is a separate concern.

HTTPS seed URLs with userinfo are rejected before clone or home configuration:
keep tokens in Git credential helpers rather than rig.stackHomeURL or origin.
Error rendering redacts userinfo even for legacy configured URLs and captured
Git diagnostics.

Pull also publishes committed root updates when every member is already current,
so it can retry publication without creating another local merge.
A .rig-stack pointer contains exactly one reference, optionally followed by one
LF or CRLF newline; embedded whitespace and control characters are rejected.

### Source signals from the .NET overlay

With `UseStackSources` other than `false`, the overlay sets
`RigStackspace=true` and `RigStackspaceRoot` (an absolute directory with a trailing
separator). Before `PrepareForBuild` / `CoreCompile`, target
`RigStackSourceSignals` appends `RIG_STACK` to `DefineConstants`. For each sibling whose source is reached directly by a swap or transitively
through a swapped sibling, it also sets `RigStackSource_Acme_Core=true`
and appends `STACK_ACME_CORE`. A project referencing no sibling gets only the
stack-wide signals.

`wire` computes a sorted, deduplicated closure of scanned PackageReference
edges per producing project and emits it with each redirect. Cycles terminate
and diamonds share one signal. A closure is activated only when MSBuild actually
swaps its root PackageReference in the consuming project. Unreachable members
remain absent. `doctor` previews the same closure; its full generated-file
comparison detects stale links, so re-run `wire` after reference changes.

ProjectReferences propagate signals too: `Tests -> App -> Acme.Control ->
Acme.Core` gives Tests both sibling signals. `RigStackGetSourceSignals` queries
referenced projects' effective sets recursively at target time. SDK projects use
the SDK's configuration, platform and nearest-target-framework reference metadata;
an outer multi-target query combines its inner builds. Direct ProjectReferences
to an offered source producer also count. No sibling signal is added merely
because an unrelated ProjectReference exists, or for a producer referring only
to itself. Both properties and constants are published before compile, including
on incremental builds.

The query invokes only signal and SDK reference-preparation targets, never Build
or Restore. SDK preparation reads existing restore assets, as a normal build
does after restore. An ancestry list of full project paths, compared without
case sensitivity, terminates cycles. Diamonds can query a project under several
ancestries, but each package's constant is emitted once. MSBuild caches target
execution within each project/global-property instance. The cost is additional
project evaluations and reference queries proportional to reachable paths; large,
heavily shared graphs can cost more than a simple chain. There is no extra
compilation or restore. Queries require the referenced project to import the
overlay; projects hiding it cannot return their effective sets.

SDK reference preparation uses private SDK targets and items. If an SDK project
has evaluated ProjectReferences to existing files but preparation returns no
candidates, the query fails with `RIGSTACK001`, naming a likely SDK item-contract
change. Conditional references that evaluate away and missing files do not
trigger this check. This is a hard error, independent of `TreatWarningsAsErrors`:
a warning could be ignored and silently compile guarded code or tests out. A
build with untrustworthy source signals must stop until rig or the SDK is updated.

`BuildProjectReferences=false` suppresses referenced builds, not the read-only
signal query: a caller being compiled still needs its effective constants even
when sibling binaries already exist. `dotnet test --no-build` skips compilation
and uses the constants baked into the existing test assembly; it does not need
to recompute source signals. Build that assembly with the intended stack-source
settings first. A no-build run cannot recover tests omitted by an earlier build.

The static PackageReference closure remains as a conservative fallback for
package edges inside swapped siblings. Direct conditional PackageReferences and
ProjectReferences are evaluated by MSBuild, including the selected SDK framework.
Deeper package conditions in the static closure can still report a source signal
for an inactive edge. This prefers a visible compile error from an overly broad
true signal to silently losing guarded code from a false signal. References
introduced after the overlay's evaluation or only by targets are not guaranteed.

`doctor` follows scanned ProjectReference paths relative to each consuming
manifest, including across members, instead of guessing from project filenames.
Its output is explicitly a static preview: conditions, framework selection,
property-expanded paths and imported references are resolved by MSBuild during
the real build, so the preview can overstate conditional edges or omit dynamic
ones. The preview agrees with unconditional, literal-path graphs.

`UseStackSources=false` disables swaps, source properties and constants together.
Hand-owned gated parent imports are preserved without a legacy advisory.
Historical ungated rig imports remain detectable; customized blocks that wire
cannot safely replace get a manual inspection remedy.

Names come from the **referenced package ID**, including a republished ID:
replace each character outside ASCII `A-Z`, `a-z`, `0-9` with `_`; preserve case
for the property suffix and uppercase for the constant suffix. For example,
`Acme.Core` becomes `RigStackSource_Acme_Core` and `STACK_ACME_CORE`. Prefixes
make leading digits safe. IDs that sanitise to the same name share a signal;
choose distinct IDs if you need to distinguish them.

The generated root `Directory.Build.props` exposes `RigStackspace`,
`RigStackspaceRoot`, and `RigStackAvailable_Acme_Core=true` before the project
body. Availability means the overlay offers that package's source stack-wide;
it does not mean this project references it. The same ID sanitising rule applies.
Project-body property and item conditions, and member props after the marked
import, can use these early signals. `wire` patches writable member props using
the same marked, gated import as targets; `doctor` checks both generated files
for staleness. When no cross-member references remain, generated root files and
marked imports in writable members are removed together. Hand-owned files are
preserved and must be maintained by you.

Early props signals are optional. A hand-owned root `Directory.Build.props`
is left unchanged: `wire` still generates the targets redirects and reports a
note explaining how to add early signals yourself. Likewise, a member's props
that hides the root import produces a note, including for non-writable members.
Missing or stale generated props are notes too: existing wired stacks see one
`Directory.Build.props: not written` note until `wire` generates it. These notes
do not block `doctor` or `pack`. Missing or shadowed targets remain blocking
problems because those builds silently resolve registry packages instead.

To add early signals to a hand-owned root props file, use the same condition and
naming rules (keep the literal root declaration so member imports recognise it):

```xml
<PropertyGroup Condition="'$(UseStackSources)' != 'false'">
  <RigStackspace>true</RigStackspace>
  <RigStackspaceRoot>$(MSBuildThisFileDirectory)</RigStackspaceRoot>
  <RigStackAvailable_Acme_Core>true</RigStackAvailable_Acme_Core>
</PropertyGroup>
```

Maintain the availability list when members or references change. Availability
can be true for a project that does not reference that sibling: conditioning a
PackageReference on it is fine, but it must not guard source-only API usage.
Use `STACK_ACME_CORE` for that compile-time decision.

`RigStackSource_*` remains available only in targets after
`RigStackSourceSignals`; `STACK_*` still means this assembly compiles against
that source. Direct conditional PackageReferences are resolved by MSBuild for the
selected configuration/TFM; transitive signals use the static closure described
above. Availability can be true while no swap or constant is produced. Use availability to condition a PackageReference, but use the
compile constant to guard source-only APIs. No static per-project facts are
promised. Re-run `wire` after changing members or references.

A command-line/global `UseStackSources=false` disables early signals too.
A value first assigned in the project body is too late to undo props evaluation:
earlier properties/items may already have been selected, even though the later
swap and constants are disabled. Set the opt-out globally or before the marked
props import. Early availability and later source signals have distinct names;
the later overlay reasserts the root signals when enabled.

`-p:UseStackSources=false` disables the swap, properties and constants together.
Rig-owned member imports check the nearest parent `Directory.Build.props` or
`Directory.Build.targets`, respectively, for rig's generated-overlay or member-import marker, or the literal declaration
`<RigStackspace>true</RigStackspace>`, before importing it. The declaration
keeps an overlay discoverable after you drop the ownership marker to maintain
it by hand; retain that declaration in a hand-owned overlay. A custom overlay
written from scratch should include it too. This
supports `rig.stack.jsonc`, `rig.stack.json`, and inline `.rig.json` stacks
without treating an unrelated `.rig.json` as a stack. Re-running `wire` upgrades the old
rig-owned ungated block in writable members, preserving their other content.
A plain clone's imports do nothing even beneath unrelated build props or targets.
Legacy ungated imports still resolve sources correctly and are reported as
informational notes, not blocking problems; non-writable members are left alone.
`doctor` previews the properties/constants by project from static discovery;
conditional references still depend on the configuration MSBuild builds.

## Member CI reconstruction

`rig stack ci` builds in an automatically removed temporary repository, outside
the checkout. Manifest mode reads the committed manifest from checkout HEAD,
normalizes sibling imports to exact cursors (ignoring proposal branches), and
uses the existing init import path. Tip mode drops cursor and tag/commit pins.
Exactly one of `--manifest` or `--seed` is required. Seed mode shares
`stackSeedReference` parsing with setup but clones without checkout into a
separate temporary directory and reads only HEAD:rig.stack.jsonc. It removes
origin before use and never adopts or publishes a home. This avoids setup's
engine, overlay, commit and home side effects; the fused root remains remote-free
for `stackCIGuardPush`. Seed files and hooks are never materialized.
Git uses configured credentials; diagnostics suppress raw clone output and
seed references use SafeRemote in errors, summary and provenance. Shared seed
validation rejects `?` and `#` in user-written reference forms (including local
paths) before path resolution, without echoing them, including
for setup, deps and home publication; configured homes are revalidated before
retry hints. Resolved absolute local remotes retain characters inherited from
the current directory, including when loaded from home configuration. Setup
removes its new clone if validation or home adoption fails, restoring the empty
destination; once adopted, later engine/member failures retain it for retry.
Rejected references never reach CI output or provenance. Seed hosts
obey the member allowlist. Private-seed failures explain fork PR access and
exit setup (10). Provenance stores reference and resolved commit under _seed, which is reserved
and rejected as a member name during manifest validation.
Member pointers remain discoverable via `stack home`, but CI requires explicit
selection so a checkout cannot silently change the workflow's manifest source.

`stack deps --seed` uses the same read-only loader and host allowlist, leaving
dependency evaluation and publication policy unchanged. The action dispatches
the same source to CI and dependency reporting. Seed-mode cache keys contain a
hash of the seed reference plus run ID, attempt, job ID and matrix index; broad restore prefixes reuse
old entries while unique keys allow updated bundles/packages to be saved.
This avoids an extra seed fetch before cache restore; it deliberately trades
exact cache hits for continuing cache refresh. Manifest mode keeps content keys.
Empty hashFiles patterns are ignored (an empty-only pattern returns an empty
string), so they cannot represent seed cursors. Fork guards remain on both caches.

The member tree is enumerated with `git ls-tree HEAD` and materialized from raw
`git cat-file --batch` bytes under the manifest prefix. This bypasses archive
export-ignore/export-subst and checkout smudge/EOL filters, including the
checkout's local config; replacement objects are disabled. It needs no PR
ancestry or complete history. File modes and contained symlinks are preserved.
A throwaway local commit records the replacement for wire's HEAD discovery.
Wire runs again after replacement, keeping sibling relative references within
the fused root. The checkout and its index remain untouched. Submodules are
not materialized; `.gitmodules` and gitlink entries fail setup explicitly.

The verb owns setup, wire, build and test reporting and distinct exit statuses;
the composite only installs pinned rig and dispatches it. Engine installation
uses rig's manifest/default josh pin on first import. Summary writing is optional
outside GitHub. Commands run from the selected member and test results stream
to stdout. See [the member CI guide](guides/testing-a-member-pr-in-its-stackspace.md).

The published action is `rigsmith/stack-action@fcae919e5eb7036b81b94ad2b5977d5a77648128 # v1.0.0`.
Its source of truth remains `.github/actions/stack/` in rigsmith. The initial
release copies action.yml and install-rig.sh byte-for-byte from v1.24.0
(`49c1c594bc3a40ee5cbee7d42707b3fa26d3f48b`). Full SHA pinning is recommended;
`@v1` is a moving major tag.

### Keeping the published action in sync

Recommend a manual release-checklist step for now; no automatic synchronization
is installed. After each rig release, compare `.github/actions/stack/` at the
released tag with the previous exported source. If it changed, export from that
immutable tag, copy action.yml and install-rig.sh unchanged to the standalone
repository root, adapt its README and copy LICENSE, and record the full rigsmith
source commit. Review the diff and run the Linux/macOS/Windows same-repo and fork
PR checks, including installer checksum pinning. Publish a new annotated action
version tag and advance its compatible major tag only after verification.
Verify from a fresh clone that executable files match the source bytes, then
update rigsmith examples to the new full action SHA and version comment in a PR.
Do not retag an existing version. Choose the action version for action API
compatibility independently of the rig version selected by `rig-version`.

This keeps the first releases reviewable without granting a release workflow
cross-repository write credentials. If release frequency warrants automation,
a release-time workflow should open a reviewed export PR rather than silently
advancing action tags.

Fresh-runner engine acquisition follows `stack init` → `stackEngine` →
`ensureJoshProxy` → the pinned installer. No separate josh action step is needed.
The local E2E uses preinstalled josh to stay offline; fresh hosted downloads
were verified by code-path inspection only.

Committed symlinks must stay within the member prefix. Rooted targets and
links escaping that prefix are rejected, including Windows rooted paths. If
the runner cannot create a contained symlink, setup fails (exit 10) naming the
file and target; it does not replace the link with a regular file. Windows
runners may need Developer Mode or symlink-creation privileges for members
that commit symlinks. Members without symlinks need no such privilege.

### Testing a complete topic

Use `rig stack ci --topic reader-fix --seed acme/stack-policy@<full-commit-sha> --member app`
(or action inputs `topic` and `seed`) to test all recorded proposals in that
topic together. Publish the proposal set to a reviewed seed and pin the same
seed commit in each member's job. `--manifest` remains available for a committed
checkout manifest; its trust depends on who controls that checkout.
Every recorded PR must resolve and be open; missing, closed, ambiguous or known
conflicting PRs fail setup rather than silently reducing the set. One PR is valid.
The selected member must participate. `--upstream-tip` cannot be combined with
`--topic`; `-n` refuses as for ordinary CI.

Each participating member uses its exact PR head tree, fetched read-only from
its declared fork or upstream. Other head repositories are refused even on an
allowed host. Nonparticipating members retain their manifest cursors. A cursor
remains the upstream import baseline, not the PR head or a claim of ancestry.
A PR based behind that cursor replaces the tree wholesale, including older
files; no merge or rebase onto the cursor is implied. Known API merge conflicts
fail setup; unknown mergeability does not establish merge readiness. Raw-tree
materialization preserves the existing LFS, submodule and escaping-symlink
refusals. Summary and provenance identify each applied head and repository.
Shared history and package caches are disabled by the action in topic mode.

Name the calling workflow job `stack topic`. Its check belongs only to the
triggering PR; each participating PR needs its own run covering the same set.
There are no cross-repository writes, automatic fan-out, or reruns when a sibling
head moves. Compare recorded SHAs and rerun each job after changes. Dependency
status publication still writes only to `GITHUB_REPOSITORY`. Merge-train
scheduling is deferred.

Topic builds execute code from **all** participating PR heads, including other
people's commits in declared forks. Declaring a repository does not make its
contributors trusted. Use ordinary `pull_request`, `contents: read`,
`pull-requests: read`, no secrets,
checkout `persist-credentials: false`, and an isolated disposable runner. The
GitHub token is captured for in-process metadata lookups only. Before any
child processes run, topic CI removes token environment variables (including
`GITHUB_TOKEN`, `GH_TOKEN`, enterprise variants, Actions tokens and PAT variables).
Build, test, init, wire and guard children inherit that scrubbed environment.
Topic mode also refuses HTTP extraheaders visible in the checkout's Git config,
without logging their values; set checkout `persist-credentials: false`.
This is credential hygiene, not a sandbox: use no secrets or privileged PATs,
and no runner with reusable credentials in files or credential helpers.
Fork-origin jobs use the same checks and may fail for inaccessible private repos.
Topic CI refuses `pull_request_target`.

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

The source of the manifest determines the authority of the repository allowlist
and proposal set. With `--manifest`, the file comes from the selected checkout's
committed HEAD. If that checkout is the triggering PR, the contributor controls
both declarations and which other heads are tested. Matching a declared fork
therefore means consistency with that manifest, not independent authorization.
A same-repository PR does not make this policy trustworthy or justify stronger
permissions. API metadata identifies a PR head; it does not authorize its code.
Trust reviewed workflow/action code for execution policy and a separately
reviewed, immutable manifest checkout for repository declarations and topic
membership. Use the [topic workflow example](../examples/github-workflows/stack-topic.yml)
to load `--seed acme/stack-policy@<full-commit-sha>` as the recommended
independent policy source, with an explicitly selected member. The example
also checks out reviewed action code separately. All tested member code comes
from resolved PR heads. Publish updated proposal records to the seed before
running CI; do not derive the seed URL/ref from PR data. Full SHA pins are
fetched explicitly, rather than interpreted as branch names.

Token scrubbing happens before seed parsing or cloning, as well as before
init, wire, build and test. The captured job token is only for API metadata;
it is never forwarded to the seed clone. Use a public seed or a separately
prefetched local seed for private policy. Fetch private seeds in an isolated
trusted preparation step and remove credentials before topic CI; do not leave
persistent extraheaders, helpers or reusable credentials on the build runner.
An unavailable private seed fails setup, without falling back to the checkout's
manifest. Ordinary non-topic seed mode retains its configured Git credential
behavior.

### CI cache and trust boundaries

`init` previously reused only josh's host-level object/filter cache, still
requesting each filtered cursor through a proxy. With `RIG_STACK_CACHE`, the
import path first consumes a bundle containing only that filtered upstream
history. Its key hashes source URL, exact cursor, prefix filter and engine
version. A hit fetches locally into the fresh fused repository and skips proxy
startup, upstream fetch and filtering. It never saves fused commits. Outer
Actions cache restore prefixes retain unchanged member bundles across manifest
changes and seed josh's host caches for changed members. In manifest mode,
package download keys include the entire manifest's immutable sibling cursors (which identify all
lockfiles/pins transitively) plus checkout lockfiles. No build outputs are cached.
Only consumed member hits enter the reported hit rate.

CI rejects custom josh pins and verifies restored binaries against the existing
source-pinned platform hashes for `r26.07.19` / release `r26.07.19-win.3`.
The rig installer is release-only: missing assets, network errors and checksum
mismatches all fail, with no Go fallback. `checksums.txt` from the release covers
corruption/mirror tampering, not a compromised release; workflow `rig-sha256`
pins an independently trusted expected archive digest.

The verb defaults to exact host allowlist `github.com`, matching its GitHub
Actions purpose while refusing arbitrary manifest-controlled destinations.
Enterprise hosts require explicit opt-in. Fused history has no remotes and a
rejecting pre-push hook; configured destinations are refused. This is accidental
push prevention, not a sandbox for build scripts. Read-only tokens, no persisted
checkout credentials and no secrets remain the fork-PR boundary. Never execute
PR code in a privileged `pull_request_target` workflow. A separate privileged
metadata handler must neither check out PR code nor execute its artifacts.

Raw-blob materialization now uses one `cat-file --batch` subprocess rather than
one per file. LFS is deliberately refused, checking `.gitattributes` across all
members for `filter=lfs`, so pointer-only builds cannot pass silently. Commits
and prefix filters are written to summary and a JSON provenance artifact. Pin
external actions by full reviewed SHA, including `rigsmith/stack-action@fcae919e5eb7036b81b94ad2b5977d5a77648128 # v1.0.0`.

Local measurement (`BenchmarkStackCIMaterialize`, 100 committed seven-byte files,
three iterations, macOS arm64): batch 74.98 ms/op versus per-file 2351.51 ms/op,
about 31× faster. The retained benchmark includes materialization and compares
101 Git subprocesses with two; this is fixture evidence, not a hosted-runner SLA.

### History cache trust

A restored filtered bundle is trusted history. Git checks object integrity,
but neither the bundle filename nor its Git object hashes prove that its
filtered commit corresponds to the upstream cursor in the manifest. No local
cursor-to-filtered-history verification is performed. The substituted-bundle
unit fixture deliberately demonstrates that different valid history under the
same cache key is accepted. An import record carried inside that same bundle
would not provide an independent trust anchor.

The trust boundary is GitHub's cache scoping: PR cache writes are scoped to that
PR ref and cannot poison another ref's cache; poisoning a default-branch cache
requires the ability to write caches in that branch's execution context. A
compromised default-branch workflow or trusted cache writer can therefore
substitute sibling sources. Cache keys and cursor provenance are not signatures.
See [GitHub cache access restrictions](https://docs.github.com/en/actions/using-workflows/caching-dependencies-to-speed-up-workflows#restrictions-for-accessing-a-cache).

`cache-history: 'false'` disables restored member/host history and engine caches
in the action and turns bundle consumption/writes off in the verb. Use it for
release-grade runs that should not trust cached filtered history; the equivalent
CLI flag is `--cache-history=false`. Existing host object caches on self-hosted
runners remain josh's responsibility; use a clean runner for that trust boundary.
`cache-packages` independently controls package-download cache restore/save and
defaults to `'true'`, like `cache-history`. Set both inputs to `'false'` for a
job that must not use this action's shared caches. Package caches contain only
downloads, not build outputs. Fork PRs override both defaults as described below.

The summary and JSON provenance record each member's `source` (`cache` or
`fetched`) and the actual `filteredCommit` imported. The manifest `commit` is a
requested upstream cursor, not an independently verified claim about cached
content. The selected PR member always says `fetched` because its tree comes
from checkout HEAD after import. Its `filteredCommit` describes the preceding
import, not the replacement checkout commit. For cached siblings, retain the
actual bundle/filtered history as well as the provenance to reproduce a build;
the cursor alone is insufficient if the cache was substituted.

Provenance artifact names include the job, matrix `strategy.job-index`, and
`github.run_attempt`, avoiding collisions between matrix legs and reruns.
GitHub documents strategy context access from job steps in its
[contexts reference](https://docs.github.com/en/actions/reference/workflows-and-actions/contexts#strategy-context).
The runner's [composite handler](https://github.com/actions/runner/blob/main/src/Runner.Worker/Handlers/CompositeActionHandler.cs)
creates embedded children; [child-context creation](https://github.com/actions/runner/blob/main/src/Runner.Worker/ExecutionContext.cs)
copies parent expression values. This source inspection supports strategy context
availability inside the composite. Actual matrix uploads remain unverified on
GitHub-hosted runners; local tests do not establish that integration.

### Fork PR cache policy

When a pull request's `github.event.pull_request.head.repo.full_name` differs
from `github.repository`, this action skips both combined `actions/cache` steps:
it neither restores nor saves history/engine caches or package caches. It also
leaves `RIG_STACK_CACHE` empty, unsets it before invoking rig, and passes
`--cache-history=false`, so the verb cannot write new filtered bundles. The
summary says both caches were skipped because this is a fork PR, rather than
presenting a hit rate. Push, schedule and other events without a pull-request
payload retain input-controlled caching. `cache-history` and `cache-packages`
each accept exactly `'true'` or `'false'` and default to `'true'` for eligible jobs.

Same-repository PRs still read base-branch caches, by GitHub's design. Cache
write scoping protects refs from some poisoning scenarios; it does not provide
confidentiality. These guards control this action's cache operations, not the
GitHub cache service's access policy: other steps or untrusted code that explicitly
request accessible caches are outside this guard. Do not treat a cache as secret
storage. Keep private workflows/material in a separate trust boundary when
untrusted code could request those caches. Use clean runners: disabling these
steps does not erase pre-existing files on self-hosted runners.

The action conditions and shell behavior are covered by local YAML-parsing and
fork/non-fork script tests. Actual GitHub fork-PR restore/save behavior remains
unverified locally; validate it on hosted runners before relying on that setup.

## Cross-member topic completion

Committed `proposals[prefix][topic].dependsOn` arrays hold directed edges to
other member prefixes in the same topic. Propose upstream members first: new
proposals record all already-proposed members of that topic. Re-proposing keeps
the original edges to avoid cycles. Edit the arrays to correct direction or
backfill older proposals, which default to no edges. This ordering is explicit;
rig does not infer a package graph. Independent changes belong in separate topics.

`propose --from` already records the topic and destination fork branch. The
checker queries upstream PRs with that fork owner and branch, then fetches the
PR details. Current head SHA comes from those details, so added commits and
squashed proposal commits need no checkout ancestry. Ambiguous branch reuse is
reported as could not check. The committed manifest must be supplied explicitly
in the checkout used by the action; a shallow pull_request merge checkout need
not contain the original proposal commit, nor any head trailer.

Cursor inclusion is checked in **upstream history** with GitHub compare,
from the API's `merge_commit_sha` to the manifest cursor. `ahead` and `identical`
pass. A merge commit, a squash commit, and the final upstream rebased commit
are therefore evaluated under the same rule; the original PR head is never
used as the ancestry witness. Fused and filtered josh hashes are never compared.
The `lastSync` contract is an unfiltered upstream SHA. Missing merge metadata,
inaccessible private upstreams, comparison errors, and missing cursors remain
non-green, distinctly labelled could not check. Closed unmerged PRs remain pending.

Status publication uses current PR head SHAs and optional `statuses: write`.
A denied write produces a workflow warning and job-summary text and does not
fail the job. No privileged pull_request_target workflow is added. The action
adds only one input and a reporting step; the Go verb owns all API logic.

Automatic refresh is opt-in through the ready-to-copy scheduled workflow at
`examples/github-workflows/stack-dependencies.yml`. Copy it into each dependent
member repository, pin a released rig version, and maintain the latest committed
manifest on that repository's default branch. On schedule (or workflow_dispatch)
`rig stack deps --publish` discovers PR heads through the API without a PR payload.
After an upstream merge and a committed cursor bump in that manifest, the next
hourly sweep can turn the status green. Nothing is installed by default; a stale
manifest continues to block. This example has not been exercised on real GitHub.

Publication is restricted to the member upstream matching `GITHUB_REPOSITORY`.
Unset or unmatched scope refuses publication with a clear warning. Other member
states are read, never written. Tokens only need status-write access to the current
repository and read access to the dependencies (including private ones). Local
reads reuse `stackAuthForURL` / `gitrepo.CredentialFor`, which asks gh first and
Git helpers second, only when no GITHUB_TOKEN is supplied. Lookup is lazy so a
stackspace without dependency edges makes no API or credential calls.

The upstream cursor invariant is established in `stackPullOne`
(`internal/rig/cli/stack.go`): `tip` comes from `stackUpstreamTip` (or an explicit
upstream import cursor); fork imports replace it with `repo.MergeBase` of the
**unfiltered** fork and upstream commits. Only `fetch` goes through josh.
`stackSetCursor(src, m, name, tip)` persists that unfiltered upstream witness,
not `fetched` or the stackspace merge's HEAD. `TestStackFlow` in
`internal/rig/cli/stackflow_test.go` asserts the rebuilt fork member's cursor
matches `newTip` from the upstream bare repository, and that a later pull records
the bare upstream's main SHA. The compare API therefore receives commits in the
upstream repository; manually supplied invalid cursors still report could not check.

The optional sticky comment is deferred: status and local output already show
the chain, and comments would require another permission and upsert lifecycle.
Local bare Git histories and fake API responses test merge, squash and rebase
transitions; real GitHub token permissions, event delivery, and merge metadata
remain integration verification for a real repository.

## CI warning guard: observed diagnostics and exact identity

`rig stack ci-guard <project.csproj>` builds a single .NET project. Plain-clone
eligibility comes only from its evaluated `StackOnlyWarning` items. The stack
context comes from `RigStackspace` and `_StackPresent` in the wired overlay.
No build-file import is needed in a plain clone. Existing manifest-gated member
imports retain their silent no-stack behavior.

Do not use project-level `NoWarn`: it erases both fusion and unrelated warnings
of that code, and erases the evidence needed for an honest count. Local .NET 10
fixtures establish that per-PackageReference `NoWarn="NU1605"` does scope to that
package while an unrelated package downgrade still fails. However a swapped
PackageReference is removed, so that metadata is unavailable for the source
reference. Retaining a package reference solely for suppression would change the
wiring semantics and is not acceptable.

The guard instead keeps events observable through `WarningsNotAsErrors`. Wire
emits a target before `_GenerateRestoreProjectSpec`, after items exist and before
NuGet copies policy into the restore graph, gated on `RigStackCIGuard=true` and
nonempty `_StackPresent`. The graph policy alone is insufficient: the outer Restore task also reads
policy at evaluation, and the SDK explicitly promotes NU1605 in
`WarningsAsErrors`. The wrapper supplies the candidate demotions to the outer
build and removes only eligible codes from its evaluated `WarningsAsErrors`
list, preserving all other entries. This temporary demotion is not
standalone suppression: only the guard may use that switch. It validates every
observed event: exact project, exact code and complete message matched against
`project.assets.json` logs, and `libraryId` equal to a swapped identity. Unmatched
NU/MSB infrastructure warnings remain visible and fail the guard, including non-swapped packages with
the same code. Generated reference metadata removes member-specific global
warning lists before entering referenced projects. References outside the member
also remove global `TreatWarningsAsErrors`; a guard-only target disables compiler
promotion there. The member is the first prefix directory below the overlay
root, matching the manifest's single-directory member names. This applies even
when a sibling normally sets `TreatWarningsAsErrors=true`. Sibling warnings, including compiler, analyzer and SDK diagnostics such as
`NETSDK1138`, remain visible, counted and non-fatal, while new member
warnings still fail unless held by an explicit declaration or tolerated by that
project's own `WarningsNotAsErrors` policy. Warning lists are merged for the
entry project and evaluated separately for other member projects; its original
list entries are preserved. Strict mode keeps sibling warnings fatal too.
Restore and assembly warnings outside exact eligibility still fail closed. No MSB3277 exemption is
emitted: a warning code cannot establish that both assemblies were swapped.

The job summary counts distinct observed diagnostic records, never declarations
or possible suppressions. Repeated console records are deduplicated; no event
means a count of zero. `--strict`/action `strict: 'true'` bypasses demotion and
classification. A failing build remains failed even if some events were eligible.

Current local fixtures prove plain declared diagnostics are observed and held,
real CS0168 failures in both modes, strict-mode failure, summary contents,
package-scoped NU1605 metadata behavior, and observed NU1603/NU1608/NU1605
holdback when explicitly declared in plain mode. A real strong-named assembly
conflict produces MSB3277 and fails the guard in stack mode.
A package minimum/exact constraint against a lower-version source project did
**not** produce NU1605 on .NET 10; the clean stack build is not evidence that a
fusion event was held. Successful real stack-mode fusion-event classification
and exact MSB3277 assembly filtering still require proof. The real
reference member project is unavailable; its platform matrix is not
verified here. Tests invoking the SDK require `RIG_STACK_E2E=1` and local feeds.

Runtime warning log-message patterns are not implemented. Stack-mode fusion
holdback remains unproven and unconfirmed against a real stack: NuGet accepted
the swapped project whatever the tested package minimum/exact constraint, so
the predicted warnings may not occur in that form. The sibling and member-policy
regression fixtures were run red against the preceding implementation, then
green after the fix; they also check strict and new member-code failures.


Project attribution compares existing files with `os.SameFile`; entry-project
and member-root paths resolve symlinks and Windows long names before relative
path calculations. Sibling attribution compares actual ancestor directory
identities, so drive case, path case and 8.3 aliases do not change ownership.
Missing paths and symlinks escaping the stack do not qualify as siblings.
Non-gated tests exercise differently named links to real project files and
Windows case/drive/available 8.3 aliases through the diagnostic classifiers.
The normal Windows test job does not set `RIG_STACK_E2E`, so real SDK guard
builds remain gated there; these path tests require neither that switch nor
an installed SDK. The 8.3 subtest skips if the volume has no short-name aliases.

Plain-clone entry-project warning lists are scoped with a temporary
`CustomAfterMicrosoftCommonTargets` hook. It imports the entry project's
original hook, then removes the guard hook and both warning-list global
properties from project references. Referenced projects evaluate their own
hooks and lists, with real new warnings still fatal. Observed tolerated warnings
are checked against each project's original policy, including references outside
the entry project's directory. The temporary hook is removed after the build.

Metadata queries disable the MSBuild console logger, parse stdout only, preserve
stderr for evaluation errors and set `DOTNET_NOLOGO=1`. Build diagnostics keep
both streams. Diagnostic parsing strips CRLF line endings and shares a single
mixed-case alphanumeric ID grammar with `StackOnlyWarning` validation, including
analyzer IDs such as `xUnit1004`. Sibling compiler, analyzer and SDK warnings
follow the sibling policy; NU/MSB infrastructure warnings still require exact
swap attribution or fail closed.

The guard is scoped to `ci`'s build phase. The test phase deliberately executes
the configured command separately; a build triggered by that command receives
none of the guard's temporary policy. A new test mode is not required: set
`guard-project` to the test project, then use
`dotnet test tests/Acme.Tests.csproj --no-build` for the same project,
configuration and target frameworks. The guarded build must produce those test
outputs first. A test command that builds another graph can fail on sibling
warnings under its own policy.

## Returning to published NuGet packages

`rig stack released <member> <sibling> --package Acme.Lib` checks up to the
20 newest stable versions returned by a NuGet flat-container index. An explicit
`--version 2.0.0` checks only that version. `--feed` accepts a flat-container base
URL (default public nuget.org), not a v3 service index. There is one 20-second
budget including GitHub reads; HTTP clients also have a 15-second timeout.
Feed failures, including authentication failures, mean **can't check**, never
not released. No registry credentials are copied or inferred for custom feeds.
Private feeds requiring authentication are currently reported as inaccessible.

Evidence is a nuspec repository URL matching the sibling upstream and a full
repository commit SHA. GitHub compare must report ahead or identical from
**every** recorded proposal's upstream merge SHA and the manifest's unfiltered
upstream cursor to that package commit. PR discovery shares `stack deps`' fork
branch lookup and ambiguity checks. The merge SHA handles merge, squash and
rebase histories without assuming the original PR head survives upstream.
Open proposals block migration too. No proposals, missing cursors, missing
metadata, inaccessible comparisons and ambiguous branches cannot establish a
release. A partial release is insufficient. The report says exactly that all
recorded proposals and the cursor were checked.

Version numbers and dates never prove containment. This implementation does not
fall back to tags: even a tag named for a version needs an explicit repository
and packaging convention before it is useful evidence. Missing repository
commit metadata therefore says **can't tell**. Registries other than NuGet and
authenticated feed support remain future work.

Guards identify packages, using the same normalized package ID as the .NET
overlay, rather than topics. The selected package must be produced by the sibling and be reachable through
the discovered package graph. All recorded sibling proposals must
be included; the cursor additionally bounds recorded upstream work. Unrecorded
fork work and a guard's actual feature requirements cannot be inferred. Keep
proposal records current and review the preview. For a transitive sibling, a published direct delivery package must explicitly
require the verified sibling version or higher. `--via-package Acme.Control`
disambiguates multiple delivery paths; `--via-version 4.0.0` selects a release.
Otherwise up to 20 newest stable delivery releases are checked. The tool bumps
that direct pin and removes only the selected sibling's guards; it does not
remove the delivery package's own guards.

Every nuspec dependency group must include the sibling with a provable minimum
at least as new as the verified release. Bare NuGet versions mean inclusive
minima; `[2.0.0]`, `[2.0.0,3.0.0)` and `(2.0.0,3.0.0)` can establish a minimum;
floating versions and unbounded lower ranges cannot. Groups missing the sibling,
older minima, invalid ranges or excluded compile assets block migration. All
groups are checked rather than guessing evaluated target frameworks. Only an
explicit immediate dependency in the delivery nuspec qualifies; deeper chains
require an explicit delivery dependency or a manually added direct reference.

If no checked release qualifies, guards and pins stay unchanged. The report names
the required sibling minimum and recommends migrating/publishing the sibling,
then its direct consumer, then the application. An exact future delivery version
cannot be predicted. Adding a direct sibling PackageReference manually is an
explicit alternative; the next run then follows the ordinary direct path.
No direct reference is silently added.

The default is read-only. `--apply` prepares edits in memory and lists files and
manual-review notes, refuses tracked or untracked dirt, and asks for interactive
confirmation. It fails off a terminal; no `--yes` bypass is provided for this
first destructive source rewrite. This follows the destructive replacement rule
even though the working tree is clean and Git can recover it. `-n` always stays
in preview mode, including with `--apply`. The local path never commits, pushes or opens a PR. Scheduled publication uses
the separate verified report/PR path described below.

Only exact `#if STACK_ID`, optional `#else`, and balanced nested directives are
rewritten, keeping the selected branch's bytes. Compound expressions, `#elif`
on a selected guard, malformed directives and non-directive symbol occurrences
refuse the entire file. As a deliberately conservative lexical fence, any file
containing the symbol and string/character literals, comments or unsupported
encoding is listed unchanged; this includes verbatim and raw strings. A future
C# lexer can widen eligibility. UTF-8 BOM and CRLF are preserved. UTF-16 is not
rewritten. Generated files and bin/obj/vendor/node_modules/packages/generated
and .vs directories are skipped; symlinks are not followed.

Before preparing any edit, a static pin preflight inspects projects and build
props/targets in the member plus automatic Directory.Build.props/targets and
Directory.Packages.props files in ancestor directories. VersionOverride,
PackageReference/PackageVersion Update or Remove, unresolved/property-based
versions or identities, wildcard updates, default item metadata, and selected
references introduced by build props/targets refuse the whole migration. Explicit
imports require MSBuild evaluation and are refused in this first implementation;
files are never silently assumed harmless. The ordinary source-only overlay
condition `'$(UseStackSources)' != 'false'` is ignored for this package-mode proof.
Every project must receive the selected package through a literal direct pin,
a versionless reference to its nearest enabled central file within the member,
or a simple local ProjectReference to a project already proved. Conditional,
external, or metadata-bearing project reference paths cannot establish delivery.
An unrelated or shadowing central file is insufficient. Transitive migration also
checks the guarded sibling for explicit pins that could override the delivery
package dependency; those require a separate direct migration. Failure returns no edit
plan and leaves all pins and guards intact; reconcile complex MSBuild rules
manually. Static discovery is not a replacement for evaluating arbitrary MSBuild.

Version edits use XML token offsets, preserving surrounding bytes, comments,
BOM and line endings. Literal Version attributes on PackageReference and central
PackageVersion items (including Directory.Packages.props) can be updated.
Floating versions, unsupported version syntax, downgrades,
conditional references and nested version metadata refuse guard edits. A
versionless self-closing PackageReference can use an updated central pin; no
literal pin means no migration. All version comparisons use NuGet's four numeric
components (absent components are zero: 1.0 equals 1.0.0.0), followed by
case-insensitive prerelease identifier ordering; metadata does not affect
precedence. Numeric components and numeric prerelease labels must fit a nonnegative 32-bit
integer; overflow labels are refused rather than assuming SemVer ordering.
This matches the supported subset of [NuGet's version comparer](https://github.com/NuGet/NuGet.Client/blob/dev/src/NuGet.Core/NuGet.Versioning/VersionComparer.cs). Unknown
syntax and ambiguous legacy prerelease labels fail closed. Delivery minima,
range bounds, candidate sorting and downgrade protection share this parser.
Feed requests use normalized versions, preserving nonzero revisions. The release
containment check itself remains commit ancestry, never numeric precedence.
RigStackSource_* and RigStackAvailable_*
conditions are listed and never rewritten.

StackOnlyWarning carries no package identity. Explicit `--warning ACME0001`
attribution removes only an unconditional self-closing declaration containing
that single Include attribute. Other declarations are listed unchanged. Users
must attribute those warning IDs during review; guessing from an ID could
remove a warning for another unreleased sibling.

`stack status --releases` annotates included merged dependencies with
`Acme.Lib released in X.Y.Z` when discovery and evidence succeed. Release checks are opt-in because discovery can scan a large repository. The
default status and stack deps do no release discovery or registry work. Status shares
its existing two-second optional dependency-report budget; it makes no new
registry call unless a merged proposal was first verified in the cursor.
Discovery is lazy and performed once for the entire report, and repeated
member/sibling annotations are cached within that report. The same context
deadline reaches discovery, credentials, GitHub and feed requests. Offline or
exhausted budgets simply omit release annotations. A hanging-feed fixture measured
2.003 seconds for the two-second budget, including one discovery call. There is no
persistent cache of release claims.

Fake-server tests exercise repository metadata and ancestry outcomes; golden
rewrite fixtures preserve exact bytes, including BOM/CRLF. The gated
RIG_STACK_E2E=1 fixture packs a local NuGet library, rewrites a consumer, then
builds it with UseStackSources=false. Hosted GitHub and private-feed integration
remain unverified.

## Push after squash/rebase delivery (2026-11-02)

The inverse-filter identity assumption in the original push design does not
hold after upstream squash-merges a proposal. Josh may retain the pre-squash
line even after pull imports an identical tree. Both delivery verbs now use
`stackDeliveryUnsent` to compare the member tree with the upstream tip tree.
Equal content means no unsent work, independently of commit identity.

For differing trees, ordinary descendant filtered history is retained. Otherwise
push requires an exact upstream-tree match on the filtered first-parent line
and reparents each subsequent linear, nonempty commit individually on upstream.
This keeps authors, committers and complete messages, rather than silently
squashing owned history; SHA identities change and signatures are stripped
because their signed ancestry changed. A merge in that replay range is refused.
No matching boundary means refusal, not patch-equivalence guessing: partial
squashes and overlapping upstream edits must not silently discard local work.

Member removal deletes its prefix, so filtering can produce remove/cursor
commits, despite manifest-only edits normally disappearing. A restored tree
matches upstream; following new work is rebuilt past that matching boundary
when administrative remove/cursor history is present. Such operations are not
member changes to export. This needs no reset flag.

Before preview and again immediately before writing, push verifies that the
planned commit descends from the observed upstream tip, regardless of the
server's receive policy. The final write is a plain non-force push. Both verbs
share the unsent decision, so status needs no disagreement warning; push may
still refuse an ambiguous history that propose can present as a reviewable diff.
Dry run restores the temporary filtered ref even on planning errors; it does
not move HEAD, manifest, cursor or remote. Git objects and filter caches may be
created while planning, as they were before.

## Scheduled release migration trust boundary

`stack released --report <file>` serializes the release/delivery proof and exact
migration contents without editing the source tree. Report summaries go to
stderr so stdout stays clean. A temporary file in the destination directory is
written, synced and closed before rename, so existing Unix descriptors cannot read the
replacement contents. Unix mode is 0600; on Windows, callers need a private
directory ACL because mode bits only control the read-only attribute. `not yet` is successful for
scheduled runs; missing evidence and feed failures remain errors. `--seed`
reloads an unfused manifest home and reads shallow default branches, independently
of any build checkout. NuGet-only discovery is built in and static.

`--publish <report>` requires a trusted `--seed` and matching command selections
(package, feed, explicit versions, delivery selection and attributed warnings).
The example repeats trusted member/sibling arguments too. With no positionals,
publish reads the pair from the report and validates manifest membership. It
reloads upstream state, repeats proposal/cursor ancestry and immediate delivery
checks, proves pins, and recomputes exact edits before any write. All report
fields must match; bases that moved require a new report. A member already pinned
to the delivery version or newer is a no-op even when the report is stale, since
that path uses no artifact contents and performs no write.

The reader clones bare and materializes raw blobs, bypassing checkout filters
and archive attributes. No repo commands, hooks, custom adapters, or MSBuild are
run. Member symlinks, submodules and LFS are refused. The fresh publisher uses
the Git data API to replace only independently derived files on the default
branch tree. `rig-released/<sibling>` and a body marker identify one proposal
per member/sibling. Branch lookup precedes PR creation, and re-runs update its
title/body. A PR targeting a different base is refused before writes. An existing
head with the intended tree and sole parent/base is retained; only a changed
tree or base creates a new commit and moves the ref. Ref paths and head queries
escape branch names, including allowed `#` characters. Workflows serialize this namespace.
PR creation denied by repository policy may leave that deterministic branch;
the summary names the Actions PR setting and retry reuses it, matching canary
recovery. Only write HTTP 403/404 responses use that successful summary path;
read errors, transport failures, 5xx and other API failures return errors with
diagnostics. HTTP status is retained without printing upstream response bodies.
No destructive local rewrite is added. `--apply` remains clean-tree,
interactive and confirm-first.

The example calls rig directly, following the canary rather than adding a
composite action mode. Both jobs install reviewed version-pinned module source
verified by Go's checksum database on fresh hosted runners without caches or
member checkout. The build job has contents read; publication needs member
contents/PR write and upstream read. A manifest-home workflow targeting another
repository needs a publish-only token with those cross-repository permissions;
Git read credentials are independently configured on both runners.

Fake services and local bare histories establish artifact refusal, exact edits,
idempotence, hand-migration no-op, permission recovery, and unchanged complex
guards. A hosted scheduled run with a real release remains integration work.
The existing gated local package-mode build fixture still applies to the shared
rewriter. npm/other registries and delivery deeper than one verified transitive
hop are not implemented.
