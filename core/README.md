# rigsmith/core

The shared engine behind `rig` and `relrig`. **Zero external dependencies**
(stdlib only) — kept that way on purpose.

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
| `ecosystem/{dotnet,node,gomod,cargo}` | built-in language adapters (reference impls of `plugin.Ecosystem`) |
| `gitutil` / `prestate` / `since` / `walkutil` | git tags + merge-base diffs, pre.json, changed-files mapping, ignore-aware walking |

`testdata/parity/` is the cross-implementation golden corpus (22 scenarios,
Node + C# oracles) — see its README for provenance and the regeneration rule.

```sh
go test ./...
```

See [../docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) and
[../docs/PLUGIN-PROTOCOL.md](../docs/PLUGIN-PROTOCOL.md).
