# shipRig

rigsmith's release tool — the Go successor to net-changesets. One uniform
`add changeset → version → publish` workflow across .NET, Node, and Go.

```sh
shiprig init
shiprig add -p my/pkg --bump minor -m "Add a feature"   # interactive without flags
shiprig status --verbose
shiprig version            # bump + changelog, with dependency cascade
shiprig info
```

`version` runs the shared engine in `rigsmith/core`: it parses changesets,
cascades bumps to dependents, applies linked/fixed/lockstep grouping, stamps the
new versions into each ecosystem's manifest, and writes `CHANGELOG.md`.

The full surface is wired: `init`, `add`, `status` (incl. `--since` and
`--output`), `version` (normal/pre/snapshot, changelog enrichment + `format:`),
`pre`, `info`, `ui`, `tag`, `publish` (idempotent, confirm-gated on a TTY,
`--yes` for CI), and `release` — the configurable step pipeline
(`.changeset/release.jsonc`: steps/hooks/vars/confirm gates/secret masking,
GitHub forge releases). See [../docs/FEATURE-PARITY.md](../docs/FEATURE-PARITY.md).
