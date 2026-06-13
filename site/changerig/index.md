# changerig

The lean changeset tool — the changeset lifecycle (`init → add → status →
version`) isolated from release orchestration. It's the same shared engine that
powers [relrig](/relrig/), without the publish/tag machinery. Aliased
`changeset`.

```sh
changerig init                                # create .changeset/
changerig add -p my/pkg --bump minor -m "…"   # write a changeset (interactive without flags)
changerig status --verbose                    # show the pending release plan
changerig version                             # bump versions + write CHANGELOG.md
changerig ui                                  # interactive bubbletea menu
```

It works across **.NET, Node, Go, and Rust** in the same polyglot monorepo. The
`version` step runs the [core](/core/) engine: it parses changesets, cascades
bumps to dependents, applies linked/fixed/lockstep grouping, stamps the new
versions into each ecosystem's manifest, and writes `CHANGELOG.md`.

## changerig vs relrig

`changerig` is the lifecycle; [`relrig`](/relrig/) is the front door that adds
`tag`, `publish`, `pre`, and the configurable `release` pipeline on top. Both
share the exact same `add`/`status`/`version` behavior because they import the
same `commands` package. Use `changerig` if all you want is changesets and
changelogs; reach for `relrig` when you also need to publish.

- [The lifecycle in detail →](./lifecycle)
