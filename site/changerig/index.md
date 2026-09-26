# changeRig

The lean changeset tool — the changeset lifecycle (`init → add → status →
version`) isolated from release orchestration. It's the same shared engine that
powers [shipRig](/shiprig/), without the publish/tag machinery. Aliased
`changeset`.

```sh
changerig init                                # create .changeset/ (--source changesets|commits|both, --changelog github|default)
changerig add -t feat -p my/pkg -m "…"        # write a changeset (interactive without flags)
changerig status --verbose                    # show the pending release plan
changerig browse                              # browse/manage pending changesets (alias: ls / list)
changerig version                             # bump versions + write CHANGELOG.md
changerig pre enter next                       # enter prerelease mode (changerig pre exit to leave)
changerig changelog add -m "…" -t fix          # hand-author a CHANGELOG entry (also: changelog format)
changerig info                                # resolved config + discovered packages
changerig config show                          # view/edit .changeset/config.json
changerig config show --json                   # the config as parsed, defaults applied (for scripts)
changerig doctor                              # health-check the setup (--fix to scaffold config)
changerig ui                                  # interactive bubbletea menu
```

`config show` prints the config file as written, wherever it resolved from.
`config show --json` is for scripts: the same config parsed (JSONC comments and
trailing commas gone), defaults applied, ecosystem blocks kept, and
`versioning.source` always present with its effective value.

`doctor` checks git, the repo, `.changeset/config.json` (and offers to scaffold
it when it's missing), and the packages discovered across every ecosystem — the
same shared report/fix model the other rigs use. With a
[release record](lifecycle#record) kept, it also checks the manifests and tags
against it.

It works across **.NET, Node, Go, and Rust** in the same polyglot monorepo.
Changesets name packages by name, so every discovered package needs a name of
its own, across ecosystems too: two packages under one name (an npm package and
a Go module both called `shared`, say) stop every command with an error naming
both, instead of one silently standing in for the other. Rename one, or narrow
discovery so only one is found: `paths`, an ecosystem's `sourcePath`, or, for a
regex ecosystem, its `packages` list. `ignore` can't separate them, because it
matches by name too. The
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
