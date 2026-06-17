# changeRig

The lean changeset tool — the changeset lifecycle (`init → add → status →
version`) isolated from release orchestration. It's the same shared engine that
powers [shipRig](/shiprig/), without the publish/tag machinery. Aliased
`changeset`.

```sh
changerig init                                # create .changeset/ (--source changesets|commits|both)
changerig add -p my/pkg --bump minor -m "…"   # write a changeset (interactive without flags)
changerig status --verbose                    # show the pending release plan
changerig browse                              # browse/manage pending changesets (alias: ls / list)
changerig version                             # bump versions + write CHANGELOG.md
changerig pre enter next                       # enter prerelease mode (changerig pre exit to leave)
changerig changelog add -m "…" -t fix          # hand-author a CHANGELOG entry (also: changelog format)
changerig info                                # resolved config + discovered packages
changerig config show                          # view/edit .changeset/config.json
changerig doctor                              # health-check the setup (--fix to scaffold config)
changerig ui                                  # interactive bubbletea menu
```

`doctor` checks git, the repo, `.changeset/config.json` (and offers to scaffold
it when it's missing), and the packages discovered across every ecosystem — the
same shared report/fix model the other rigs use.

It works across **.NET, Node, Go, and Rust** in the same polyglot monorepo. The
`version` step runs the [core](/core/) engine: it parses changesets, cascades
bumps to dependents, applies linked/fixed/lockstep grouping, stamps the new
versions into each ecosystem's manifest, and writes `CHANGELOG.md`.

## changeRig vs shipRig

`changerig` is the lifecycle; [`shipRig`](/shiprig/) is the front door that adds
`tag`, `publish`, `pre`, and the configurable `release` pipeline on top. Both
share the exact same `add`/`status`/`version` behavior because they import the
same `commands` package. Use changeRig if all you want is changesets and
changelogs; reach for shipRig when you also need to publish.

- [The lifecycle in detail →](./lifecycle)
