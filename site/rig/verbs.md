# Verbs

| Verb | What |
|------|------|
| `build` | Build the project |
| `test` | Run the tests |
| `run` / `dev` | Run the project |
| `format` | Format the code |
| `lint` | Lint the code |
| `typecheck` | Type-check the code |
| `verify` | `build` → `test` → `run` in sequence, then check the artifacts were built together ([see below](#verify)) |
| `coverage` | Run tests with coverage; `--min` gate; `--open` report (in-process cobertura→HTML for .NET) |
| `kill` | Kill dev processes by project/pattern/`--port` (config `kill.match` wins) |
| `add` / `uninstall` (`remove`, `rm`) / `outdated` (`od`) `[project]` / `upgrade` | Package management, native per ecosystem. On .NET, `outdated` reviews **every** project in the repo (respecting `exclude`), grouped by project, so a stale package in any in-repo dependency surfaces; name a `[project]` to scope it, like `run` |
| `deps` / `dependencies` | List dependencies with current + latest versions (`-u` updates only, `--vulnerable`, `--json`); whole-repo (per-project) on .NET |
| `install` (`restore`) / `ci` / `clean` / `rebuild` (`rb`) | Restore/clean/rebuild (rebuild scopes bin/obj removal on .NET) |
| `global` / `dlx` / `x` | Global tool install / one-shot tool run (`dnx`, `pnpm dlx`, …) |
| `publish` | `dotnet publish` with rid/output/self-contained from flags or `.rig.json publish.*` |
| `doctor` | Environment checklist (SDK pins via nearest `global.json`), headed by a `Setup` group: which rig is running and from where, the family on `PATH`, and what's registered in your shell ([see below](#doctor-setup)) |
| `cd` | Fuzzy project navigation (prints the dir; pair with a shell wrapper) |
| `watch <verb>` / `rig w r` | Watch modifier via the pre-parse pipeline (verb prefixes work too: `rig cove`) |
| `init` | Scaffold a `.rig.json` |
| `info` | Show what rig discovered (root, primary ecosystem, `.rig.json`, per-ecosystem dev commands, packages) — plus a `Warnings` section for anything wrong with the config |
| `explain [verb]` | Show what a verb resolves to — command, directory, environment, source — without running it ([see below](#explain)) |
| `config` | Manage `.rig.json` (`get` / `set` / `show` / `path` / `edit`) |
| `default` | Show or set the default project for `run`/`publish` (interactive picker) |
| `setup` | Install shell integration — `cd` wrapper + tab completion (zsh/bash/fish/PowerShell); `--aliases` also adds the short [verb aliases](./alias) |
| `alias` | Install short verb aliases — `rr` run, `rb` build, `rt` test, `rcd` cd, … ([details](./alias)) |
| `ui` | Interactive bubbletea menu over the dev verbs (capability-gated) |
| *custom* | Any `commands` entry in `.rig.json` becomes a subcommand — shell string, argv, or a cross-platform Tengo [`script`](/rig/configuration#commands) |
| *scripts* | In a Node repo, every `package.json` script becomes a verb |

## What rig itself has installed {#doctor-setup}

`rig doctor` opens with a `Setup` group about rig rather than your project:

```
Setup
  ✓ rig        1.4.2 · /Users/john/.local/bin/rig
  ✓ family     shiprig, changerig, clauderig · /Users/john/.local/bin
  ! shell      not installed in ~/.zshrc — `rig cd` can't change your directory
               and tab completion is off; run `rig setup zsh`
  ✓ aliases    4 of 11 installed: rr, rb, rt, rcd · ~/.zshrc
```

- **rig** — the running binary's version and path. Two copies on `PATH` is a
  warning: the first one answers `rig`, and it may not be the one you upgraded.
  Running a build by path (a source build, a `-dev` launcher) is normal, so it
  says what typing `rig` would run instead of faulting it.
- **family** — which sibling rigs are installed and where. Never a warning:
  each one's completion is loaded only when present, so installing one later
  needs a new shell, not a re-run of `rig setup`.
- **shell** — whether the `rig setup` block is in your startup file, and whether
  it's the one this rig would write. Absent or stale is a warning, because both
  of the things it provides fail silently. Having only a `--dev` block is called
  out by name: it's the state behind "I ran setup and `rig cd` still does
  nothing".
- **aliases** — which of `rr`, `rb`, `rt`, … are actually live, from the same
  block `rig alias install` writes.

The group runs everywhere, including a directory with no project — that is
exactly where you stand when asking why `rcd` does nothing. Nothing here is
touched by `rig doctor --fix`: writing to your startup file is `rig setup`'s
job, and it asks.
## Proving a result: `verify` {#verify}

`build`, `test` and `run` each answer their own question honestly, and the
answers can still be collectively wrong — because nothing checks that the
artifacts in play were produced *together*. A test binary two hours older than
the resources it loads passes its own build check and then crashes in code
nobody touched.

`rig verify` does two jobs:

```sh
rig verify                     # build → test → run, stopping at the first failure
rig verify --stale-only        # report disagreement, run nothing
rig verify --no-run            # build and test only
rig verify --run-timeout 30s   # how long "it starts" is given to prove itself
```

**Sequencing** makes "I checked" mean one thing instead of three. Each step is
the *same* command the standalone verb runs — `verify` reuses `build`/`test`/`run`
rather than reproducing how they resolve a target, so the two can never disagree
about what ran.

**Agreement** is the valuable half. Sequencing alone doesn't solve the problem,
it hides it, by rebuilding everything every time — fine for a Go service,
unusable where a build takes minutes to hours, which is exactly where stale
artifacts survive longest. So `verify` compares modification times instead:

- With **no configuration**, the generic check: is anything under the source
  tree newer than the newest build output? (Source means files a build actually
  consumes — editing a README doesn't count.) Output locations follow the
  ecosystem: `bin`/`dist` for Go, `dist`/`build`/`out`/`.next` for Node,
  per-project `bin/<config>/<tfm>` for .NET, `target/<profile>` for Cargo. Node
  also gets `node_modules` checked against its lockfile.
- With an **`artifacts` block** in `.rig.json`, the artifacts rig cannot infer —
  generated resources, multi-artifact builds, an `out/` tree beside the repo
  ([configuration](./configuration#artifacts)).

```
  ✓ build output  up to date with main.go
  ✗ browser       out/App.app/Contents/Resources/en.pak is 2h older than src/strings.grd (and 1 more file)
  ✗ unit-tests    out/unit_tests is 2h older than src/renderer.cc
```

Notes on the guarantees, because a check that exits zero while being wrong is
worse than no check:

- **Staleness is a failure, not a warning** — `verify` exits non-zero, so it can
  gate CI or a pre-push hook. A warning in a long log is what got missed.
- **Checks that could not run are reported as skipped**, never counted as
  passes; when nothing could be checked, the summary says so instead of printing
  a green line.
- **Nothing is rebuilt implicitly.** `--stale-only` reports and stops; the full
  `verify` rebuilds by construction.
- **A directory artifact is judged by its OLDEST file.** An app bundle whose
  newest file is minutes old can still hold a resource the build never
  refreshed — that bundle looks fresh and loads stale data.
- **The run step passes by staying alive.** A server or a desktop app never
  exits, so "still running after `--run-timeout`" (default 10s) is the answer to
  "does it start". An exit with a non-zero status before then is a failure. Set
  `verify.run: false` (or pass `--no-run`) where launching isn't wanted.
- **The run step is cleaned up completely.** It runs in its own process group,
  so the timeout takes down everything it started — `go run`'s compiled binary,
  a dev server's child processes — rather than reporting "it starts" and leaving
  one behind holding the port.

## Across the whole workspace (`--all`) {#all}

`rig build --all` (and `test`, `format`, `lint`, `typecheck`, `clean`) runs the
verb in every workspace package in dependency order; `--filter <glob>` narrows
the set. On a terminal you get the live dashboard — one row per package — and
off one (CI, piped, `--quiet`) it streams each package in turn.

**A package that doesn't define the verb is skipped, not failed.** A Node
package with no `typecheck` script has nothing to type-check, so it is reported
as skipped and the run carries on:

```
· @acme/auth (node)
– @acme/docs (node) — skipped: no "typecheck" script
✓ 34 ok   – 1 skipped
```

That is decided from the package's own manifest before anything runs, so it
never hides a real result: a script that runs and exits non-zero still fails the
run, still names the package, and still exits non-zero. When no package defines
the verb at all, the run says so rather than reporting success.

**A failure fails the run without ending it.** Every package still runs, and the
closing line counts what happened — so one CI log names every broken package
instead of only the first:

```
· @acme/auth (node)
✗ @acme/core (node): exit status 1
· @acme/web (node)
✓ 33 ok   ✗ 1 failed   – 1 skipped
```

The error names them too (`build failed in 2 packages: @acme/core, @acme/api`),
since a log long enough to need this is long enough that scrolling back for the
`✗` lines is the tedious part. The dashboard has always worked this way; the
plain path now matches it, so a run reports the same thing whether or not stdout
is a terminal.

## Passing flags to the underlying tool

rig owns a small set of flags per verb (`--all`, `--filter`, `--watch`, `-i`, plus
the global `--dry-run` / `--quiet` / `--no-env` / `--root`). Anything else is
rejected rather than guessed at, so a typo like `--dry-runn` is caught instead of
being handed to your package manager. To reach the tool underneath, put the flag
after `--`:

```sh
rig build -- --target=host        # → pnpm run build --target=host
rig test -- --reporter=dot        # → npm run test --reporter=dot
rig dlx prettier -- --write .     # → pnpm dlx prettier --write .
```

Everything after `--` is forwarded verbatim and never interpreted — it is not
read as a project name or a test-class query. Forget it and rig says so, naming
the flag and quoting your own command line back with the `--` already in place:

```
  ERROR

  Unknown flag: --target.

  rig build doesn't take --target. To pass it to the underlying command, put it after --:

      rig build -- --target=host
```
## Reading a verb before you run it {#explain}

`rig explain <verb>` prints what the verb resolves to and stops there:

```
$ rig explain markers
Verb
  name:    markers
  source:  custom command · /repo/.rig.json

Command
  runs:    grep -rho 'sheepish-[a-z-]*' src | sort -u
  shell:   portable · rig's in-process POSIX shell, same on every OS
  dir:     /repo

Environment
  API_TOKEN=abc      · .env / .env.local
  SHEEPISH_ROOT=src  · .rig.json env
  the rest is inherited from the current environment

  nothing ran — `rig markers` runs it
```

A custom command can be valid JSON wrapping a valid shell line that quietly does
the wrong thing — a character class missing `0-9`, a `grep -h` upstream of a
filter that matches on filenames — and it exits 0 while printing something
plausible. Nothing can validate that for you, but a resolved command you can
read takes seconds to check.

Bare `rig explain` lists every verb the repo resolves — the ecosystem's dev
loop, your `commands`, the `package.json` scripts and the script directories —
each with the command it becomes.

The resolution comes from the same code the verb runs through, not a second
implementation, so what you read is what executes. Verbs that decide part of
their command while running (`coverage`, `rebuild`, `publish`, `upgrade`,
`outdated`) are not guessed at: explain says so, and for the ones whose
`--dry-run` prints an exact command it points there, since that goes through the
real path. A few verbs have no such contract — `info` has no underlying command,
and `outdated` runs its scans without echoing them — so explain says only that
it cannot show a guaranteed answer. The same is true for an
argument that selects a project or a test filter — `rig test MyClass --dry-run`
rather than `rig explain test MyClass`.

## Ecosystem coverage

The same verb runs the native tool for your stack. A few combinations have no
native equivalent and degrade gracefully — with a clear message — rather than
failing:

- **.NET** has no separate `typecheck` (it would just be `build`).
- **Cargo** has no `dlx` one-shot runner, and `deps` falls back to the plain
  `cargo outdated` output rather than the rich table.
- **Node** `clean` runs only when the package defines a `clean` script.

The full per-ecosystem matrix lives in [`docs/ECOSYSTEM-MATRIX.md`](https://github.com/rigsmith/rigsmith/blob/main/docs/ECOSYSTEM-MATRIX.md).

## Git & worktree verbs

| Verb | What |
|------|------|
| `copy` / `cp` | Detached copy of the repo tree to a new folder; `--git` keeps `.git` history |
| `worktree` / `wt` | Parallel-dev sibling worktrees: `new` / `list` / `open` / `rm` (the menu/list show age, newest-first). Direct branch management is left to `git`/`gh` |
| `prune` / `tidy` | One sweep that reaps merged + gone-upstream **worktrees and branches** (worktrees first). `--worktrees` / `--branches` scope it; at the confirm prompt `w`/`b`/`a` retarget in place. `-n` previews, `-y` skips the prompt; off a terminal it refuses without `-y`. `--keep-gone` keeps gone-upstream items |

```sh
rig worktree new feat/x          # sibling checkout off mainline (prints the path)
rig worktree new feat/x --open   # …and open a review window for this run
rig worktree new feat/x --repo ~/Git/other   # act on another repo without cd'ing there
rig worktree list                # this repo's worktrees, newest-first (alias: ls)
rig copy ../scratch --git        # detached copy that keeps history
```

See [claudeRig — worktree discipline](/clauderig/commands#worktree-discipline)
for how the guard makes worktrees + PRs the default under Claude Code, and
[Configuration](./configuration#worktree) for the `worktree.autoOpen` /
`worktree.openCmd` keys.

## Fused forks: `stack` {#stack}

Some projects only make sense together — a library, a second library, and the
thing that uses both — and when you maintain forks of all three, iterating means
publishing a package to see a change land. A stackspace fuses those repos
into **one history**, each under its own directory, so a change spans them in a
single commit and the build compiles against source rather than packages.

Nothing downstream learns about it. Each repo keeps its own remote, and work
leaves one project at a time: `propose` produces an ordinary pull request to a fork
you contribute to, and `push` fast-forwards a repo you own with its history.

| Verb | What |
|------|------|
| `stack setup [seed]` | Clone an explicit seed into an empty directory and configure its manifest home; without a seed, preserve remotes and home configuration. Set up the stackspace: install the fusion engine, import the members, write the build overlay, print the status. The one command a new clone needs; safe to run again |
| `stack init` | Write the manifest, or import the repos it names into this history (and a generated `README.md`, unless one is there that rig does not own); a private upstream is reached with your `gh` login, and an import whose fetch brought nothing back is refused rather than recorded |
| `stack add [upstream]` | Add a repo to this stackspace and import it; asks when not given |
| `stack rm <repo>` | Remove a repo — manifest entry, directory and overlay redirects; refuses while it holds unsent or uncommitted work, or files git ignores (`--force` overrides); `--keep-tree` keeps the directory, and needs a clean one even with `--force` |
| `stack seed [dir] [--publish <seed>]` | With --publish, publish root-only commits to a manifest home; with no arguments, retry the configured home. Unsent member work is refused unless --force overrides the reconstruction check. A directory and --publish cannot be combined. Local publication requires a bare home. With a directory, export the root files as a small repo (conventionally `rigstack-<name>`, suggested outside enclosing repositories); `stack init` on a clone of it rebuilds every member at its recorded cursor, or from the fork branch it was last proposed to while that branch still exists; refuses while a member holds unsent commits (`--force`) |
| `stack home [pointer-file]` | Read a member’s single-reference .rig-stack pointer (default .rig-stack) |
| `stack canary` | Test upstream tips; required `--seed`, `--allowed-hosts`, `--members`, `--mode batch` or `--mode separate`, `--isolate`, `--build`, `--test`, `--report`; `--publish` validates tracked upstream ancestry and reconciles reported build outcomes into cursor PRs or failure issues on a fresh trusted runner; `-n` previews |
| `stack deps` | Read `--manifest <path>` or `--seed <ref>` (exclusive); `--allowed-hosts <hosts>` restricts seed hosts; check topic PR dependencies against upstream merge states and manifest cursors; `--publish` posts statuses only in `GITHUB_REPOSITORY` |
| `stack released <member> <sibling> --package <id>` | Verify NuGet release ancestry and package delivery; preview pin and guard edits, or use `--apply` on a clean tree with interactive confirmation; `--report` writes read-only JSON, `--seed` reads a trusted manifest home and default branches, and `--publish` independently verifies and proposes a member PR. `-n` previews only |
| `stack edge pack <member>` | Reconstruct the committed manifest (or `--manifest` / `--seed`) and fork trackBranch, then rename, version, pack and verify .NET packages; `--number`, `--upstream-tip`, `--here`, `-o`, `-n`; tip conflicts remove stale top-level package files and write an inconclusive report (`-n` prints only), preserving unrelated files and directories; writes packages and provenance, never pushes a feed |
| `stack edge status [member]` | Read carried-item release evidence, integration freshness, newest edge version and retirement verdict; `--json`, `--package`, `--version`, public `--feed` |
| `stack status` | Each repo's cursor against its upstream, whether it holds work that has not left — all of which `propose` would send — and, listed under it, each `stack-pr-*` topic in flight with the fork branch its pull request is on, `not proposed yet`, whether the branch has moved since, and whether a pull left it behind; `--releases` opts into NuGet release annotations under the shared short deadline |
| `stack pull [repo]` | Merge new upstream commits into a repo's directory (all repos by default); private upstreams through your `gh` login, as with `init` |
| `stack propose [repo] [new-branch]` | Put that repo's changes on your fork as a PR-ready branch — **all** of them, the prefix's whole divergence from upstream; `--from <branch>` sends only what a topic branch of the stackspace adds (a bare name resolves to the conventional `stack-pr-<name>`), so a second fix can be its own pull request (needs `trackBranch`, which it then keeps current; the topic must be an ancestor of HEAD); `--track-only` refreshes only trackBranch from HEAD after a pull, requiring a clean prefix, a current cursor and every recorded local topic merged into HEAD; it opens no PR or proposal record, cannot combine with `--from`, and pushes nothing when the exact tree and single upstream parent already match; `--dry-run` shows the commit and branch that would go and touches no remote; both report any package the branch references that this stackspace provides from source rather than a feed, since a plain checkout of the branch cannot restore those |
| `stack push [repo]` | Fast-forward a repo you own; compare upstream trees, preserve individual new commits, and refuse ambiguous or non-descendant history; inferred when only one is yours; `--dry-run` shows the target, branch and commits that would go and touches no remote |
| `stack ci-guard <project>` | Guard one .NET build: new member warnings fail; sibling warnings, including SDK diagnostics, stay visible, counted and non-fatal unless `--strict`. Unattributed NU/MSB warnings fail closed. Tests run separately; see [CI warning guard](./stack#ci-warning-guard) for exemptions and `--no-build` reuse |
| `stack wire` | Write the build overlay so members resolve each other from source |
| `stack pack [repo]` | Build a member's publishable packages from inside the stackspace, where the build overlay makes cross-member references resolve from source (every member with no argument); `--out` chooses the directory, default `dist/`; refuses while the overlay is missing, since without it the packages are the ones a bare checkout would produce |
| `stack doctor` | Check the engine and manifest; `--fix` installs what's missing |

`rig stack released <member> <sibling> --package Acme.Lib` checks NuGet release
commit ancestry. `--version` selects a release, `--feed` selects a flat-container
feed, `--warning` explicitly attributes warning IDs, and `--apply` edits a clean
working tree after interactive confirmation. `-n` previews only. See [stackspaces](./stack).

With `publishPrefix` or `publishesAs`, select the original public `--package` id.
Released renames the consumer's republished reference and bumps its version,
using that referenced id for guard and pin checks. It refuses plans that would
leave other republished ids of the sibling behind.

For transitive siblings, `stack released --via-package Acme.Control
--via-version 4.0.0` selects a direct package whose nuspec must deliver the
verified sibling minimum in every target-framework group. `rig stack status
--releases` opts into bounded release annotations; default status skips discovery.

`stack released --report <file>` writes read-only JSON; `not yet` exits 0.
Use `--seed acme/rigstack-app@main` for a trusted manifest home and member default
branches. In a fresh job, `stack released [<member> <sibling>] --publish <file>`
requires `--seed` and the same trusted `--package`, `--feed`, `--version`,
`--via-package`, `--via-version` and `--warning` selections. It re-verifies all
release/delivery evidence and edits, then opens or updates a stable member PR.
`--report`, `--publish` and `--apply` are mutually exclusive. Neither report nor
publish edits the local tree; `--apply` still requires clean-tree interactive
confirmation. See [scheduled return to packages](stack.md#scheduled-return-to-packages)
and `examples/github-workflows/stack-released.yml`. NuGet only; npm and deeper
transitive chains remain undone.


On .NET, `wire` source constants and target-time properties include the static
PackageReference closure of each swapped sibling, propagated through evaluated
ProjectReferences before compile, including incremental builds. Unreachable
siblings stay absent; deeper package conditions remain conservative. Doctor
follows literal ProjectReference paths in its static preview. An empty SDK
reference candidate set with existing references fails with `RIGSTACK001` so an
SDK change cannot silently remove guarded code or tests. See
[Source signals](stack.md#source-signals-from-the-net-overlay) for limitations.
Hand-owned imports that continue the parent search are preserved; historical
rig-owned ungated imports get a legacy advisory, with a manual remedy for customized blocks that wire cannot safely upgrade.

Dry runs: `pull -n` reports each current cursor and upstream tip (including
“nothing to pull”), without fetching objects, starting josh, or changing the
stackspace. It retains the normal dirty-tree check; `--repin` resolves the pin
again for the preview. Commit counts are omitted because upstream objects need
not be present locally. Merge conflicts and replacement safety checks require a
real pull. `wire -n` reports current wiring without writing; `doctor --fix -n`
reports the engine installation it would attempt. `init`, `add`, `rm`, `setup`,
`ci`, `ci-guard`, `deps` (including `--publish`),
and offline seed directory exports refuse `--dry-run` before making changes.
Seed publication supports a dry-run preview without cloning or pushing. `home`, `status` and plain `doctor`
are read-only; `propose`, `push`, and `pack` retain their existing previews.

```sh
rig stack init                       # writes rig.stack.jsonc to fill in
rig stack init                       # again: imports each repo it names
rig stack setup                      # a fresh clone: engine, members, overlay, status
rig stack status                     # who has moved upstream, and where each topic's PR is
rig stack pull pty-core              # take that movement
rig stack propose pty-core read-timeout -m "Fix the read timeout"  # → stack/read-timeout
```

Importing and pulling are done by [josh](https://josh-project.dev), the git
history-filtering proxy; `propose` uses no engine at all, since the directory's
tree is already what upstream wants. rig fetches a verified `josh-proxy` on
first use where one is published for your platform and pinned version, and
builds it from source otherwise.

The default stack manifest home is a small seed repo containing the manifest,
README and root overlays. Publish with `rig stack seed --publish acme/rigstack-app@main`;
rebuild in an empty directory with `rig stack setup acme/rigstack-app@main`.
Pull and propose publish root-only updates without sending fused history.
Use `rig stack seed` to retry a failed publication; local work is retained.
A member's `.rig-stack` contains that same seed reference; `rig stack home` reads it.

Only setup with an explicit seed opts into a manifest home; no-argument setup
leaves origin and home configuration unchanged. After a manual clone, opt in
with `rig stack seed --publish <seed>`. A publication failure after pull
returns non-zero while retaining the successful local pull and cursor.

`stack status` also displays the dependency chain checked by `stack deps`.
See [topic dependencies](./stack#stack-topic-dependencies) for recording edges,
publication permissions, and the opt-in scheduled refresh workflow.

**[Edge builds](./stack#edge-builds)** prepare carried fixes under your own ids; `--here` output and number 0 are unpublishable. `edge ci` and `edge init` are not built yet.

**[Stackspaces](./stack)** is the full guide: setting one up, wiring the
build so the consumer compiles against source, sending and updating pull
requests, and the rules that will otherwise catch you out. Manifest keys are in
[Configuration](./configuration#stack).

The .NET `stack wire` props overlay exposes early `RigStackspace`,
`RigStackspaceRoot`, and stack-wide `RigStackAvailable_<id>` availability.
These are usable in project-body conditions; availability does not imply a swap.
The targets overlay exposes `RigStackspace`, `RigStackspaceRoot`,
per-swapped-package `RigStackSource_Acme_Core` properties and `STACK_ACME_CORE`
compile constants, plus `RIG_STACK`. Per-package properties are available inside
targets after `RigStackSourceSignals`, not in project evaluation conditions.
Global `-p:UseStackSources=false` disables all signals and swaps; a value first
set in the project body cannot undo early evaluation. Member imports check the
nearest parent props or targets file for rig's generated-overlay or member-import
marker, or the literal `<RigStackspace>true</RigStackspace>` declaration. Retain
that declaration when taking ownership of an overlay; an ancestor stack manifest
alone does not trigger the import. Re-running `wire` upgrades rig-owned imports;
`stack doctor`
previews the signals by project. Optional props issues (missing, stale, hand-owned
root props or member shadowing) are advisory and do not block packing; targets
issues remain blocking. See [the stack guide](stack.md) for naming and timing.

### Stackspace member CI

Use `seed: acme/rigstack-app@main` (CLI `--seed acme/rigstack-app@main`)
to read the committed manifest from its home, or `manifest` / `--manifest`
to read it from checkout HEAD. Exactly one is required. Seed references use the
same owner/repo@ref, URL, scp and local-path syntax as `stack setup`.
CI never infers this input from a member's `.rig-stack` pointer: workflows
explicitly choose their manifest source. Origin matching still selects the member.
The seed host must be in `allowed-hosts`; local paths have no host.
Private seeds use configured Git credentials and may be unavailable to fork PRs;
fetch failures name the seed and exit at setup (10). Configure credentials outside
the action without embedding tokens in workflow inputs.
The summary and provenance artifact record the sanitized seed and resolved commit.
Topic dependency reporting also reads the seed, using
`rig stack deps --seed <ref> --allowed-hosts github.com --publish`.

`rig stack canary` provides scheduled cursor updates through separate build
and publication jobs; see [Stackspaces](./stack#scheduled-upstream-canary).

`rig stack ci --manifest stack/rig.stack.jsonc --member app --test "rig test"`
builds the committed checkout against sibling sources in a temporary stackspace.
Keep this job alongside the plain-clone matrix. The published composite is
`rigsmith/stack-action@fcae919e5eb7036b81b94ad2b5977d5a77648128 # v1.0.0` (full SHA recommended; `@v1` moves).
See [the guide](https://github.com/rigsmith/rigsmith/blob/main/docs/guides/testing-a-member-pr-in-its-stackspace.md) for a copyable workflow and other CI usage.

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

Stack CI defaults to `--allowed-hosts github.com`; explicitly list other trusted
fork/upstream hosts with `--allowed-hosts` (the action input is `allowed-hosts`).
It rejects custom josh versions, LFS members and fused push destinations. The
composite verifies rig release checksums, supports a workflow-pinned `rig-sha256`,
reuses cursor-keyed member histories and reports consumed cache hits and member
commit/filter provenance. Use `pull_request` with `permissions: {contents: read}`,
no secrets and checkout `persist-credentials: false`. Pin external action uses
by full commit SHA, including `rigsmith/stack-action@fcae919e5eb7036b81b94ad2b5977d5a77648128 # v1.0.0`.

Set action `cache-history: 'false'` (CLI `--cache-history=false`) for builds that
should not trust restored filtered history. Cached bundles are not verified
against upstream cursors; provenance records `source` and actual `filteredCommit`
so a cached import is explicit. See the action README for the cache scope trust
boundary and reproducibility limits.

## Prefix matching

Verbs prefix-match, so `rig cove` runs `coverage` and `rig w r` is `watch run`.
The watch modifier rides the same pre-parse pipeline, so it composes with any
verb.

## Discovered verbs

In a Node repo, every `package.json` script becomes a verb. In a Go repo, any
`main` package under `scripts/` or `cmd/` is surfaced as a bare `rig <name>`
verb — these are exact-match only (excluded from prefix-matching) and never
shadow a built-in. `rig run` offers those `cmd/*` binaries directly instead of
falling through to a doomed `go run .`.

## How a project name resolves {#project-resolution}

`rig run <name>`, `rig build <name>`, `rig cd <name>` and a configured
[`defaultProject`](./configuration) all resolve through the same rules, in this
order:

1. **Best tier wins, and it stops there** — exact, then prefix, then substring
   (`rig cd` also accepts subsequences). One exact hit is never ambiguous just
   because looser matches exist: `Tweed.App` is that project, not it plus
   `Tweed.App.Tests`. Names match in full, slash-short (`@scope/pkg` → `pkg`),
   and dot-short (`Acme.Desktop` → `Desktop`) form.
2. **Ties break by proximity to your working directory** — the project you're
   standing in wins, then the nearest common ancestor.
3. **Anything still tied is ambiguous, and rig says so** — a picker on a
   terminal; off one, the candidate paths, what each copy is (a
   [nested worktree](./configuration#nested-worktrees), and whether `rig prune`
   would remove it), and the exact `exclude` line to paste into `.rig.json`.

A bare `rig run` obeys **the same rules** as `rig run <name>`. A
`defaultProject` matching two checkouts fails exactly as the explicit command
would rather than launching whichever was found first, and when it resolves
cleanly rig echoes what it picked:

```
· defaultProject Tweed.App → ui/src/Tweed.App
```

## Opening the picker (`-i` / `--interactive`)

At a stackspace root where targets live only in subdirectories, a bare `rig run`
(or `build`/`test`/`format`/`lint`/`typecheck`/`clean`/`rebuild`) opens a picker —
no flag needed. `run` lists the runnable packages **and** the repo's surfaced
scripts; the other verbs list packages only. When one obvious target *would* run
directly, pass `-i`/`--interactive` to force the picker anyway:

```sh
rig run                  # picker only when there's no single target
rig run -i               # always pick, even with one obvious target
rig build --interactive  # same, for the --all-capable verbs
rig rebuild -i           # rebuild a chosen package, or "All packages"
```

`rebuild` carries its own picker (it sequences clean → build, so it has no single
command to ride the shared one): `rig rebuild <project>` scopes the rebuild to one
package, and the picker's **All packages** rebuilds each in dependency order.

Off a TTY there's no picker, so `-i` reports a helpful error and points you at
`rig <verb> <project>`.

## Picker controls (exclude / include)

When `rig run` (or the `rig ui` project menu) lists several projects, you can
curate the set live:

- **`x`** — exclude the highlighted project from future discovery. In a crowded
  directory (≥3 siblings) it asks whether to hide just that project or the whole
  `<dir>/*`.
- **`i`** — show/hide excluded projects; while shown they appear struck-through,
  and pressing `i` on one re-includes it.

Exclusions are written to `.rig.json`'s [`exclude`](./configuration) globs and
match against the project's full name, short name, and repo-relative path.
