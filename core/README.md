# rigsmith/core

The shared engine behind `rig` and `relrig`. **Zero external dependencies**
(stdlib only) — kept that way on purpose.

| Package | What |
|---|---|
| `semver` | SemVer 2.0.0 + node-semver bump rules |
| `changeset` | `.changeset/*.md` parse/render (shared @changesets format) |
| `config` | `.changeset/config.json` schema |
| `planner` | release plan: cascade, linked/fixed/lockstep grouping, changelog rendering |
| `plugin` | the extension contract (ecosystem adapters + changelog generators) |
| `ecosystem/{dotnet,node,gomod}` | built-in language adapters (reference impls of `plugin.Ecosystem`) |

```sh
go test ./...
```

See [../docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) and
[../docs/PLUGIN-PROTOCOL.md](../docs/PLUGIN-PROTOCOL.md).
