# shipRig vs. release-it

How rigsmith's release tool (`shiprig`) compares to
[release-it](https://github.com/release-it/release-it) — an excellent,
battle-tested, configurable release automation tool. release-it is a great choice
for a huge number of projects; shipRig exists not because release-it falls short
but because rigsmith wanted a different *model* (polyglot monorepo + committed
changeset intent + a single zero-dependency binary). Both automate the
`bump → changelog → commit → tag → publish → GitHub release` chain and both are
configurable end to end, so the interesting differences are in the model each one
uses, not in which tool is "better."

> Companion docs: [RELEASE-ORCHESTRATOR.md](RELEASE-ORCHESTRATOR.md) (the
> `shiprig release` pipeline) and [FEATURE-PARITY.md](FEATURE-PARITY.md).

## TL;DR

- **release-it** is a polished, mature Node CLI that releases **one package per
  run**, derives the version bump from **git tags / commit history** (or a
  recommended bump via the conventional-changelog plugin), and is extended through
  a rich, well-maintained **plugin ecosystem**. For a vast range of projects —
  especially Node, but well beyond it via plugins — it's the batteries-included
  default, and an easy recommendation.
- **shipRig** is a single static Go binary that releases a **whole polyglot
  monorepo per run**, derives the bump from **explicit changeset files** authored
  alongside each change, cascades that bump through the dependency graph, and
  orchestrates the publish chain via a thin, optional `release.jsonc` pipeline.

They sit at different points on the same axis: release-it infers *what to
release* from VCS state; shipRig records *what to release* as committed intent and
then plans the cascade across many packages and ecosystems.

## At a glance

| | **shipRig** | **release-it** |
|---|---|---|
| Runtime | Single static Go binary, zero runtime deps | Node.js ≥ 20.19 |
| Install | `curl \| sh` / Homebrew / Scoop | `npm i -D release-it` (or `npx`) |
| Ecosystems | .NET, Node, Go, Rust (+ generic `regex` adapter) | npm-first; other targets via plugins/config |
| Version source | **Explicit changeset files** (captured intent) | **Git tags + commit history** (or conventional-changelog plugin) |
| Unit of release | The **whole monorepo** — multi-package plan in one run | **One package** per run (monorepo via per-package runs) |
| Dependency cascade | Built in — dependents auto-bumped; linked/fixed/lockstep grouping | Not modeled; workspace bumps are scripted per recipe |
| Changelog | Aggregated from changesets; pluggable generators (JSON contract) | From commit log / conventional-changelog plugin / keep-a-changelog |
| Pipeline config | Optional `.changeset/release.jsonc` (steps/hooks/vars/gates/order) | `.release-it.json` / `package.json` / `.js` config + lifecycle hooks |
| Forge releases | GitHub (via `gh`), per package, notes from CHANGELOG | GitHub **and** GitLab, first-class |
| Extensibility | Subprocess + versioned-JSON plugin contract (adapters + changelog) | In-process JS plugin API (npm packages) |
| Interactive vs CI | Auto by TTY; rich bubbletea TUI; `--yes` for CI | Interactive by default; `--ci` for automation |
| Dry run | `--dry-run` (masks secrets, resolves the plan) | `--dry-run` |
| Secrets | Lazy `vars` capture (e.g. OTP from `op`) + output masker | Hooks/env; OIDC trusted publishing for npm |

## The core difference: changesets vs. commit/tag inference

This is the decision that drives everything else.

**release-it** looks at the repository's git state. By default it reads the
latest tag, computes the next version (or you pick interactively), generates a
changelog from the commits since that tag, and publishes. With
`@release-it/conventional-changelog` it instead *recommends* the bump by parsing
Conventional Commits. The "what changed and how much" lives in your commit
messages and tag history.

**shipRig** uses the changesets model (it's the Go successor to net-changesets,
itself a port of the @changesets workflow). Each meaningful change ships with a
small changeset file (`changerig add` / `shiprig add`) declaring which packages it
touches and at what bump level, plus prose for the changelog. At release time the
planner reads the accumulated changesets, computes per-package bumps, **cascades**
them to dependents, applies **linked/fixed/lockstep** grouping, stamps every
ecosystem's manifest, and aggregates the changelog.

Trade-offs:

- **Intent vs. inference.** Changesets capture release intent at PR time, decoupled
  from commit hygiene — you don't need disciplined Conventional Commits, and the
  changelog prose is written deliberately. release-it needs either a human in the
  loop or a commit convention to know the bump.
- **Multi-package planning.** Because shipRig holds the whole set of pending changes
  and the dependency graph, one run produces a coherent multi-package plan with a
  cascade. release-it releases a single package per invocation; a monorepo is a
  scripted sequence of runs (per its monorepo recipe), and inter-package bump
  propagation is something you arrange yourself.
- **Friction.** Changesets add a per-change authoring step. release-it has zero
  extra files when you're happy releasing from tags/commits.

## What release-it does brilliantly

- **Maturity and ecosystem.** Years of production use across thousands of projects,
  a large and active plugin catalog (`bumper`, `keep-a-changelog`, calver, Gitea,
  pnpm, and more), and deep npm integration including modern **OIDC trusted
  publishing**.
- **Genuinely extensible beyond npm.** The plugin API is real reach, not a
  footnote — there are community plugins for .NET, pnpm, Gitea, and others. If a
  target isn't covered, a plugin is a tractable amount of JS. (rigsmith's author
  wrote a .NET adapter for release-it years ago — the model works.)
- **GitLab.** First-class GitLab releases alongside GitHub; shipRig's forge step is
  GitHub-only today (via `gh`).
- **In-process JS plugins.** If you live in Node, authoring or pulling a plugin is
  `npm install` and a JS module — lower ceremony than a subprocess plugin for
  Node-shaped extensions.
- **Single-package simplicity.** For one package, release-it is less machinery:
  no changeset files, no monorepo planner to reason about. That simplicity is a
  feature, and for most repos it's the right amount of tool.

## How shipRig differs

- **Polyglot, by design.** One tool versions/publishes .NET, Node, Go, and Rust in
  the *same* repo, plus a generic `regex` adapter for arbitrary version files —
  through one ecosystem-neutral plugin contract. release-it is npm-centric and
  reaches other targets through config/plugins.
- **Monorepo as the default unit.** The dependency cascade, linked/fixed/lockstep
  grouping, and aggregated multi-package changelog are core, not a recipe you
  assemble.
- **Zero-runtime-dependency distribution.** A single static binary installs via
  `curl | sh` / Homebrew / Scoop with no Node toolchain — shipRig's north-star
  property. release-it requires Node ≥ 20.19 on every machine that releases.
- **Language-neutral plugins.** The subprocess + versioned-JSON contract means a
  changelog generator or adapter can be written in any language (the built-ins
  dogfood the same JSON contract an external plugin speaks).
- **Deliberate changelog prose.** Notes are authored in changesets at change time,
  not reconstructed from commit subjects.

## Configuration shape

Both are "configurable pipelines with hooks," but the layering differs.

release-it centers a single config object (`.release-it.json` /
`package.json#release-it` / `.release-it.js`) with sections for `git`, `github`,
`gitlab`, `npm`, `hooks` (lifecycle scripts like `before:init`,
`after:bump`, `before:git:release`), and a `plugins` map. One run = one package's
lifecycle.

shipRig keeps versioning/changelog in the `core` engine and treats `release` as a
**thin sequencer**. The optional `.changeset/release.jsonc` defines `order`,
per-`steps` config (`before`/`after`/`run`/`args`/`confirm`/`message`/`forge`),
global `hooks`, and lazy `vars` for secret capture, with `${...}` interpolation
and a secret masker. The discipline is explicit: keep it glue, don't let it drift
into a general-purpose task runner. (See
[RELEASE-ORCHESTRATOR.md](RELEASE-ORCHESTRATOR.md).)

```jsonc
// shiprig: .changeset/release.jsonc — thin sequencer over engine steps
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

```jsonc
// release-it: .release-it.json — one package's lifecycle + plugins
{
  "git":    { "commitMessage": "chore: release v${version}", "requireCleanWorkingDir": true },
  "github": { "release": true },
  "npm":    { "publish": true },
  "hooks":  { "before:init": ["npm test"], "after:bump": "npm run build" },
  "plugins": { "@release-it/conventional-changelog": { "preset": "angular" } }
}
```

## Which to pick

- **Reach for release-it** when: you're releasing a **Node/npm** project (or a few,
  one at a time), you want a mature tool with a deep plugin ecosystem, you need
  **GitLab** releases or npm OIDC publishing, and you're fine driving version from
  tags/commits (optionally Conventional Commits).
- **Reach for shipRig** when: you have a **polyglot monorepo** (.NET/Node/Go/Rust),
  you want **explicit, reviewed release intent** with a real cross-package
  **dependency cascade** and grouping, you want a **single static binary** with no
  Node runtime on release machines, and you want the publish chain orchestrated by
  a thin, auditable pipeline.

In one line: **release-it infers the release from your VCS history, one package at
a time; shipRig plans the release from committed changeset intent, across the whole
polyglot monorepo at once.**

## Sources

- [release-it on GitHub](https://github.com/release-it/release-it)
- [release-it on npm](https://www.npmjs.com/package/release-it)
- [release-it monorepo recipe](https://github.com/release-it/release-it/blob/main/docs/recipes/monorepo.md)
- [@release-it/conventional-changelog](https://github.com/release-it/conventional-changelog)
</content>
</invoke>
