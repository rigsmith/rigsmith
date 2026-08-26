# Stack workspaces

Some projects only make sense together — a library, a second library, and the
thing that uses both. When you maintain forks of all three, iterating means
publishing a package to see a change land, and a change that spans them cannot
be one commit.

A **stack workspace** fuses those repos into one git history, each under its own
directory. A change spans them in a single commit, the build compiles against
source rather than packages, and each project still leaves as an ordinary pull
request against its own upstream. Upstream never learns the workspace exists.

## The two halves

Two independent problems, two independent solutions. You need both, and they
know nothing about each other.

**The git half.** Each upstream repo's history is imported into one workspace
repo, rewritten to live under a prefix directory. That is
[josh](https://josh-project.dev) doing reversible history filtering; rig drives
it as a throwaway localhost process and does everything else with plain git.
Because it is one repo, one commit can touch every project in it.

**The build half.** If the projects depend on each other through a package
registry, your edits do not reach the consumer without a publish cycle. A
workspace-level build file redirects those package references at the sources
next door — see [wiring the build](#build) below. No upstream-owned file is
modified.

## Setting one up

### 1. The workspace is an ordinary git repo

It needs no remote and never gets one. Nothing is pushed *from* it except
through `send`, which pushes to your forks.

```sh
mkdir ~/src/my-stack && cd ~/src/my-stack
git init -b main
```

### 2. Scaffold the manifest

The first `init` writes a commented skeleton and stops — it cannot guess your
repos. Fill it in, and the second `init` imports them.

```sh
rig stack init          # writes rig.stack.jsonc
```

```jsonc
{
  "$schema": "https://rigsmith.dev/schemas/rig-stack.json",
  "repos": {
    "pty-core": {
      "upstream": "github.com/acme/pty-core",   // where PRs go
      "fork":     "github.com/you/pty-core",    // where `send` pushes
      "upstreamBranch": "main"                  // branch of upstream to follow
    },
    "term-core": {
      "upstream": "github.com/acme/term-core",
      "fork":     "github.com/you/term-core",
      "upstreamBranch": "main"
    },
    "term-control": {
      "upstream": "github.com/acme/term-control",
      "fork":     "github.com/you/term-control",
      "upstreamBranch": "main"
    }
  }
}
```

The key is the directory the project will live under. Repo specs are
`host/owner/name` — no scheme, no `.git` — because the same string has to serve
as a URL, an engine path, and a label.

`upstreamBranch` is the branch of **upstream** this directory follows: what
`pull` takes, and what `send` roots its commit on. It is *not* the branch `send`
creates — you name that one per change. Full key reference in
[Configuration](./configuration#stack).

### 3. Import

```sh
rig stack init

# pty-core: imported upstream a1b2c3d4
# term-core: imported upstream e5f6a7b8
# term-control: imported upstream 9c0d1e2f
```

Each repo's history is fetched through its prefix filter and merged in. The
first run also acquires the josh engine — a verified binary for your platform,
seconds rather than the multi-minute Rust build it would otherwise be.

You now have one history, one directory per project, and a `lastSync` block in
the manifest recording the upstream commit each was taken from. Those cursors
are committed alongside the import, which is what makes a repeated `pull` a
no-op rather than a surprise.

### 4. Wire the build {#build}

This part has nothing to do with git, and what it looks like depends on your
ecosystem. The goal is the same everywhere: make the consumer compile against
the sources next door instead of a published package.

On .NET, a `Directory.Build.targets` at the workspace root is picked up by
MSBuild's walk-up from every project underneath — so long as none of the
imported repos carries a root targets file of its own, which is the usual case:

```xml
<Project>
  <ItemGroup Condition="'$(UseWorkspaceProjects)' != 'false'">
    <ProjectReference Include="$(MSBuildThisFileDirectory)pty-core/src/Pty.Core/Pty.Core.csproj"
                      Condition="@(PackageReference->AnyHaveMetadataValue('Identity', 'Pty.Core'))" />
    <PackageReference Remove="Pty.Core" />
  </ItemGroup>
</Project>
```

Ordering matters: the `Condition` reads the package references *before* `Remove`
deletes them. Conditioning on "did this project actually reference the package"
stops it wiring anything circular.

Keep an escape hatch like the `UseWorkspaceProjects` property above, so you can
still build the way upstream CI will:

```sh
dotnet build -p:UseWorkspaceProjects=false
```

Other ecosystems have their own version of this — a workspace `paths` mapping,
a `go.work` file, a linked dependency. Nothing in `rig stack` depends on which
you choose.

## The daily loop

There is nothing special about it. It is one repo, so you work in it like one
repo.

```sh
# change an API and its consumer, together
$EDITOR pty-core/src/Pty.Core/PtyConnection.cs
$EDITOR term-control/src/Control/TerminalControl.cs

rig build          # compiles against source, no publish cycle
git commit -am "fix the read timeout and the control that hit it"
```

One commit, spanning two projects, and the build proved the change works end to
end before you committed it. That is the whole point of the exercise.

## Sending a change upstream

One `send` per project. Each produces a branch on **your fork** holding a single
commit whose diff is exactly what you changed in that directory, rooted on the
current upstream tip.

```sh
rig stack send <repo> <new-branch>

rig stack send pty-core fix/read-timeout -m "Fix the read timeout"
rig stack send term-control fix/read-timeout -m "Handle the new timeout"

# sent pty-core to you/pty-core:fix/read-timeout
#   — open the PR against acme/pty-core
```

The branch holds that project's files at their real un-prefixed paths —
`src/Pty.Core/…`, not `pty-core/src/…` — with no sign the repo is fused with
anything, and none of the workspace's own history for a maintainer to read
around. Open the PR from your fork as usual.

`<new-branch>` is a branch you are creating on *your fork*, named per change.
Nothing reads it from the manifest, because it is a property of the change
rather than of the project — which is also why the `rig ui` flow asks for it.

Sending again to the same branch **updates** it, so you can act on review
feedback: commit in the workspace, re-send, and the pull request moves. The
branch is replaced under a lease, so the push still fails if someone else moved
it in the meantime.

::: warning `send` refuses a stale cursor
Your workspace tree is a snapshot taken at the last `pull`. If upstream has
moved on since, committing that tree onto the newer tip would present every
commit that landed in between as though your branch had **reverted** it. `send`
stops rather than build such a branch — run `rig stack pull <repo>` and send
again.
:::

## Taking upstream's changes

```sh
rig stack status              # who has moved, per repo
rig stack pull pty-core       # take one
rig stack pull                # take all of them
```

A pull merges the new upstream commits into that repo's directory, so a conflict
is scoped to the project that caused it rather than landing across the whole
workspace. The cursor only advances once the merge is committed.

## The verbs

| Verb | What it does |
|---|---|
| `stack init` | Scaffold the manifest, or import the repos it names that are not imported yet |
| `stack status` | Each repo's cursor against its upstream branch tip |
| `stack pull [repo]` | Merge new upstream commits into a repo's directory (all repos by default) |
| `stack send <repo> <new-branch>` | Put that repo's changes on your fork as a PR-ready branch |
| `stack doctor` | Check the engine and manifest; `--fix` installs what is missing |

All of it is in [`rig ui`](./verbs) too, under **▸ Stack** — `send` there picks
the repo from the manifest and prompts for a branch name. Tab completion offers
your repo names for the verbs that take one.

## The engine

Importing and pulling are done by [josh](https://josh-project.dev), the git
history-filtering proxy: `init` and `pull` drive its reversible `:prefix=`
filter to move commits between an upstream repo and its directory here.

`send` uses no engine at all. The directory's tree is already what upstream
wants, prefix absent inside it, so the export is plain `git commit-tree` onto
the upstream tip.

rig owns the binary so you do not have to. `rig stack doctor --fix` fetches a
verified `josh-proxy` for your platform — built and published by
[rigsmith/josh-binaries](https://github.com/rigsmith/josh-binaries), since
upstream ships no releases — falling back to building it from source where none
exists. Nothing runs in the background: the engine starts per operation and
stops after. Pin a version per workspace with the manifest's
[`josh` key](./configuration#stack).

## Things worth knowing before they bite

- **A clean worktree is required** for `init`, `pull`, and `send`. An import
  amends its merge commit and stages everything, so an unrelated edit sitting in
  the tree would be swallowed into it. The one exception is the manifest itself
  on first import, since filling it in and re-running is the documented flow.
- **The workspace root is the git top level**, so the verbs work from anywhere
  inside it — including from within one of the imported projects, which carries
  its own package manifest and would otherwise look like the root.
- **The workspace is disposable.** Every commit that matters reaches upstream
  through a fork branch; the fused history is a working convenience, not an
  archive. If it ever gets tangled, delete it and `init` again.
- **Do not give the workspace a remote** and push it somewhere. It contains
  several rewritten upstream histories fused together, which is meaningful to
  you and to nobody else.
