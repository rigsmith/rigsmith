# relrig

rigsmith's release tool — the Go successor to net-changesets. One uniform
`add changeset → version → publish` workflow across .NET, Node, and Go.

```sh
relrig init
relrig add -p my/pkg --bump minor -m "Add a feature"   # interactive without flags
relrig status --verbose
relrig version            # bump + changelog, with dependency cascade
relrig info
```

`version` runs the shared engine in `rigsmith/core`: it parses changesets,
cascades bumps to dependents, applies linked/fixed/lockstep grouping, stamps the
new versions into each ecosystem's manifest, and writes `CHANGELOG.md`.

Wired: `init`, `add`, `status`, `version`, `info`. Scaffolded (stubs): `publish`,
`tag`, `pre`. See [../docs/PORTING-PLAN.md](../docs/PORTING-PLAN.md).
