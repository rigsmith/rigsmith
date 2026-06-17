# core

The shared engine behind `rig`, `changerig`, and `shiprig`. **Zero external
dependencies** (standard library only) — kept that way on purpose.

It's a Go module (`github.com/rigsmith/core`), not a CLI you install; it's
documented here because it's where the release logic actually lives.

| Package | What |
|---|---|
| `semver` | SemVer 2.0.0 + node-semver bump rules + npm range matching |
| `changeset` | `.changeset/*.md` parse/render (shared @changesets format) |
| `config` | `.changeset/config.json` schema (changelog/format specs, ignore globs) |
| `planner` | release plan: range-aware cascade, linked/fixed/lockstep grouping, pre/snapshot, changelog rendering |
| `changelog` | changelog-git/-github enrichment + release-line decoration + file writer |
| `mdfmt` | native prettier-equivalent markdown formatter + `format:` dispatch |
| `jsonc` | tolerant JSONC parse + comment-preserving editor |
| `plugin` | the extension contract (ecosystem adapters + changelog generators) |
| `ecosystem/{dotnet,node,gomod,cargo,electron,tauri,regex}` | built-in adapters (reference impls of `plugin.Ecosystem`): four language adapters, two desktop ecosystems, and a generic regex adapter |
| `gitutil` / `prestate` / `since` / `walkutil` | git tags + merge-base diffs, pre.json, changed-files mapping, ignore-aware walking |
| `pathmap` | cross-OS path resolution (used by claudeRig) |

## Parity corpus

`testdata/parity/` is the cross-implementation golden corpus — 22 scenarios with
both Node (@changesets) and C# (net-changesets) oracles — that pins RigSmith's
behavior against the implementations it was ported from.

- [The plugin protocol →](./plugin-protocol)
