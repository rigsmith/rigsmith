# Velopack desktop release (local, no registry)

A worked `shiprig release` pipeline for a single-app .NET desktop app packaged
with [Velopack](https://velopack.io) and released entirely from a developer
machine — no NuGet/registry publish, no CI handoff. Modeled on the Halyards app.

- [`config.jsonc`](./config.jsonc) — changeset config; sets `tagTemplate` so the
  release tags `vX.Y.Z` instead of `Name@X.Y.Z`.
- [`release.jsonc`](./release.jsonc) — the pipeline: `version → commit → build →
  tag → push → release`.

## What this example exercises

Three shiprig features that let a real desktop pipeline stay declarative instead
of falling back to hand-written shell in every step:

| Feature | Replaces |
|---------|----------|
| `${version}` = the **new** (bumped) version, resolved from changesets at plan time (plus `${lastVersion}` / `${nextVersion}`) | Re-reading the bumped version out of the `.csproj` with `grep`/`cut` in each custom step |
| `commit.paths` — scope the release commit to specific files | A custom `commit.run` doing `git add <files> && git commit` to avoid `git add -A` sweeping WIP |
| `tagTemplate: "v${version}"` — single-app tag convention, honored by the tag/publish/forge steps alike | A custom `tag.run` calling `git tag -a "v$V"` after grepping the version |

What still needs a custom step: the Velopack packaging itself (`./pack.sh all`)
and the Velopack-aware GitHub upload (`./release-github.sh`, `vpk upload github`).
First-class Velopack packaging/feeds is a possible future addition.
