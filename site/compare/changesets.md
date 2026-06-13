# RigSmith vs @changesets/cli

[@changesets/cli](https://github.com/changesets/changesets) is the tool the
changeset workflow comes from, and RigSmith deliberately speaks its format:
the `.changeset/*.md` files and `config.json` are compatible. changeRig and
shipRig are a faithful port of that model — pinned against a golden corpus that
includes live @changesets output — with a few deliberate differences.

| | @changesets/cli | changeRig / shipRig |
|---|---|---|
| Runtime | Node | Single static Go binary — no Node required |
| Ecosystems | npm / JavaScript packages | .NET, Node, Go, Rust in one repo |
| Changeset format | `.changeset/*.md` | Same format (compatible) |
| Dependency cascade | Yes | Yes (range-aware, ported behavior) |
| Linked / fixed groups | Yes | Yes, plus lockstep |
| Publish | npm | Per-ecosystem native publish, via the plugin contract |
| Release orchestration | GitHub Action + bespoke scripts | `shiprig release` — a configurable pipeline (`.changeset/release.jsonc`) |
| Extensibility | JS changelog functions | Subprocess JSON plugin contract (any language) |

## When to use which

**Use @changesets/cli** if you're a pure-npm monorepo already invested in the
Node toolchain and its ecosystem of GitHub Actions.

**Use changeRig / shipRig** if your repo is polyglot (or just isn't Node), if you
want a single binary with no runtime to install, or if you want the release
*orchestration* (`shiprig release`) as a first-class, config-driven pipeline
rather than hand-rolled CI scripts.

Because the changeset format is shared, moving an existing @changesets repo to
changeRig is mostly a matter of swapping the binary — your `.changeset/`
directory comes along unchanged.
