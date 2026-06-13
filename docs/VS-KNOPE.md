# rigsmith vs. knope

How rigsmith's tools compare to [knope](https://knope.tech) — a single-binary,
configurable release-and-workflow automation tool. Of all the tools we've
compared against, knope is the closest cousin: it's a **single static binary**
with **no runtime dependency** (Rust, where relrig is Go), and it's one of the
few release tools that natively speaks **changesets**. So the interesting
differences aren't "binary vs. Node script" (as with
[release-it](VS-RELEASE-IT.md)) — they're about the *release model* and how far
each tool reaches into general task automation.

knope is an excellent, thoughtfully designed tool, and for many projects it's the
better choice — this comparison is about *fit*, not quality. It was a genuine
candidate before rigsmith existed; the gaps below are gaps for *our* shape of
project (a .NET-heavy polyglot monorepo), not flaws in knope.

> Companion docs: [VS-RELEASE-IT.md](VS-RELEASE-IT.md),
> [RELEASE-ORCHESTRATOR.md](RELEASE-ORCHESTRATOR.md) (the `relrig release`
> pipeline), and [FEATURE-PARITY.md](FEATURE-PARITY.md).

## What's being compared

knope is one tool that does two things: it **automates releases** (version bump
→ changelog → tag → forge release) and it runs **user-defined workflows** of
arbitrary steps (select an issue, run a command, open a PR). rigsmith splits
those concerns across the family:

- **`relrig` / `changerig`** — the release engine. This is the head-to-head with
  knope's release half.
- **`rig`** — the convention-first dev launcher (run/build/test/format/doctor).
  This covers the everyday-dev-loop "tedious tasks" that knope's `Command`
  workflows can also be wired up to do, but from the opposite direction (see
  [The workflow / task-runner axis](#the-workflow--task-runner-axis)).

## TL;DR

- **knope** is a single Rust binary that determines each package's version from
  **conventional commits *and* changesets combined**, edits a broad set of
  **versioned file formats**, generates a changelog, and creates a
  GitHub/GitLab/Gitea release. Beyond releases it's a general **workflow runner**:
  `knope.toml` defines named workflows as ordered `steps` (`PrepareRelease`,
  `Release`, `Command`, `CreatePullRequest`, `SelectGitHubIssue`, …), so it also
  automates issue selection, branch juggling, and PR creation.
- **relrig** is a single Go binary that determines versions from **explicit
  changeset files only**, cascades the bump through the **dependency graph**,
  applies **linked/fixed/lockstep** grouping, stamps every ecosystem's manifest,
  aggregates the changelog, and **publishes to the native registries** — all as
  one planned multi-package run. Its `release.jsonc` pipeline is a deliberately
  *thin* sequencer, not a general task runner; the dev-loop tasks live in the
  separate `rig` launcher.

Both reject the Node-runtime tax and both embrace changesets. They diverge on
two things: **how the version is decided** (changesets-only vs. commits +
changesets) and **how much the tool tries to be** (a focused release engine plus
a separate dev launcher, vs. one configurable release-and-workflow runner).

## At a glance

| | **relrig** (+ `rig`) | **knope** |
|---|---|---|
| Runtime | Single static Go binary, zero runtime deps | Single static Rust binary, zero runtime deps |
| Install | `curl \| sh` / Homebrew / Scoop | Prebuilt binaries / `cargo install knope` / `knope-dev/action` |
| Version source | **Changeset files only** (captured intent) | **Conventional commits + changesets**, combined |
| Versioned formats | .NET, Node, Go, Rust via plugin adapters (+ generic `regex`) | Cargo, npm, pyproject, pom.xml, go.mod, pubspec, deno, gleam, tauri (+ regex) — **no native .NET** |
| Unit of release | Whole monorepo — one planned multi-package run | Per-package `PrepareRelease`, run independently |
| Dependency cascade | Built in — dependents auto-bumped; linked/fixed/lockstep grouping | Not auto-cascaded; can edit a `dependency` field within a file |
| Registry publish | Built in — runs npm/NuGet/cargo/Go publish per ecosystem | Via a `Command` step you add; native step does tag + forge release |
| Forge releases | GitHub only (via `gh`) | GitHub, GitLab, **and Gitea** |
| Issue trackers | — | GitHub / Gitea / Jira issue selection steps |
| Workflow model | Thin `release.jsonc` sequencer over engine steps | General `knope.toml` workflows: any ordered `steps` |
| Dev-loop tasks | The separate `rig` launcher (run/build/test/format/doctor) | Arbitrary `Command` steps in a workflow |
| Extensibility | Subprocess + versioned-JSON plugin contract (adapters + changelog, any language) | `Command` steps + regex versioned files; not plugin-extensible |
| Interactive vs CI | Auto by TTY; bubbletea TUI; `--yes` for CI | Workflows run the same locally or in CI; `--generate` for defaults |

## The core difference: changesets-only vs. commits + changesets

This is the decision that drives the rest.

**knope** combines two signals when it computes a release. It parses
**Conventional Commits** since the last tag *and* reads any **changeset** files
in `.changeset/`, then merges them to pick each package's next semantic version
and assemble the changelog. You can lean on commit hygiene, on deliberate
changeset prose, or both at once — knope adopted changesets *in addition to*
commit parsing, so it sits on both sides of the
[VS-RELEASE-IT](VS-RELEASE-IT.md) fence at the same time.

**relrig** uses changesets **exclusively** (it's the Go successor to
net-changesets, itself a port of the @changesets workflow). Every meaningful
change ships a changeset file (`changerig add` / `relrig add`) declaring the
packages it touches, the bump level, and the changelog prose. At release time the
planner reads the accumulated set, computes per-package bumps, **cascades** them
to dependents, applies **linked/fixed/lockstep** grouping, stamps every
ecosystem's manifest, and aggregates the changelog.

Trade-offs:

- **Flexibility vs. one source of truth.** knope lets a team mix commit-derived
  and changeset-derived bumps; relrig insists the intent lives in changesets, so
  there's exactly one place "what releases and how much" is recorded — no
  dependence on commit discipline, no ambiguity between two signals.
- **Multi-package planning.** relrig holds the whole pending set *and* the
  dependency graph, so one run produces a coherent cross-package plan with a
  cascade and grouping. knope's `PrepareRelease` runs **per package
  independently**; there's no automatic cross-package bump propagation (it can
  rewrite a `dependency` field inside a versioned file, but not plan a cascade),
  and `$version` interpolation is unsupported in multi-package setups.

## Where knope is ahead

- **Two version sources.** If your team already lives on Conventional Commits,
  knope gives you releases with *zero* extra files, and lets you sprinkle in
  changesets only where a commit can't capture the nuance. relrig has no
  commit-inference path at all.
- **More forges.** GitHub, GitLab, **and Gitea** releases are first-class, plus
  issue-tracker integration (GitHub/Gitea/Jira). relrig's forge step is
  GitHub-only today (via `gh`) and has no issue-tracker step.
- **A general workflow runner.** `knope.toml` workflows are arbitrary ordered
  steps — select an issue, switch branches, run a command, open a PR, cut a
  release — so one tool scripts a lot of the day-to-day. relrig deliberately
  keeps its pipeline thin (see below).
- **Breadth of versioned formats out of the box.** Python (`pyproject.toml`),
  Java (`pom.xml`), Dart (`pubspec.yaml`), Deno, Gleam, and Tauri are built in;
  relrig ships .NET/Node/Go/Rust adapters and reaches the rest through its
  `regex` adapter or a custom plugin.

## Where rigsmith is ahead

The first two are simply why rigsmith was built rather than adopting knope —
knope was close enough on the binary-and-changesets axis to be a real candidate,
and these are differences in target, not signs that one tool is better made than
the other.

- **.NET, as a first-class citizen.** knope has **no native .NET/C# support** —
  no `.csproj`, `.nuspec`, `Directory.Build.props`, or `AssemblyInfo` — so .NET
  would run through its generic `regex` matcher with a hand-rolled NuGet publish.
  That's a perfectly reasonable scope choice for knope; it just happens that .NET
  is central to our repos, and relrig ships a real .NET adapter that treats a .NET
  project exactly like the others. This was the single biggest reason we built
  rather than adopted.
- **Polyglot release, not just polyglot version strings.** knope edits version
  numbers across many file formats, but by design it **does not build or
  publish** — its release step writes files and cuts a forge release; pushing to
  npm/NuGet/cargo is a `Command` step you wire up per ecosystem (a clean,
  composable model). relrig instead detects each ecosystem and runs its **native
  publish** (npm, NuGet, cargo, Go) as part of one planned run. "Polyglot" in
  rigsmith means the whole release, manifest-to-registry, across languages — a
  deliberately heavier, less composable trade than knope's.
- **A real dependency cascade.** Dependents are auto-bumped when a dependency
  releases, with linked/fixed/lockstep grouping and an aggregated multi-package
  changelog — core behavior, not something you assemble. knope releases each
  package independently.
- **Built-in registry publishing.** `relrig publish` runs the native publish for
  each ecosystem (npm, NuGet, cargo, Go), idempotently and confirm-gated on a
  TTY. knope's native release step creates the tag and forge release; pushing to
  a package registry is a `Command` step you wire up yourself.
- **Language-neutral plugin contract.** A subprocess + versioned-JSON protocol
  lets an ecosystem adapter or changelog generator be written in *any* language
  (the built-ins dogfood the same contract). knope is extended through `Command`
  steps and regex files, not a plugin API.
- **A focused tool per job.** relrig stays a release engine; the everyday dev
  loop is the separate `rig` launcher (`run`/`build`/`test`/`format`/`doctor`).
  knope folds release and general task-running into one configurable surface —
  genuinely powerful, and many teams prefer one tool; rigsmith just makes the
  opposite bet, keeping the release sequencer thin and the dev loop separate.
- **Deliberate changelog prose.** Notes are authored in changesets at change
  time; relrig never reconstructs a changelog from commit subjects.

## The workflow / task-runner axis

knope's tagline is "handle all the tasks most developers find tedious," and its
`knope.toml` workflows make it a genuine task runner: any workflow is a named
list of steps, and `Command` steps run arbitrary shell. rigsmith answers the same
need but splits it deliberately:

- **Release orchestration** is `relrig release` over an optional
  `.changeset/release.jsonc` — a *thin sequencer* (steps with
  `before`/`after`/`run`/`args`/`confirm`/`forge`, plus lazy `vars` for secret
  capture and a secret masker). The design discipline is to keep it glue and
  *not* let it grow into a general-purpose task runner (see
  [RELEASE-ORCHESTRATOR.md](RELEASE-ORCHESTRATOR.md)).
- **Everyday dev tasks** are `rig` — convention-first detection of the right
  native command per ecosystem (`rig build`/`test`/`run`/`format`/`coverage`/
  `doctor`/`cd`), plus workspace-declared script verbs.

So where knope offers one configurable funnel for both release and chores,
rigsmith offers a thin release sequencer *and* a separate, convention-driven dev
launcher.

## Configuration shape

Both are "configurable pipelines," but knope's config is a list of general
workflows while relrig's is a thin sequencer over a fixed engine.

```toml
# knope: knope.toml — named workflows, each an ordered list of steps
[package]
versioned_files = ["Cargo.toml"]
changelog = "CHANGELOG.md"

[[workflows]]
name = "release"
[[workflows.steps]]
type = "PrepareRelease"     # reads commits + changesets, bumps, writes changelog
[[workflows.steps]]
type = "Command"            # arbitrary shell — e.g. publish to a registry
command = "cargo publish"
[[workflows.steps]]
type = "Release"            # tag + GitHub/GitLab/Gitea release
```

```jsonc
// relrig: .changeset/release.jsonc — thin sequencer over engine steps
{
  "vars":  { "npmOtp": { "command": ["op", "item", "get", "npm", "--otp"], "lazy": true } },
  "steps": {
    "commit":        { "before": ["rig test"], "message": "chore: release" },
    "publish":       { "args": ["--otp", "${vars.npmOtp}"], "confirm": "Publish?" },
    "githubRelease": { "forge": "auto" }
  },
  "order": ["version", "commit", "publish", "push", "githubRelease"]
}
```

The version/changelog logic in relrig lives in the `core` engine (cascade,
grouping, manifest stamping); `release.jsonc` only orders and decorates those
fixed steps. In knope, `PrepareRelease` and `Release` are themselves steps you
arrange inside whatever workflow you author, alongside arbitrary `Command`s.

## Which to pick

- **Reach for knope** when: you want **conventional commits** to drive (or
  co-drive) versions, you need **GitLab or Gitea** releases or issue-tracker
  integration, you like one tool that scripts **general workflows** (issues, PRs,
  branches, commands) as well as releases, and your packages release largely
  independently of one another.
- **Reach for relrig** (with `rig`) when: you want **changesets as the single
  source of release intent**, you have a **polyglot monorepo** that needs a real
  cross-package **dependency cascade** and linked/fixed/lockstep grouping in one
  planned run, you want **built-in registry publishing** across .NET/Node/Go/Rust
  and a **language-neutral plugin** contract, and you prefer a thin auditable
  release sequencer kept separate from a convention-first dev launcher.

In one line: **knope decides versions from commits *and* changesets and is one
configurable runner for releases and chores; relrig decides versions from
changesets alone, plans a cross-package cascade across a polyglot monorepo, and
keeps release glue thin while `rig` handles the dev loop.**

## Sources

- [knope](https://knope.tech/) — homepage and docs
- [knope on GitHub](https://github.com/knope-dev/knope)
- [knope concepts: ChangeSet](https://knope.tech/reference/concepts/changeset/)
- [knope concepts: Conventional commits](https://knope.tech/reference/concepts/conventional-commits/)
- [knope: packages / versioned files](https://knope.tech/reference/config-file/packages/)
- [knope step: PrepareRelease](https://knope.tech/reference/config-file/steps/prepare-release/)
- [knope step: Release](https://knope.tech/reference/config-file/steps/release/)
- [knope GitHub Action](https://github.com/knope-dev/action)
