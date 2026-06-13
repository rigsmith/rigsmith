# rigsmith

A Go monorepo housing a family of convention-first, zero-runtime-dependency CLI
tools and the shared engine they're built on:

| Module | Binary | What it is |
|---|---|---|
| [`core/`](core/) | — | `github.com/rigsmith/core` — the shared engine: semver, changeset parsing, the release planner (cascade + grouping), the plugin contract, the built-in ecosystem adapters (.NET, Node, Go, Rust), and `pathmap` (cross-OS path resolution). No external dependencies. |
| [`cli/`](cli/) | `rig` | The convention-first dev launcher (run/build/test/format across .NET, Node, Go, Rust). Successor to the .NET/Node [`rig`](https://github.com/JohnCampionJr/rig). |
| [`changerig/`](changerig/) | `changerig` | The lean changeset tool: the lifecycle (init → add → status → version) isolated from release orchestration. Exports the shared `commands` package relrig reuses. Aliased `changeset`. |
| [`release/`](release/) | `relrig` | The release front door: everything changerig does, plus publish/tag/pre orchestration. Successor to [net-changesets](../net-changesets). |
| [`clauderig/`](clauderig/) | `clauderig` | Sync your Claude Code setup (config, skills, session history) across machines via a private git repo, with cross-OS path correction and secret stripping. See [docs/CLAUDERIG-DESIGN.md](docs/CLAUDERIG-DESIGN.md). |

Both binaries are single, statically-linked Go executables — the north-star
property: no .NET runtime, no Node, installable via `curl | sh` / Homebrew /
Scoop on any machine John roams onto.

## Status

**At parity — ported 2026-06-11/12.** The shared engine, both CLIs, and the
release orchestrator are built and tested at functional parity with the two
.NET source projects (see [docs/FEATURE-PARITY.md](docs/FEATURE-PARITY.md) for
the feature audit and [test-parity.md](test-parity.md) for per-suite test
coverage). Behavior is pinned by a cross-implementation golden corpus
(`core/testdata/parity/`, 22 scenarios verified against live Node @changesets
and the live C# binary). [claude-questions.md](claude-questions.md) records
the decisions made along the way.

### What works today

```sh
# in any polyglot monorepo (.NET / Node / Go / Rust):
changerig init                              # create .changeset/
changerig add -p my/pkg --bump minor -m "…" # write a changeset (interactive without flags)
changerig status --verbose                  # show the pending release plan
changerig version                           # bump versions + write CHANGELOG.md, with dependency cascade
changerig ui                                # interactive bubbletea menu
relrig publish                              # registries + tags (idempotent, confirm-gated on a TTY)
relrig release                              # the configurable step pipeline (.changeset/release.jsonc)

rig info                                    # what rig discovered
rig build / test / run / format             # the right native command for the detected ecosystem
rig coverage --min 80 / kill / doctor / cd  # the dev-loop verbs (see cli/README.md for all)
```

The release engine — changeset parsing, the dependency **cascade** (a dependent
is patch-bumped when its dependency releases), **linked/fixed/lockstep**
grouping, version bumping, and changelog rendering — is a faithful port of
net-changesets and is exercised end-to-end by `changerig version` across all four
ecosystems (`examples/demo` is a ready-made polyglot repo to try it on).

**Changelog generators are pluggable**: the built-in renderer dogfoods the same
JSON contract external plugins speak; `examples/plugins/changeset-changelog-changelogen`
is a Node reference plugin producing changelogen-style output. Set
`"changelog": "<plugin>"` in config to swap it in.

## Design

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — module layout, the shared-core boundary, how `rig` and `relrig` relate.
- [docs/PLUGIN-PROTOCOL.md](docs/PLUGIN-PROTOCOL.md) — the one extension mechanism (subprocess + versioned JSON) for both ecosystem adapters and changelog generators, and why built-ins dogfood it.
- [docs/PORTING-PLAN.md](docs/PORTING-PLAN.md) — the original staged porting plan (historical; the port is complete — see FEATURE-PARITY.md for current state).
- [docs/FEATURE-PARITY.md](docs/FEATURE-PARITY.md) — exhaustive feature-by-feature parity audit of rigsmith against net-changesets and rig (.NET + Node).
- [docs/RELEASE-ORCHESTRATOR.md](docs/RELEASE-ORCHESTRATOR.md) — design for `relrig release` + `.changeset/release.jsonc` (the configurable pipeline), mapped from net-changesets. **Built** (`release/internal/pipeline` + `forge`).

## Building

```sh
go build ./...          # from any module dir; the repo is a go.work workspace
go test ./...           # core has full unit tests
go build -o bin/relrig ./release
go build -o bin/rig ./cli
```

### Running a dev build alongside the installed binaries

To dogfood the tools from source without disturbing a globally installed
`rig`/`relrig`/etc., install `<tool>-dev` launchers — they run the working tree
via `go run` and coexist with the stable binaries:

```sh
rig dev-install         # or: rig run dev-install, or: go run ./scripts/dev-install
```

This writes `rig-dev`, `relrig-dev`, `clauderig-dev`, … to `~/.local/bin` (sh
wrappers on macOS/Linux, `.cmd` on Windows). Each recompiles the current source
on every run, so edits take effect with no reinstall. The launchers are
discovered from `go.work`, so a new workspace module gets one automatically.

`rig dev-install` works because rig surfaces any `main` package the workspace
declares under `scripts/` or `cmd/` (in `go.work`) as a bare `rig <name>` verb —
the Go counterpart to how it exposes a Node repo's `package.json` scripts. These
verbs are exact-match only (excluded from prefix-matching) and never shadow a
built-in.
