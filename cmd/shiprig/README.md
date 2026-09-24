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
shiprig version --changelog --since main   # preview only this branch's entries (writes nothing)
shiprig version --ignore my/app   # leave a package out of this run; its changesets wait
shiprig version --yes --release-as my/pkg=2.0.0   # an exact version, no prompt (CI)
shiprig info
```

In a `rig stack` stackspace the pipeline adjusts itself: `version` stamps
nothing into a member's manifest (the number is recorded in
`.changeset/versions.json` and reaches the build as `${version.<pkg>}`), and
`tag`, `push` and `release` are skipped — a fused history is never tagged or
pushed. `shiprig version --no-stamp` (or `versioning.stamp: false`) does the
same anywhere.

`versioning.record: true` keeps a release record beside it (`released` in
`.changeset/versions.json`): the version every package last released at,
stamped or not, which `doctor` checks the manifests and tags against. It's
never a version source, and it's off by default, as canon keeps no record.

A release that stops partway records the step it stopped at, and a later
`shiprig release --from <step>` past it is refused (`--force` overrides): the
steps in between never ran, and `publish` ships what `build` produced. See
[the pipeline docs](../../site/shiprig/pipeline.md#resuming-a-release).

`publish` and `tag` speak @changesets v3's output contract for a calling
action: with `--output <file>` or `$CHANGESETS_OUTPUT` set, each appends one
`{"type":"git-tag","tag":…,"packageName":…}` line per tag it creates, skips a
tag already present locally or on the remote, and pushes nothing. The tags stay
local for the caller to push (changesets/action, shiprig-action). `publish`
checks that the file can be written before it touches any registry.

`version` runs the shared engine in `rigsmith/core`: it parses changesets,
cascades bumps to dependents, applies linked/fixed/lockstep grouping, stamps the
new versions into each ecosystem's manifest, and writes `CHANGELOG.md`.

It follows @changesets v3, which changes two things a release job notices:

- **Nothing pending is an error.** `shiprig version` with no changesets (and no
  prerelease waiting to graduate) exits 1. Check first in a script; the release
  action already does. The `release` pipeline checks for you: with nothing
  pending it skips the built-in `version` step ("no pending changesets") and
  runs the rest, so a publish-only release still works. A custom `version`
  step (`run` or `script`) is never skipped.
- **Private packages are left alone by default.** A `"private": true` package
  is treated as ignored — not versioned, tagged or released — unless the
  changeset config sets `privatePackages`. `{ "version": true }` versions it;
  add `"tag": true` for it to get a git tag and a forge release too. An app
  that is private only to stay off a registry (an Electron app) wants both.

The full surface is wired: `init`, `add`, `status` (incl. `--since` and
`--output`), `version` (normal/pre/snapshot, changelog enrichment + `format:`;
`--since <ref>` narrows a `--changelog` or `--dry-run` preview to a branch's
changesets and commits, and a run that writes refuses it),
`pre`, `info`, `ui`, `tag`, `publish-plan` (what a publish would release, as
`changeset publish-plan`), `pack` (build those packages into a directory, as
`changeset pack`), `publish` (idempotent, confirm-gated on a TTY;
`--from-pack-dir` pushes what `pack` built, building nothing;
`--yes` for CI), and `release` — the configurable step pipeline
(`.changeset/release.jsonc`: steps/hooks/vars/confirm gates/secret masking,
GitHub forge releases). See [../docs/FEATURE-PARITY.md](../docs/FEATURE-PARITY.md).
