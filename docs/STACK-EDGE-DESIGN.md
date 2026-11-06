# stack edge: shipping carried fixes under your own ids

> Status: **proposed** (2026-11-06). Nothing here is implemented. It builds on
> four parts of [STACK-DESIGN.md](STACK-DESIGN.md) and should be read after them:
> "Republished ids (#259)", "Proposing one fix out of several", "Member CI
> reconstruction" and "Returning to published NuGet packages". Every claim
> about current behaviour below names the function it was checked against.

## The job

You contribute fixes to a library you do not own, `acme/Lib`, from a
stackspace. The fixes leave as pull requests from your fork, `you/Lib`. The
next release is weeks away, and your app needs the fixes now. You do not want
to maintain the library or take it over. You want a **stop-gap**:

- upstream plus the fixes you carry, published to a private feed,
- under ids of your own (`Lib` → `You.Lib`, `Lib.Controls` → `You.Lib.Controls`)
  with the **same assemblies and namespaces**, so switching back is a change of
  `PackageReference` id and nothing else,
- versioned as a prerelease above the release it is based on
  (`<next upstream version>-edge.<n>`),
- and **retired** when the fixes ship upstream.

We call that an **edge** build. It has been set up by hand twice. This document
turns what those two setups ran into into a design that rig can carry, so the
setup is quick, consistent and repeatable from one project to the next.

### Why a separate document

STACK-DESIGN.md records how a stackspace works: import, propose, overlay, CI,
release. Edge adds no mechanism to any of those. It is one workflow that
*consumes* five of them (trackBranch, publishing ids, `pack`, seed
reconstruction, `released`) and adds one lifecycle on top: set up, publish,
retire. Written as a section, it would be spread across the places it reads
from. STACK-DESIGN.md carries one pointer to this file, beside "Republished ids".

## What the manual setups ran into

Each row is something the hand-built setup needed. The right-hand column is
where this design deals with it. The rest of the document explains each answer.

| # | Needed by hand | Answer here |
|---|---|---|
| 1 | A second fork (`acme-app/Lib`), used only to publish, because PRs had to keep coming from `you/Lib` | **Roles, not a list of forks.** `fork` stays the PR fork. Publishing runs in the manifest home by default. A separate publishing repo is an optional role, and it never needs upstream's source ([Manifest](#the-manifest-edge-block)) |
| 2 | A hand-kept list of `<repo> <branch>` lines, merged onto upstream main at build time | **Gone.** trackBranch already holds upstream plus everything carried ([trackBranch](#build-from-trackbranch)) |
| 3 | A hand-made integration branch, because two carried fixes conflicted | **Gone.** The resolution is made once, on the stackspace's integration line, and trackBranch carries the resolved tree |
| 4 | `PackageId` renamed at pack time through `CustomAfterMicrosoftCommonTargets`, checked by reading nuspecs | `edge pack` generates that hook from `publishPrefix`/`publishesAs`, and verifies the nuspecs itself ([Identity](#package-identity)) |
| 5 | Version `<next build>-edge.<run number>` | Computed by rig; the rule is in [Versions](#versions) |
| 6 | A ~176 MB sample package that had to be left out | `edge.exclude`, plus a size report on every pack |
| 7 | Upstream's workflows disabled on the publishing fork | Not needed when the home publishes. With a publishing repo that is a fork, `edge init` disables them and `doctor --edge` re-checks after every sync |
| 8 | The feed's push key set as a repo secret; GitHub's secrets API returned 5xx twice | `edge init` sets it with bounded retries, then reads the secret's metadata back to confirm |
| 9 | `publishPrefix` in the manifest, so `wire` still resolves `You.Lib` from source (the #259 failure) | The same key now drives the rename, so a member cannot be republished without it, and `doctor --edge` checks every `You.*` reference is redirected |
| 10 | Checking by hand whether each fix's PR was merged *and* released | `edge status`, built on `released`'s evidence model; retirement is a reported outcome ([Retirement](#retirement)) |

## Build from trackBranch

The hand-made list and integration branch (items 2–3) existed only because no
`trackBranch` was set. With topics, rig already writes the branch an edge build
wants. That claim holds, but only once you are precise about what trackBranch
holds. The code (`newStackSendCmd`, the `--from` path in
`internal/rig/cli/stack.go`) says:

- **What it holds.** One commit. Its tree is `HEAD:<prefix>`, read when
  `propose` starts (`fullTree := tree`), so it is whatever the checked-out
  branch carries for that member. Its single parent is the upstream tip, which
  the stale-cursor guard has just required to equal the cursor
  (`tip != m.cursor(name)` refuses). So the branch reads as "upstream at the
  cursor, plus everything this stackspace carries", and conflict resolutions
  made on the integration line are in it, because they are in `HEAD`'s tree.
- **How it moves.** Force-pushed under a lease (`PushRefForce`, which uses
  `--force-with-lease` against the ref's current value) and recorded locally
  as `refs/rigsmith/integration/<name>`.
- **When it moves.** Only when `propose --from` actually pushes a proposal.
  It does **not** move on:
  - `pull`,
  - a whole-prefix `propose` (no `--from`),
  - `propose --from` exits that report "nothing to send" (the topic matches
    upstream, or the fork already holds the proposal), which return before the
    integration push,
  - `--dry-run`.

That gives an edge build three gaps to close. None is a new mechanism.

1. **trackBranch is `HEAD`'s tree, not the integration line's.** Run
   `propose --from` while a topic branch is checked out and trackBranch becomes
   that topic alone. A rebuild, and therefore an edge build, then silently drops
   every other carried fix. Nothing checks this today. **Decision:**
   `propose --from` refuses when the topic is not an ancestor of `HEAD`,
   because a `HEAD` that does not contain the topic being proposed cannot be the
   integration line. This protects every rebuild, not only edge.
2. **Nothing refreshes trackBranch after a pull.** After `pull`, `HEAD:<prefix>`
   is the new upstream plus the carried fixes. trackBranch still has the old
   base until the next `propose --from` pushes, and that needs a topic rooted
   after the new import (the topic-staleness guard). In practice, a pull leaves
   trackBranch stale until a human re-roots a topic. **Decision:**
   `rig stack propose <member> --track-only` performs just the integration
   push, with the same guards (cursor equals upstream tip, clean prefix, and the
   new ancestry check for every recorded topic of that member that still exists
   locally). `propose` stays the only writer of trackBranch; edge only reads it.
   `pull` does not push it: `pull` already publishes the home, but a second
   remote write to a member's fork is not something a cronnable verb should
   take on.
3. **Upstream moves while nothing is re-proposed.** trackBranch is then
   upstream-at-an-old-cursor plus the fixes. **Decision:** by default, edge
   builds trackBranch exactly as it is. That is the combination the stackspace
   last had in front of it when it proposed, and the version is computed from
   the upstream commit actually built, so it is honest. `--upstream-tip` is an
   opt-in mode for scheduled runs. It three-way merges trackBranch onto the
   current upstream tip in the temporary repository (`git merge-tree
   --write-tree`, base = trackBranch's parent). A conflict is **inconclusive**,
   with the remedy "pull, resolve on the integration line,
   `propose --track-only`", never a guess. A clean merge builds and tests
   before anything publishes. Provenance records both the trackBranch commit
   and the tip.

### Not a mode of `stack ci`

`stack ci` cannot be reused unchanged. It clears `TrackBranch` and
`LastPropose` for every member before rebuilding (`newStackCICmd`: "CI cursor
mode must not silently select a proposed fork branch"). That is right for
member CI, which has to test the PR's tree against siblings' recorded cursors.
Edge needs the opposite for the edge member: init's ordinary fork import
(`stackImportFromFork` → `stackForkRefFor`, explicit `trackBranch` first),
where the cursor is set to `merge-base(trackBranch, upstream tip)` over the
unfiltered objects (`stackPullOne`). So edge reconstruction **shares
`stack ci`'s parts**: the read-only seed loader (`stackCISeed`), the temporary
fused repository, the remote-free guard (`stackCIGuardPush`) and the host
allowlist. It **differs in one rule**: edge members keep their trackBranch, and
non-edge siblings are normalised to their cursors exactly as `stack ci` does.

### What counts as "carried"

trackBranch is everything the integration line carries for the member. That
includes commits that were never proposed. This is deliberate: the stop-gap is
for "what my app needs", and a fix not yet proposed is still needed. It is also
why retirement cannot be decided from proposal records alone (see
[Retirement](#retirement)). `edge status` reports unproposed content as its own
line, so it is never a surprise.

**Someone else's PR.** The manual setups pinned a foreign PR's sha in the
build-time list. Here it is brought into the stackspace as a topic: a branch
rooted on the member's import commit, holding that PR's change merged onto the
cursor tree, merged into the integration line like any other topic. Its content
then reaches trackBranch, and any conflict is resolved once, like item 3. What
the repository cannot answer is *whose PR it is*. `proposals` records only what
`propose --from` sent from your fork (`stackDepsAPI.proposal` looks up PRs by
`<fork owner>:<branch>`), so a foreign PR has no record and `released` cannot
see it. The edge block therefore records it, as `adopted: [{pr, commit}]`,
purely so `status` and retirement can track it. A build-time merge list would
bring back items 2 and 3, so there is not one. Bringing the PR's change in as a
topic is a small verb of its own (`rig stack edge adopt <member> <pr-url>`).
It is listed under open questions because no stackspace verb imports a foreign
tree into a topic today: only `stack ci --topic` materialises PR heads, and only
in a temporary repository.

## Package identity

### One source of truth: the publishing ids

`publishPrefix` and `publishesAs` already exist per member. Today only `wire`
reads them (`stackManifest.publishing` → `stackRedirects`), so that a consumer
referencing `You.Lib` inside the stackspace is redirected to the project
producing `Lib`. Edge makes them **the** rule for the rename too: explicit
`publishesAs` entries win, and `publishPrefix` covers the rest. That is the
same precedence `stackRedirectsFromScan` applies. Two consequences:

- Item 9 cannot be forgotten. A member cannot be edge-published without the key
  that makes `wire` redirect its ids.
- **Edge never publishes an original id.** Every package it packs must resolve
  to a republished id, or `edge pack` refuses. An original id on a private feed
  shadows the public package for every consumer that has that feed configured,
  which is the dependency-confusion failure in reverse.

### The rename: a temporary hook, not the committed overlay

The brief suggested putting the rename into the overlay rig already generates,
under a property switch. **Pushback: use a generated, temporary
`CustomAfterMicrosoftCommonTargets` file, as the manual setup did.** The
reasons, from the code:

- **The overlay is not always there.** `wire` writes the root
  `Directory.Build.targets` only while a reference crosses between members, and
  removes it when none does. A stackspace holding only `acme/Lib`, with the app
  outside, has no overlay to put a switch in.
- **The overlay is committed and checked.** It is a root file, outside every
  prefix, so it would not leak into a proposal. But `doctor` compares it with
  what `wire` would write, and the seed publishes it. Packaging concerns in it
  widen what every stackspace build evaluates, for a feature most stackspaces
  never use.
- **rig already does this.** `stack ci-guard` builds a plain clone with a
  temporary `-p:CustomAfterMicrosoftCommonTargets=<file>`, written to a temp
  directory and removed afterwards, that imports the project's original hook
  when there is one (`stackGuardPlainTargets`). Edge reuses that chaining, so a project that sets its own hook
  keeps it.

One difference from ci-guard matters. ci-guard adds
`CustomAfterMicrosoftCommonTargets` to `GlobalPropertiesToRemove` on project
references (`stackciguardprocess.go`), so its hook stops at the entry project.
Edge needs the opposite. The hook has to reach every referenced project,
because the package-to-package dependencies in the nuspec take the *referenced*
project's `PackageId`. That is how the manual setup's dependencies followed the
rename. So edge leaves the global property to propagate.

The hook keys on the **id the project produces**, as discovery reported it,
not on `$(MSBuildProjectName)`. The manual hook's `You.$(MSBuildProjectName)`
is right only where the id equals the project name. For each republished
package, rig writes one conditional assignment:

```xml
<!-- generated per edge pack, in a temporary directory; never in the tree -->
<Project>
  <!-- the project's own hook, if it had one: written in literally, imported as ci-guard does -->
  <PropertyGroup><_RigEdgeOriginalHook>/path/to/original.targets</_RigEdgeOriginalHook></PropertyGroup>
  <Import Project="$(_RigEdgeOriginalHook)" Condition="Exists('$(_RigEdgeOriginalHook)')" />
  <PropertyGroup Condition="'$(PackageId)' == 'Lib' or ('$(PackageId)' == '' and '$(AssemblyName)' == 'Lib')">
    <PackageId>You.Lib</PackageId>
  </PropertyGroup>
  <PropertyGroup Condition="'$(PackageId)' == 'Lib.Controls' or ('$(PackageId)' == '' and '$(AssemblyName)' == 'Lib.Controls')">
    <PackageId>You.Lib.Controls</PackageId>
  </PropertyGroup>
</Project>
```

Whether `PackageId` has already been defaulted from `AssemblyName` when this
hook evaluates depends on the SDK's import order. The fallback condition covers
both orders, but that is a spike to run, not a fact (see open questions). The
nuspec check below catches it either way.

Only `PackageId` changes. `AssemblyName`, `RootNamespace` and the source are
untouched, which is what makes switching back a one-attribute change for the
consumer. Nothing is written under any member prefix.

### Plumbing this needs

- **The version** needs no protocol change. The .NET adapter already passes
  `-p:Version=<Package.Version>` as a global property (`packArgs`), so edge
  sets the package's version before calling `Artifacts`.
- **The hook** does. `ArtifactsRequest` has no way to pass extra MSBuild
  properties, and the .NET adapter does not apply `req.Env` to `dotnet pack`.
  An additive `Properties` field on `ArtifactsRequest` is the clean seam. An
  adapter that cannot honour a republished id must refuse rather than ignore
  it, the same contract `Channels` already states. Ignoring it would publish
  original ids to the private feed. Edge requires an adapter to declare the
  capability.
- **The output listing** already copes with the rename. The adapter predicts
  `<Package.Name>.<version>.nupkg`, which under a rename names a file that does
  not exist. `stackPack` lists what it observed appear in the output directory
  (`stackPackNewFiles`), and adds a reported path outside that directory only
  if it exists (`stackPackOutside`). Edge verification works from those
  observed files, never the prediction.

### `-p:Version` propagates too, so cross-member dependencies need care

A global property reaches every referenced project. Inside the stackspace a
cross-member `PackageReference` has become a `ProjectReference` (the overlay
swap), so a member's nuspec would declare a dependency on the sibling's id **at
the edge version of the member being packed**. That version exists on no feed
unless the sibling was edge-packed in the same run with the same number.
**Decision for v1:** edge members must not depend, through a swap, on another
member's packages. `edge pack` refuses and names the dependency. Packages
within one member (`You.Lib.Controls` → `You.Lib`) are the common case. They
share one version, which is exactly right. Cross-member edge closures are an
open question.

### pack verifies its own output

The manual setups checked nuspecs by eye. `edge pack` opens every `.nupkg` it
observed being produced and refuses unless all of these hold:

1. `<id>` is the republished id of a package the member produces, and not
   excluded.
2. Every dependency naming an id **this member produces** names its republished
   id, at the edge version. An original id there means a project the hook
   missed.
3. No dependency names an id **another member** produces (the v1 rule above).
4. Dependencies naming anything else (exact third-party pins included) match
   what discovery reported for that project. The rename must not move them.
5. Assembly names are unchanged. This is checked by evaluating `AssemblyName`
   for each packed project with and without the hook, and comparing. A project
   whose `AssemblyName` is derived from `$(PackageId)` would otherwise ship
   renamed assemblies, and switching back would stop being a one-line change.
6. Each package's size is reported. One over `edge.maxSize`, if it is set,
   refuses (item 6).

The publish job runs the same checks again on the downloaded artifacts, without
building ([CI](#ci-two-jobs-like-the-canary)).

**Repository metadata.** Where Source Link is active, the SDK fills the
nuspec's `<repository url commit>` from the local `.git`. In a reconstruction
that is a fused commit with no remote, which means nothing. `released` reads
exactly that element as release evidence (`stackReleaseVersionProof` matches
the URL to the sibling's upstream). So edge sets `RepositoryUrl` to the PR fork
and `RepositoryCommit` to the trackBranch commit it built. The provenance is
then honest, and an edge package can never pass for an upstream release.

## Versions

`<base>-<label>.<n>`, label `edge` by default.

- **`n`** is the CI run number (`GITHUB_RUN_NUMBER`). Gaps from failed runs
  are fine. NuGet compares dot-separated numeric prerelease identifiers
  numerically, so `edge.10` sorts above `edge.9`. rig's own comparer
  (`stackCompareNuget`) agrees, and its parser (`stackParseNugetVersion`)
  refuses numeric labels that overflow 32 bits. A local
  `edge pack` without `--number` uses `0` and refuses to publish.
- **`base`** must sort above the release the build is based on. Upstreams
  disagree about what the tree says. Some bump the version straight after
  releasing, so the tree already names the next release. Others leave it at the
  last release. MinVer-style projects declare nothing, and discovery returns an
  empty version for them. The proposed rule: let *R* be the newest stable
  version of the original id on the public feed (`stackReleaseCandidates`, the
  lookup `released` already does), and *T* the in-tree version at the upstream
  commit built. If *T* > *R*, base = *T*. Otherwise base = *R* with its patch
  incremented. The edge build then sorts above *R* and below any upstream
  release ≥ base, which is when it should retire anyway.
- **Monotonicity.** A run number resets if the workflow is recreated. The
  publish job therefore reads the feed's newest `<base>-edge.*` and refuses a
  number that does not increase. That read uses the same credential as the
  push, so it does not hit the anonymous-read gap below.

## The manifest: `edge` block

A top-level `edge` map keyed by member, beside `repos`:

```jsonc
{
  "repos": {
    "lib": {
      "upstream": "github.com/acme/Lib",
      "fork": "github.com/you/Lib",          // the PR fork: where proposals come from
      "trackBranch": "stack/integration",    // written by propose; what edge builds
      "publishPrefix": "You."                // Lib -> You.Lib, for wire AND for edge
    },
    "app": { "upstream": "github.com/you/App", "fork": "github.com/you/App", "owned": true }
  },
  "edge": {
    "lib": {
      "feed": "https://nuget.pkg.github.com/acme-app/index.json",
      "label": "edge",                                  // default
      "exclude": ["Lib.Samples"],                       // original ids
      "maxSize": "50MB",                                // optional
      "publishFrom": "github.com/acme-app/lib-edge",    // optional; default: the manifest home
      "pushKeySecret": "EDGE_FEED_KEY",                 // a secret NAME; never a value
      "adopted": [
        { "pr": "github.com/acme/Lib/pull/412", "commit": "<full 40-character sha>" }
      ]
    }
  }
}
```

Why it is shaped like this:

- **Outside every prefix.** The manifest is a root file, and `propose` pushes
  only `HEAD:<prefix>`, so nothing here can leave in a proposal. The seed does
  carry it to the manifest home, so the home sees the feed URL and the secret's
  name. Neither is a secret.
- **A top-level map, not a key per repo.** The comment-preserving writer
  (`stackWriteManifestMap` over `confkit.Writer`) replaces whole values up to
  depth 2. That covers both a dedicated file and an inline `stack` block in
  `.rig.json`. `repos.<name>.edge` would be depth 3 in a dedicated file and
  depth 4 inline. A top-level map leaves rig able to scaffold the block once
  without touching `repos`.
- **`edge init` never edits the manifest after that.** The block is
  hand-authored and carries comments, and rewriting the map would lose them. So
  `--scaffold` writes it only when it is absent. Otherwise the manifest is
  input: `init` reads it, sets up what lives elsewhere, and prints any manifest
  edit it needs (a missing `trackBranch`, a missing `publishPrefix`) for you to
  make.
- **Ids live in one place.** No `id` or `prefix` key in the edge block, for the
  reason given under [Identity](#one-source-of-truth-the-publishing-ids).
- **No list of branches.** That was item 2.
- The schema (`site/public/schemas/rig-stack.json`) gains `edge`, with
  `additionalProperties: false` like the rest. Validation adds rules the schema
  cannot express: an `edge` key must name a member; that member must have
  `trackBranch` and a publishing rule; `adopted[].commit` must be a full sha;
  `exclude` entries are reported if they name nothing the member produces (the
  way `wire` reports an unmatched `publishesAs`).

### "Multiple forks" is two roles

The first manual setup moved toward a list of forks. The code says why that
would have broken things. Proposal lookup finds PRs by
`<fork owner>:<branch>` (`stackDepsAPI.proposal`), and `released` and `deps`
build on that. Point `fork` at a publishing org and every recorded proposal
becomes "no PR found". So:

- **`fork`** stays the one PR fork. It is unchanged, and it is still where
  trackBranch lives.
- **`publishFrom`** is where publishing CI runs, if not the home. It holds no
  upstream source. Reconstruction fetches trackBranch from `fork`, so the
  publishing repo is just a place with a workflow, a secret and permission to
  push to the feed.

### Is a publishing repo needed at all?

Usually not. The manual setups forked upstream into an org because CI needed
the source. Seed reconstruction removes that need. The workflow lives at the
stackspace root (`.github/workflows/stack-edge.yml`), is committed, and reaches
the home through ordinary seed publication, which exports every root file
outside the prefixes. The home's CI then rebuilds from seed + trackBranch and
publishes. With no upstream code in the publishing repo, **item 7 disappears**.

`publishFrom` remains for one real case: the feed belongs to an owner the home
repo's token cannot push to (for example, the org's package feed while the home
is under a personal account). Then the workflow has to run under that owner, or
hold a token that can. If `publishFrom` is a fork of upstream anyway, `edge init`
disables every workflow in it except the edge one, and `doctor --edge` re-checks
after every sync, because a sync can bring in new upstream workflows, enabled.

## CI: two jobs, like the canary

The edge workflow follows `stack canary` and `stack released --publish` rather
than adding a composite-action mode. The workflow calls rig directly, so local
behaviour and CI behaviour are the same code.

1. **Build job.** `contents: read`, no secrets, checkout
   `persist-credentials: false`.
   `rig stack edge ci --seed acme-app/app-stack@main --member lib --report edge.json`
   reconstructs (keeping trackBranch for edge members), wires, runs the
   configured build and test, then `edge pack` with its verification. It
   uploads the `.nupkg`s and the report. trackBranch is your code, plus
   upstream's and any adopted PR's. It is still code, so it gets no publishing
   credential.
2. **Publish job.** A fresh runner. It runs no project code and restores no
   cache.
   `rig stack edge ci --seed acme-app/app-stack@main --publish edge.json`
   reloads the seed independently. It checks that the report's trackBranch
   commit is the fork's current trackBranch, or an ancestor of it (the report is
   build-job output, so it is a claim, not proof). It re-runs the nuspec checks
   on the downloaded packages, checks version monotonicity against the feed,
   and pushes with the key from `pushKeySecret`. The report lists commits and
   ids, never subprocess output, as the canary's does.

`edge pack` locally runs the same reconstruction in a temporary directory,
**not** the working tree. So what you test locally is what CI publishes.
`--here` packs the working tree for a quick look, and its output can never be
published.

## Verbs

All under `rig stack edge`, a group with a menu (`RunE`, per cliguard), with
`--dry-run` honoured everywhere it can be.

| verb | does |
|---|---|
| `edge status [member]` | one line per carried item (each recorded proposal, each `adopted` PR, and "unproposed content" if any): open / merged / merged and in cursor / released in *V*. Plus trackBranch freshness (locally: `refs/rigsmith/integration/<name>` tree vs `HEAD:<prefix>`; anywhere: its base vs the upstream tip), the newest edge version on the feed, and the retirement verdict |
| `edge pack [member]` | reconstruct, rename, version, pack, verify. `--number`, `--upstream-tip`, `--here`, `-o` |
| `edge ci --seed … (--report \| --publish)` | the two CI halves above |
| `edge init [member]` | idempotent setup from the manifest ([below](#setup-edge-init-and-doctor---edge)) |
| `edge adopt <member> <pr-url>` | bring a foreign PR in as a topic and record it under `adopted` (open question) |
| `doctor --edge` | the same facts as `init`, read-only |

Outside the group: `propose --track-only` (writer of trackBranch, see above),
and `released`'s extension for switching back ([Retirement](#retirement)).

`edge status` reuses `released`'s evidence model and does not invent a weaker
one. "Merged" comes from the PR's `merged_at`. "In cursor" is the same GitHub
compare from the merge sha to the cursor that `deps` uses
(`stackDepsAPI.dependency`). "Released in *V*" is the nuspec
`<repository commit>` of the **public** package, compared from each merge sha
(`stackReleaseVersionProof`). That function today returns one verdict for all
proposals together, and fails as soon as one is open. `status` needs the same
check per item, so the loop is split, and the all-together form stays for
`released`. Adopted PRs are looked up by number, since no fork branch identifies
them.

**The private-feed gap.** `released` sends no credentials to a feed
(`stackReleaseGet` is a bare GET) and reports an authenticated feed as "can't
check". Retirement does not need the private feed. Whether a fix shipped is
read from the *public* feed, which needs no auth. Only one `status` line, "newest
edge version on the feed", needs auth. Without a credential it says "can't
check". Giving `status` the push credential would mean it holds a write
credential just to read, so it is not done. Read access is an open question.

## Setup: `edge init` and `doctor --edge`

The goal is setup that is quick, consistent and repeatable across projects. So
`init` is a **fixed list of facts**. Each one is checked, then (in `init` only)
made true, then checked again. `doctor --edge` runs the same list and changes
nothing, and `init --dry-run` prints it with what each step would do. A second
`init` finds everything true and does nothing.

| fact | `init` makes it true by | item |
|---|---|---|
| the member has an `edge` block, `trackBranch` and a publishing rule covering every non-excluded package | printing the edit (never writing `repos`) | 9 |
| trackBranch exists on `fork` and holds the integration line (`refs/rigsmith/integration/<name>` tree = `HEAD:<prefix>`; every recorded topic is an ancestor of `HEAD`) | running `propose <member> --track-only` | 2, 3 |
| every reference to a republished id inside the stackspace is redirected (`stackRedirects` has a link for it), and the overlay is current | running `wire` | 9 |
| a local `edge pack -n` passes every nuspec check, with sizes reported | (a check only, so the failure is reported before CI) | 4, 5, 6 |
| the manifest home is configured, and the workflow file is at the root and published there | writing the workflow from a template, committing it by path, `seed --publish` | 1 |
| `publishFrom`, if set, exists; if it is a fork, every workflow but edge's is disabled | API calls, each confirmed | 1, 7 |
| the push secret exists in the publishing repo | `gh secret set` with the value from stdin or the publish-auth seam ([PUBLISH-AUTH-DESIGN.md](PUBLISH-AUTH-DESIGN.md)), never argv; retried on 5xx with backoff (3 tries); confirmed by reading the secret's metadata and requiring an `updated_at` newer than the attempt | 8 |
| consumers outside the stackspace can restore `You.*` | printing a `nuget.config` snippet with package source mapping for `You.*` → the edge feed | — |

Rules for the actions:

- **Outward and destructive actions confirm interactively, and fail when not
  on a terminal.** Creating a repo, overwriting a secret and disabling
  workflows are each confirmed. There is no `--yes`, following the existing
  rule for destructive commands. "Repeatable" comes from idempotence (a
  satisfied fact asks nothing), not from skipping prompts.
- **Never inside a member.** The consumer's `nuget.config` belongs to the
  consumer, and an owned member is still a member. `init` prints the snippet.
  Package source mapping matters here: without it, a `You.Lib` someone
  publishes on nuget.org would be restored in place of yours.
- **The fact list is shared code**, so `doctor --edge` and `init` cannot drift
  apart in what they consider true.

## Retirement

Retirement is an outcome `edge status` reports, not something you work out by
hand. A member is **retirable** at public version *V* when all of these hold:

1. every recorded proposal and every `adopted` PR is merged, and *V*'s
   `<repository commit>` is ahead of or identical to each merge sha (the
   `released` rule, per item), **and**
2. **nothing else is carried.** Three-way merge trackBranch into *V*'s commit,
   with base = their merge base, and get *V*'s tree back unchanged. Recorded
   proposals cannot show this, because trackBranch can hold content no PR
   carries ([What counts as carried](#what-counts-as-carried)). The check is by
   content. So a fix that upstream reworked while merging it reads as "not
   contained", and the member stays on edge. That is the safe direction.

Then the plan is to **switch consumers back**: `PackageReference
Include="You.Lib" Version="x-edge.n"` → `Include="Lib" Version="V"`. Can
`stack released --apply` do that? Not today, and the code says exactly why:

- `stackReleaseXML` matches `Include` (or `Update`) equal to `--package` and
  rewrites only `Version`. A consumer referencing `You.Lib` is not matched by
  `--package Lib`.
- The guard symbol it removes is `STACK_` + `StackSourceName(--package)`, so
  `STACK_LIB`. Source signals are named from the **referenced** id, republished
  ones included, so the consumer's guards read `STACK_YOU_LIB`.
- The pin preflight (`stackReleaseProvePins`) proves delivery for `--package`.
  It proves nothing for the republished id.

**Decision:** `released` reads the sibling's publishing rule from the
manifest. When the consumer references a republished id of the sibling, the
plan includes renaming `Include` to the original id, alongside the version
bump, using the same token-offset editing. Guards are named from the republished
id, and the pin proof runs on the republished id. One more rule is new:
**all-or-nothing per member**. Every republished id of that member moves in one
plan, or none does. A consumer left with `You.Lib.Controls` (edge) beside `Lib`
(release) has the same assembly names from two package ids, which NuGet does not
reconcile. That is a build failure at best, and at worst a silent pick of one
copy. The existing `--apply` rules are unchanged: clean tree, interactive
confirmation, no commit, no push. The scheduled `--report`/`--publish` path gets
the same rename.

After migration, `edge status` says the block can go. The edge block and
`publishPrefix` are removed by hand, since the manifest is yours. Packages already
on the feed stay: deleting them would break any consumer still pinned to one.

## Invariants

- **Edge never writes inside a member prefix.** The rename is a temporary hook
  outside the tree. The version is a command-line property. Nothing is stamped,
  and nothing reaches `.changeset/versions.json`.
- **Edge never writes a proposal and never pushes upstream.** It reads
  trackBranch from `fork`. Its only writes are the feed, root files of the
  stackspace (the workflow), and settings and secrets of the publishing repo
  (each confirmed). The one fork write in this design, `propose --track-only`,
  belongs to `propose`.
- **Edge never publishes an original id**, and never publishes a package the
  publish job has not verified itself.
- **Local equals CI.** `edge pack` and `edge ci` share one reconstruction,
  from the committed manifest and the fork's trackBranch, never the working tree.

## Failure modes, named

- **trackBranch narrowed to one topic** by proposing with that topic checked
  out. Closed by the new ancestry refusal in `propose --from`.
- **trackBranch stale after a pull.** Reported by `status` and closed by
  `propose --track-only`. Until then, edge builds the older, still coherent,
  base.
- **An edge package depending on a sibling at a version no feed has**, because
  `-p:Version` propagates. Refused at pack (v1).
- **The hook evaluated in the wrong order**, leaving an original id. Caught by
  nuspec check 1 or 2.
- **Two ids for one assembly in a consumer.** At retirement, prevented by the
  all-or-nothing rule. *During* the edge period it can still happen: another
  package the consumer uses depends on the original `Lib`, so restore brings
  both. rig cannot see the consumer's whole graph from the stackspace. It is an
  open question below.
- **A recreated workflow resetting the run number.** Refused by the
  monotonicity check.
- **Upstream workflows re-enabled by a fork sync.** Only with a forked
  `publishFrom`; caught by `doctor --edge`.
- **A report claiming more than the build did.** The publish job trusts nothing
  in it it can re-check (commit ancestry, nuspecs, versions).

## Open questions

1. **"Next build" per ecosystem.** The *R*/*T* rule is .NET-shaped and reads
   nuget.org. An upstream that publishes nightlies to its own feed has a better
   *R* than nuget.org's newest stable. Should `edge.baseFrom` name that feed?
2. **Adopted foreign PRs.** `edge adopt` needs a way to turn a PR head (an
   upstream-repo commit) into a topic under the prefix. The likely shape is
   `merge-tree` of the head against the cursor, then `commit-tree` under the
   prefix on a branch rooted at the import commit. No verb does this today. And
   when the foreign PR's head moves past the pinned sha, should `status` only
   warn, or should edge refuse until the pin is updated?
3. **Authenticated feeds.** Both for `status`'s "newest edge version" and for
   `released` against private feeds generally. Which credential source? A
   read-only feed token through the publish-auth seam is the obvious candidate.
   rig copies no registry credentials today, deliberately.
4. **Non-.NET ecosystems.** The rename that makes .NET easy changes nothing a
   consumer compiles against. Elsewhere it does. An npm package renamed to
   `@you/lib` changes every import specifier unless the consumer uses an alias
   (`"lib": "npm:@you/lib@…"`). Cargo needs `package = "you-lib"` in the
   dependency table. In Go the module path *is* the import path, and `replace`
   is the native stop-gap, not republishing. So "edge" may only make sense for
   .NET and npm-with-aliases, and the capability flag on the adapter is where
   that is declared.
5. **Cross-member edge closures.** When `You.Lib.Controls` needs a fixed
   `You.Core` from another member: one run, one version across both? Or per-member
   versions, with the dependency rewritten to the sibling's edge version?
   Refused in v1.
6. **Hook evaluation order.** Does `PackageId` already hold its `AssemblyName`
   default when `CustomAfterMicrosoftCommonTargets` imports, on the SDKs rig
   supports? Settle it with a spike over a project with and without an explicit
   `PackageId`, verified by nuspec. The double condition is the hedge until then.
7. **Diamond with the original id during the edge period** (the failure mode
   above). Could `doctor --edge`, pointed at a consumer's `project.assets.json`,
   report a graph holding both `Lib` and `You.Lib`? That needs a consumer path,
   which the stackspace only knows for members.
8. **Publishing tip-mode builds.** Should a scheduled `--upstream-tip` build be
   allowed to publish, given that its tests are the only evidence for a
   combination nobody pulled? Proposed: allowed, because the build job must run
   the configured tests first, as the canary does. But that is a policy choice
   for the user, and the workflow template could default it off.
9. **GitHub Packages and `GITHUB_TOKEN`.** If the feed is the publishing
   owner's own GitHub Packages, a workflow token with `packages: write` might
   replace the secret entirely, removing item 8. This is not verified, and the
   rules for linking packages to repositories need checking first.
10. **A default `maxSize`.** The 176 MB sample was found by accident. Should
    edge refuse above some default, or only report?

## What this changes elsewhere, when built

From the repository's checklist, so none of it is forgotten:

- `site/public/schemas/rig-stack.json`: the `edge` map. The scaffold template
  (`stackManifestTemplate`) gains a commented example. `knownKeys` needs nothing:
  `stack` is already the top-level `.rig.json` key, and `edge` sits inside it.
- `site/rig/stack.md`, `site/rig/verbs.md`, `site/rig/configuration.md`,
  `README.md`, `cmd/rig/README.md`: the verbs and the block.
- The `rig stack` group's help block and `rig ui` menu: the `edge` group.
- `~/.claude/skills/rigsmith-tools/SKILL.md`: when an agent should reach for
  edge rather than hand-built packing.
- A changeset per user-visible slice. Gated end-to-end coverage
  (`RIG_STACK_E2E=1`): pack a two-package member with a republished prefix and
  assert the nuspec ids, dependencies and assembly names.
