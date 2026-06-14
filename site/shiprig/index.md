# shipRig

RigSmith's release tool — the Go successor to net-changesets. One uniform
`add changeset → version → publish` workflow across .NET, Node, and Go.

```sh
shiprig init
shiprig add -p my/pkg --bump minor -m "Add a feature"   # interactive without flags
shiprig status --verbose
shiprig version            # bump + changelog, with dependency cascade
shiprig publish            # registries + tags (idempotent, confirm-gated on a TTY)
shiprig release            # the configurable step pipeline
shiprig info
```

`version` runs the shared engine in [`rigsmith/core`](/core/): it parses
changesets, cascades bumps to dependents, applies linked/fixed/lockstep
grouping, stamps the new versions into each ecosystem's manifest, and writes
`CHANGELOG.md`.

## The full surface

The whole workflow is wired:

- `init`, `add`, `status` (incl. `--since` and `--output`)
- `version` (normal / pre / snapshot, changelog enrichment + `format:`)
- `pre` — enter/exit prerelease mode
- `info`, `ui`
- `tag` — create the git tags for the released versions
- `publish` — idempotent, confirm-gated on a TTY, `--yes` for CI
- `release` — the [configurable step pipeline](./pipeline)

## shipRig vs changeRig

[`changeRig`](/changerig/) is the changeset lifecycle on its own. shipRig is
everything changeRig does **plus** `tag`, `publish`, `pre`, and `release`. They
share the same `add`/`status`/`version` code, so the changeset half behaves
identically.

- [The release pipeline →](./pipeline)
