# shipRig

rigsmith's release tool — the Go successor to net-changesets. One uniform
`add changeset → version → publish` workflow across .NET, Node, and Go.

## The release pipeline

`shiprig release` runs an ordered, configurable pipeline. Each stage is a built-in
step you can reorder, disable, or wrap with `before`/`after` hooks and confirm
gates in `.changeset/release.jsonc`:

```
version → commit → publish → tag → push → release → artifacts
```

| step        | what it does                                                                       |
|-------------|------------------------------------------------------------------------------------|
| `version`   | parse changesets, cascade bumps, stamp manifests, write `CHANGELOG.md`             |
| `commit`    | stage + commit the version bump                                                    |
| `publish`   | push each package to its registry (npm / crates.io / NuGet; a no-op for tag-native Go) |
| `tag`       | create the per-package git tag (`pkg@1.2.3`, or `dir/v1.2.3` for Go modules)       |
| `push`      | push commits + tags to the remote                                                  |
| `release`   | create the forge release (GitHub / GitLab / Gitea) with notes from the changelog   |
| `artifacts` | build + attach cross-platform binaries (e.g. goreleaser)                           |

> **Build status (pre-1.0).** In active design, not yet wired: `tag` (today folded
> into `publish`), the multi-forge `release` (today the GitHub-only `githubRelease`
> step), and `artifacts`. See
> [../docs/RELEASE-STEPS-AND-FORGES-DESIGN.md](../docs/RELEASE-STEPS-AND-FORGES-DESIGN.md)
> and [../docs/RELEASE-PIPELINE-DESIGN.md](../docs/RELEASE-PIPELINE-DESIGN.md).

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
