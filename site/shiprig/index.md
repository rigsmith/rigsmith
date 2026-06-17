# shipRig

RigSmith's release tool — the Go successor to net-changesets. One uniform
`add changeset → version → publish` workflow across .NET, Node, Go, and Rust,
plus Tauri and Electron desktop apps.

```sh
shiprig init               # release-init wizard: pipeline, forge, publish auth
shiprig add -p my/pkg --bump minor -m "Add a feature"   # interactive without flags
shiprig status --verbose
shiprig version            # bump + changelog, with dependency cascade
shiprig publish            # registries (idempotent, confirm-gated on a TTY)
shiprig release            # the configurable step pipeline
shiprig release --dry-build # build artifacts locally, publish nothing
shiprig doctor             # health-check changesets + release readiness
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
- `release` — the [configurable step pipeline](./pipeline) with step filtering
  (`--only` / `--skip` / `--from` / `--to`), `--dry-run`, and `--dry-build`
- `doctor` — the changeset baseline (git/repo/config/workspace) plus a release
  section: `gh` auth and the publish/build tool each detected ecosystem needs

Beyond the basics, the `release` pipeline adds multi-forge releases
(GitHub / GitLab / Gitea), OIDC trusted publishing + 1Password/secret-manager
auth for npm / crates.io / NuGet, Tengo scripting (`if` gates, computed `vars`,
`script` steps), a cross-platform portable shell, an `issues` step that
comments on and closes resolved issues, and code-signing for Tauri / Electron
artifacts. See [the release pipeline](./pipeline).

## shipRig vs changeRig

[`changeRig`](/changerig/) is the changeset lifecycle on its own. shipRig is
everything changeRig does **plus** `tag`, `publish`, `pre`, and `release`. They
share the same `add`/`status`/`version` code, so the changeset half behaves
identically.

- [The release pipeline →](./pipeline)
